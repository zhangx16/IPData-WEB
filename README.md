# IPData-WEB

参考 [PoemMisty/CFData-WEB](https://github.com/PoemMisty/CFData-WEB) 的优选工作流，新建的独立 Go 云厂商 IP 优选项目。已去掉原 CFData 官方 CF 网段扫描、测速源下载、订阅上传及兼容入口；仅保留针对用户输入 IP/CIDR 的 HTTPS CF 链路探测。新探测代码使用 Go 标准库，无外部模块。

## 构建与运行

需要 Go 1.23+：

```sh
cd combined_refactor
go test -race ./...
mkdir -p ../release_assets
go build -trimpath -o ../release_assets/ipdata-web .
../release_assets/ipdata-web
```

浏览器打开 http://127.0.0.1:13335 。支持导入文本、IPv4/IPv6、多个端口、服务商标签、SNI、二次域名复验、成功率/延迟/证据筛选及 CSV/完整 JSON 导出。页面最多展示前500条筛选结果，导出包含全部筛选记录。服务商名称只是标签，不会自动获取或保证对应网段归属。

默认端口443/8443、32并发、每秒50请求、4秒超时、3轮探测；每个请求（含复验）共用速率限制。单网段最多65536地址，单任务最多100000个IP:端口。IPv4网段包括网络和广播地址，IPv6只枚举输入的小网段。

```sh
./release_assets/ipdata-web -cli -file authorized-ips.txt -ports 443,8443 \
  -provider DMIT -sni www.cloudflare.com -verify-sni one.one.one.one \
  -concurrency 32 -rate 50 -rounds 3 -out results.jsonl
# CSV输出
./release_assets/ipdata-web -cli -targets 192.0.2.1 -format csv -out results.csv
```

默认校验证书。`-insecure` / 网页勾选可关闭验证，仅用于观察特征。文档IP为示例，请替换为你拥有或获得授权的目标。CLI逐条写入文件；网页结果只在页面内存中，刷新不会恢复。网页断开或点击停止会取消扫描；暂无暂停续扫、持久化任务或带宽测速。

## 探测与证据

固定连接候选 IP:端口，使用配置的域名同时作为 TLS SNI 与 HTTP Host，请求 `/cdn-cgi/trace`；不使用环境代理，不跟随重定向。每次记录TCP、TLS、TTFB、总时间、HTTP状态、CF-Ray、CF机房及trace出口IP。

- `cf_headers`：至少一次成功响应含 `Server: cloudflare` 或 `CF-Ray`。
- `trace_valid`：全部主域名轮次返回有效trace（状态200，h匹配、colo三字符、fl非空、ip为有效地址）。
- `dual_verified`：在上一项基础上第二域名也返回有效trace。

CF响应头、trace均可仿造；这些等级表示观察到的响应，不证明使用Reality、不证明是第三方转发节点，也不保证VLESS或任意业务流量可用。TTFB中位数来自成功轮次，包含TCP/TLS建连；成功率表示请求成功率，不是ICMP丢包率。排序先按成功率降序，再按TTFB升序。

Reality客户端不携带服务端Private Key；服务端持有私钥，客户端使用对应的认证材料。未通过认证的连接可能转发至配置的target/dest。域名目标也可能解析到CF Anycast，不能把“填域名”当成避免CF地址的保证。SNI、目标证书及Host路由需实际匹配，不能假定任意SNI均可用。参见 [Reality官方说明](https://github.com/XTLS/REALITY) 与 [Cloudflare SNI/Host不匹配说明](https://developers.cloudflare.com/support/troubleshooting/http-status-codes/cloudflare-1xxx-errors/error-1013/)。本项目不修改服务器Reality配置。

## 部署与权限

默认只监听本机，无登录认证。远程使用推荐SSH隧道，或置于具备认证的HTTPS反向代理后：

```sh
ssh -L 13335:127.0.0.1:13335 user@your-server
```

可用 `-listen 127.0.0.1:13335` 调整地址。单Web实例同时运行一个扫描；全局速率与并发按该任务配置。导出完整JSON可能包含网络错误信息与出口IP。

## 来源与许可证

工作流参考 CFData-WEB，本项目采用 GPL-3.0（见 LICENSE）。原 CFData Go/UI功能已移除，当前扫描引擎与页面为独立实现。未内置第三方云厂商网段清单，避免把过期或不明归属地址作为默认探测对象。
