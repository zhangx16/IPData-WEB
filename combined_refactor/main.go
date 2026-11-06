package main

import (
	"context"
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed cloud.html
var page string

var buildVersion = "dev"
var buildCommit = "unknown"
var desktopErrors bool

func main() {
	setupConsole()
	if e := run(); e != nil {
		log.Print(e)
		if desktopErrors {
			showStartupError(e)
		}
		os.Exit(1)
	}
}
func run() error {
	c := defaults()
	cli := flag.Bool("cli", false, "命令行扫描")
	bind := flag.String("listen", "127.0.0.1:13335", "Web监听地址")
	file := flag.String("file", "", "IP/CIDR文本文件")
	out := flag.String("out", "results.jsonl", "CLI结果导出路径")
	format := flag.String("format", "jsonl", "jsonl 或 csv")
	executable, e := os.Executable()
	if e != nil {
		return e
	}
	dir := flag.String("data", defaultDataDirectory(executable, runtime.GOOS), "持久化任务目录（Windows默认程序所在目录；同一时刻只允许一个进程使用）")
	autoOpen := flag.Bool("open", runtime.GOOS == "windows", "启动Web后自动打开默认浏览器；CLI模式忽略")
	version := flag.Bool("version", false, "显示版本与构建信息后退出")
	resume := flag.String("resume", "", "继续已有任务ID（沿用原配置）")
	name := flag.String("name", "", "任务名称")
	list := flag.Bool("tasks", false, "列出持久化任务并退出")
	flag.StringVar(&c.Targets, "targets", "", "IP/CIDR，逗号分隔")
	flag.StringVar(&c.Ports, "ports", c.Ports, "端口")
	flag.StringVar(&c.SNI, "sni", c.SNI, "SNI及Host")
	flag.StringVar(&c.VerifySNI, "verify-sni", c.VerifySNI, "第二域名，空值关闭")
	flag.IntVar(&c.Concurrency, "concurrency", c.Concurrency, "并发")
	flag.IntVar(&c.Rate, "rate", c.Rate, "每秒请求上限")
	flag.IntVar(&c.Timeout, "timeout", c.Timeout, "请求超时毫秒")
	flag.IntVar(&c.Rounds, "rounds", c.Rounds, "重复轮数")
	flag.BoolVar(&c.Insecure, "insecure", false, "关闭证书校验，仅作特征探测")
	flag.StringVar(&c.Provider, "provider", "custom", "服务商标签")
	flag.StringVar(&c.SpeedURL, "speed-url", "", "可选HTTPS下载测速地址（空值关闭）")
	flag.Int64Var(&c.SpeedBytes, "speed-bytes", c.SpeedBytes, "每个通过Trace的目标最多下载字节数")
	flag.IntVar(&c.SpeedTimeout, "speed-timeout", c.SpeedTimeout, "单次测速超时毫秒")
	flag.Parse()
	if *version {
		fmt.Printf("IPData-WEB %s (%s) %s/%s\n", buildVersion, buildCommit, runtime.GOOS, runtime.GOARCH)
		return nil
	}
	desktopErrors = runtime.GOOS == "windows" && !*cli && !*list
	m, e := newManager(*dir)
	if e != nil {
		return fmt.Errorf("无法打开任务目录 %s：%w；请选择可写入的解压目录，或通过 -data 指定目录", *dir, e)
	}
	defer m.close()
	if *list {
		return json.NewEncoder(os.Stdout).Encode(m.list())
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *cli {
		if *format != "csv" && *format != "jsonl" {
			return fmt.Errorf("format 必须为csv或jsonl")
		}
		id := *resume
		if id == "" {
			if *file != "" {
				b, e := os.ReadFile(*file)
				if e != nil {
					return e
				}
				c.Targets = string(b)
			}
			t, e := m.create(*name, c)
			if e != nil {
				return e
			}
			id = t.ID
		} else if *file != "" || c.Targets != "" {
			return fmt.Errorf("resume沿用原配置，请勿同时传入file或targets")
		}
		log.Printf("任务ID %s；Ctrl+C暂停；继续：-cli -data %s -resume %s", id, *dir, id)
		if e = m.start(id); e != nil {
			return e
		}
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
	loop:
		for {
			select {
			case <-ctx.Done():
				if e = m.pause(id); e != nil {
					return e
				}
				m.wg.Wait()
				break loop
			case <-ticker.C:
				t, _, e := m.get(id, 0, 1)
				if e != nil {
					return e
				}
				fmt.Fprintf(os.Stderr, "\r已测 %d/%d，命中 %d，%s", t.Completed, t.Total, t.Hits, t.Status)
				if t.Status != "running" && t.Status != "pausing" {
					break loop
				}
			}
		}
		fmt.Fprintln(os.Stderr)
		m.mu.Lock()
		t := taskCopy(m.tasks[id])
		rs := append([]Result{}, m.tasks[id].Results...)
		m.mu.Unlock()
		f, e := os.Create(*out)
		if e != nil {
			return e
		}
		e = writeResults(f, rs, *format)
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		log.Printf("已导出 %d 条至 %s", len(rs), *out)
		if t.Status == "failed" {
			return fmt.Errorf("任务失败: %s", t.Error)
		}
		return nil
	}
	explicitListen := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "listen" {
			explicitListen = true
		}
	})
	listener, e := listenForApp(*bind, runtime.GOOS == "windows" && !explicitListen)
	if e != nil {
		return fmt.Errorf("无法监听 %s：%w", *bind, e)
	}
	defer listener.Close()
	url := browserURL(listener.Addr())
	s := &http.Server{Addr: listener.Addr().String(), Handler: api(m), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32768}
	errCh := make(chan error, 1)
	go func() { errCh <- s.Serve(listener) }()
	log.Printf("IPData-WEB %s %s；任务保存在 %s，网页关闭后任务继续运行", buildVersion, url, *dir)
	if *autoOpen {
		if e := openBrowser(url); e != nil {
			log.Printf("%v；请手动打开 %s", e, url)
		}
	}
	if !strings.HasPrefix(*bind, "127.0.0.1:") {
		log.Print("Web无登录认证，请在可信网络或认证反向代理后使用")
	}
	select {
	case e := <-errCh:
		if e != http.ErrServerClosed {
			return e
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := s.Shutdown(shutdown); e != nil {
			return e
		}
	}
	return nil
}

func listenForApp(address string, fallback bool) (net.Listener, error) {
	l, e := net.Listen("tcp", address)
	if e != nil && fallback {
		return net.Listen("tcp", "127.0.0.1:0")
	}
	return l, e
}
func csvSafe(s string) string {
	if len(s) > 0 && strings.ContainsRune("=+-@\t\r\n", rune(s[0])) {
		return "'" + s
	}
	return s
}
func writeResults(w io.Writer, rs []Result, format string) error {
	if format == "json" {
		return json.NewEncoder(w).Encode(rs)
	}
	if format == "jsonl" {
		enc := json.NewEncoder(w)
		for _, r := range rs {
			if e := enc.Encode(r); e != nil {
				return e
			}
		}
		return nil
	}
	cw := csv.NewWriter(w)
	if e := cw.Write([]string{"ip", "port", "provider", "evidence", "success", "rounds", "median_ms", "speed_mbps", "speed_bytes", "speed_error"}); e != nil {
		return e
	}
	for _, r := range rs {
		mbps, n, se := "", "", ""
		if r.Speed != nil {
			mbps = fmt.Sprintf("%.3f", r.Speed.Mbps)
			n = strconv.FormatInt(r.Speed.Bytes, 10)
			se = r.Speed.Error
		}
		if e := cw.Write([]string{r.IP, strconv.Itoa(r.Port), csvSafe(r.Provider), r.Evidence, strconv.Itoa(r.Success), strconv.Itoa(r.Rounds), fmt.Sprintf("%.2f", r.Median), mbps, n, csvSafe(se)}); e != nil {
			return e
		}
	}
	cw.Flush()
	return cw.Error()
}
