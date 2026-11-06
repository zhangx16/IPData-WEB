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
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"
)

//go:embed cloud.html
var page string

func main() {
	c := defaults()
	cli := flag.Bool("cli", false, "命令行扫描")
	bind := flag.String("listen", "127.0.0.1:13335", "Web监听地址")
	file := flag.String("file", "", "IP/CIDR文本文件")
	out := flag.String("out", "results.jsonl", "CLI结果文件")
	format := flag.String("format", "jsonl", "jsonl 或 csv")
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
	flag.Parse()
	if *cli {
		if *file != "" {
			b, e := os.ReadFile(*file)
			if e != nil {
				log.Fatal(e)
			}
			c.Targets = string(b)
		}
		ts, e := prepare(c)
		if e != nil {
			log.Fatal(e)
		}
		if *format != "csv" && *format != "jsonl" {
			log.Fatal("format 必须为csv或jsonl")
		}
		f, e := os.Create(*out)
		if e != nil {
			log.Fatal(e)
		}
		defer f.Close()
		enc := json.NewEncoder(f)
		cw := csv.NewWriter(f)
		if *format == "csv" {
			if e = cw.Write([]string{"ip", "port", "provider", "evidence", "success", "rounds", "median_ms"}); e != nil {
				log.Fatal(e)
			}
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		done, hits := 0, 0
		scan(ctx, c, ts, func(r Result) {
			done++
			if r.Success > 0 {
				hits++
			}
			if *format == "csv" {
				e = cw.Write([]string{r.IP, strconv.Itoa(r.Port), csvSafe(r.Provider), r.Evidence, strconv.Itoa(r.Success), strconv.Itoa(r.Rounds), fmt.Sprintf("%.2f", r.Median)})
			} else {
				e = enc.Encode(r)
			}
			if e != nil {
				log.Fatal(e)
			}
			fmt.Fprintf(os.Stderr, "\r已测 %d/%d，命中 %d", done, len(ts), hits)
		})
		cw.Flush()
		if e = cw.Error(); e != nil {
			log.Fatal(e)
		}
		fmt.Fprintln(os.Stderr)
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, page)
	})
	slot := make(chan struct{}, 1)
	mux.HandleFunc("/api/scan", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "application/json required", 415)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
			http.Error(w, "origin rejected", 403)
			return
		}
		select {
		case slot <- struct{}{}:
			defer func() { <-slot }()
		default:
			http.Error(w, "已有扫描运行", 409)
			return
		}
		var cfg Config
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		d.DisallowUnknownFields()
		if e := d.Decode(&cfg); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		ts, e := prepare(cfg)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Cache-Control", "no-store")
		enc := json.NewEncoder(w)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		send := func(v any) {
			rc := http.NewResponseController(w)
			_ = rc.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if e := enc.Encode(v); e != nil {
				cancel()
				return
			}
			if e := rc.Flush(); e != nil {
				cancel()
			}
		}
		send(map[string]any{"total": len(ts)})
		scan(ctx, cfg, ts, func(r Result) { send(r) })
		if ctx.Err() == nil {
			send(map[string]any{"done": true})
		}
	})
	s := &http.Server{Addr: *bind, Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32768}
	log.Printf("IPData-WEB http://%s （网页扫描断开即停止；CLI输出持久化）", *bind)
	if !strings.HasPrefix(*bind, "127.0.0.1:") {
		log.Print("当前Web无登录认证，请在可信网络或认证反向代理后使用")
	}
	log.Fatal(s.ListenAndServe())
}

func csvSafe(s string) string {
	if len(s) > 0 && strings.ContainsRune("=+-@\t\r\n", rune(s[0])) {
		return "'" + s
	}
	return s
}
