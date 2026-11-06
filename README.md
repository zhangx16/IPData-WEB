# IPData-WEB

参考 [PoemMisty/CFData-WEB](https://github.com/PoemMisty/CFData-WEB) 的优选流程，新建的独立 Go 云厂商 IP 优选项目。已移除原 CFData 功能和兼容入口。Go 标准库实现，无第三方运行时依赖，提供 Web 与 CLI。

## 构建与启动

需要 Go 1.23+：

```sh
cd combined_refactor
go test -race ./...
mkdir -p ../release_assets
go build -trimpath -o ../release_assets/ipdata-web .
cd ..
./release_assets/ipdata-web -data ./ipdata-tasks
```

打开 http://127.0.0.1:13335 。支持 IPv4/IPv6、导入 IP/CIDR 文本、多端口、服务商标签、自定义 SNI、双域名验证、任务历史、暂停/继续、重启恢复、延迟/成功率/证据筛选、按带宽排序及 CSV/JSON 导出。页面最多显示500条，筛选导出会拉取该任务的全部完成记录再应用筛选；“导出任务全部 CSV”直接从服务器导出。

默认端口443/8443、并发32、每秒50个请求、单请求4秒、3轮主域名探测。复验和可选下载请求共用同一限速器。单任务最多100000个IP:端口、最多16个端口，单输入网段最多65536地址。较大的范围请分批创建任务。IPv4枚举包含网络/广播地址；IPv6请输入具体IP或小网段，不枚举整个/48。只对自有或获授权目标运行测试。

## 任务持久化与暂停续扫

- 任务配置保存为 `<id>.json`；完整结果逐条追加至 `<id>.jsonl`，每条结果写入后 `fsync`，再更新内存进度。
- 刷新或关闭网页不会取消后台任务。选择历史任务可查看结果；切换任务不影响当前扫描。
- 点击“暂停”取消在途请求，等待收尾后进入暂停状态；点击“继续”跳过已经完整落盘的 IP:端口。
- 正常退出或重启后，未完成任务转为暂停，需要手动继续；不会擅自重启网络扫描。强制终止时未完成的目标会在续扫时重试，已落盘目标不重复。
- 启动时根据日志重算完成数和命中数，修复末尾缺失换行的半条记录；中间损坏会明确报错，不静默丢弃证据。
- 同一任务目录仅允许一个进程，通过操作系统文件锁保护。进程退出后锁自动释放；Web与CLI不能同时打开相同目录。
- 同一实例同时运行一个任务；可以先创建其他暂停任务。暂无修改已有任务配置、任务删除或自动调度功能。

不要手工修改运行中的任务文件。备份时先停止服务，再复制完整任务目录。

## CLI

```sh
./release_assets/ipdata-web -cli -data ./ipdata-tasks \
  -file authorized-ips.txt -ports 443,8443 -provider DMIT -name 'DMIT 自有节点' \
  -sni www.cloudflare.com -verify-sni one.one.one.one \
  -concurrency 32 -rate 50 -rounds 3 -out results.jsonl

# Ctrl+C 或 SIGTERM 会暂停并导出已完成记录。再次运行按原配置续扫：
./release_assets/ipdata-web -cli -data ./ipdata-tasks -resume TASK_ID -out results.jsonl

# 查看任务
./release_assets/ipdata-web -data ./ipdata-tasks -tasks

# CSV 导出可在创建/续扫命令上加：
# -format csv -out results.csv
```

续扫沿用保存的配置，不修改 SNI、端口、轮数或速率；若需要不同配置，请新建任务。

## 带宽测速

默认关闭。网页展开“带宽测速”填写地址，或在CLI加：

```sh
-speed-url 'https://speed.cloudflare.com/__down?bytes=1048576' \
-speed-bytes 1048576 -speed-timeout 10000
```

仅在**全部主域名轮次返回有效Trace**后下载。测速URL主机名作为测速SNI/Host，TCP连接固定到候选IP:端口，不走系统代理、不跟随重定向。测速URL与探测SNI可以不同，最终是否能转发到该主机需要实际验证。

每个合格候选最多下载设定字节数（1KiB至32MiB），超时范围100ms至60秒。只接受HTTP200、非HTML/JSON、未压缩且足量的响应。失败保留错误和已接收字节数，不给出有效Mbps。每次记录实际字节数、正文读取时间、总耗时、测速SNI及状态。Mbps按正文读取时间计算，不含握手等待；小文件与缓冲会影响测量，不能替代长期稳定性测试。

## DMIT 网段来源

网页“查看 DMIT 网段来源”可查看、勾选并导入IPv4段，不会自动启动扫描。数据为构建时快照，可用以下脚本刷新后重新构建：

```sh
python3 tools/update_dmit.py
```

脚本仅查询公开路由API，不探测网段内IP。查询 AS906、AS32519、AS54574 的 [RIPEstat Announced Prefixes API](https://stat.ripe.net/docs/data-api/api-endpoints/announced-prefixes)，保存查询时间、窗口、各ASN计数及合并后的前缀。DMIT主网络身份可在 [PeeringDB AS906](https://www.peeringdb.com/asn/906) 核对；AS32519见 [RIPEstat AS Overview](https://stat.ripe.net/data/as-overview/data.json?resource=AS32519)。

2026-11-06获取的查询窗口为2026-10-23至2026-11-06，合并后39个IPv4网段（83968地址）、9个IPv6网段。这是窗口内观察到的BGP公告，不是实时设备清单、全部资产清单或扫描授权。

[data-sources/](data-sources/) 同时保存用户提供的46段原清单和来源差异；原清单共91136地址，两个端口为182272个候选，超过单任务上限。部分网段未被此次数据完整覆盖，不将它们自动当成当前AS906资产。

## 探测证据

固定连接候选IP:端口，以同一域名设置TLS SNI和HTTP Host，请求`/cdn-cgi/trace`，不使用环境代理，不跟随重定向。

| 等级 | 含义 |
| --- | --- |
| `cf_headers` | 至少一次完整成功响应包含`Server: cloudflare`或`CF-Ray` |
| `trace_valid` | 全部主域名轮次状态200，trace的h匹配、colo三字符、fl非空、ip有效 |
| `dual_verified` | 上一等级基础上，第二域名也返回有效trace |

默认验证TLS证书；`-insecure`或网页选项可关闭验证作特征探测。CF响应头和trace可被仿造，命中不能证明使用Reality、第三方转发、VLESS可用或任意业务可用。TTFB中位数只统计成功轮次并包含TCP/TLS建连；成功率为HTTPS CF特征请求成功率，不是ICMP丢包率。默认排序为成功率优先，再按TTFB升序。

Reality服务端持有私钥，客户端不携带服务端Private Key。未认证连接可能转发至配置的target/dest；域名也可能解析到CF Anycast，不能把“填域名”视为避免CF地址的保证。参见 [Reality官方说明](https://github.com/XTLS/REALITY) 和 [Cloudflare SNI/Host不匹配说明](https://developers.cloudflare.com/support/troubleshooting/http-status-codes/cloudflare-1xxx-errors/error-1013/)。本项目不修改Reality服务端配置。

## 验证与部署

`go test -race ./...`覆盖TLS、证书检查、重定向限制、分页与操作接口、目录锁、日志损坏、崩溃恢复、已完成目标去重、暂停/重载续扫及限量下载。额外的真实子进程测试需要Python3与openssl：

```sh
python3 tools/e2e_local.py
```

该脚本仅访问本机，实际启动程序、暂停任务、SIGKILL、重启、续扫并检查CSV；结果写入忽略提交的`test-artifacts/`。实际公网样本结果见 [2026-11-06测试报告](docs/testing-2026-11-06.md)。

默认监听本机且无登录认证。远程推荐SSH隧道：

```sh
ssh -L 13335:127.0.0.1:13335 user@your-server
```

也可放在有认证的HTTPS反向代理后；监听地址用`-listen`调整。Windows构建已通过，Windows实机运行尚未验证。Linux支持真实进程崩溃恢复测试。

## 许可证

工作流参考CFData-WEB，当前引擎与页面独立实现；项目采用GPL-3.0，见[LICENSE](LICENSE)。
