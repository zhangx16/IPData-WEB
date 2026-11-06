# IPData-WEB

参考 [PoemMisty/CFData-WEB](https://github.com/PoemMisty/CFData-WEB) 的优选流程，新建的独立 Go 云厂商 IP 优选项目。原 CFData 功能已移除。提供 Web/CLI、内置 VLESS 真连接、可选带宽测速、任务持久化与暂停续扫。

## Windows 便携软件

[下载 Windows 版本](https://github.com/zhangx16/IPData-WEB/releases/latest)。普通 Intel/AMD 电脑选择 amd64 ZIP，Windows ARM 电脑选择 arm64 ZIP。完整解压后双击 `IPData-WEB.exe`，自动打开本地界面，无需安装 Go、Python、Node、sing-box 或 Xray。

程序内置 VLESS 的 WS+TLS 和普通 TCP+TLS 客户端。Windows 任务固定保存在 EXE 旁的 `ipdata-tasks`；端口占用时默认自动换用可用本地端口。详见 [Windows说明](docs/windows.md)。

## 构建与启动

Go 1.23+；WebSocket 模块在构建时下载并编入程序，许可证见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

```sh
cd combined_refactor
go test -race ./...
mkdir -p ../release_assets
go build -trimpath -o ../release_assets/ipdata-web .
cd ..
./release_assets/ipdata-web -data ./ipdata-tasks
```

打开 http://127.0.0.1:13335 。界面按电脑端优化，提供任务工具条、进度统计、可折叠配置与批量结果操作；[UI审查与静态稿](docs/ui/desktop-review.md)。手动输入 IP/CIDR 或导入文本，没有内置 DMIT 网段选择或网段数据接口。支持 IPv4/IPv6、多个端口、筛选、排序、CSV/JSON 导出，**点击结果 IP:端口复制纯 IP**（IPv6 不含方括号和端口）。点击行其他位置查看证据。

## 三种测试模式

| 模式 | 行为 |
| --- | --- |
| CF链路初筛 | 固定连接候选IP，以CF域名作为SNI/Host检测CF响应和Trace |
| CF初筛后验证VLESS | 对CF命中候选，使用模板再建立完整VLESS通道请求HTTPS目标 |
| 仅测VLESS真连接 | 对全部候选直接使用模板验证，不依赖CF初筛结果 |

粘贴完整 `vless://` 分享链接，程序只替换服务器 IP 和端口，保留模板 UUID、SNI、Host、WS 路径。当前支持 `security=tls`、`encryption=none` 的 WS 与普通 TCP；不支持 Reality、Vision/flow、gRPC、TLS 指纹模拟、WS early data 和 TCP HTTP 伪装，遇到这些参数会明确报错。

CF域名与模板参数分开：`one.one.one.one` 是CF第二个测试域名，真连接会自动从模板提取业务SNI/Host。已知候选可选“仅测VLESS”，两个CF域名不参与。无需把整条分享链接或业务SNI填到CF复验域名框。

候选入口IP地区与模板出口无需一一对应，同一个美国模板可以测试不同地区入口。出口由业务后端决定，换入口不会自动指定出口国家；每个候选仍须按该模板实际验证。换模板需重新测试。

真连接默认在通道内访问 `https://www.cloudflare.com/cdn-cgi/trace`，记录成功轮数、TTFB中位数、HTTP状态和代理出口IP。默认Trace地址还会校验返回的h、fl、colo和ip；自定义HTTPS地址接受200或204。内层HTTPS始终验证证书，无直连回退，不使用系统环境代理。

## 可选下载测速

网页“带宽测速”中勾选“启用下载测速”；不勾选只测连通性，不下载测速文件。

CF模式只对全部主域名轮次Trace有效的候选下载；VLESS模式只对真连接成功的候选**通过VLESS通道下载**，不会用直连结果冒充代理带宽。默认1MiB，每候选最多32MiB，独立超时；不跟随重定向，拒绝HTML/JSON、压缩响应和不足量数据。

结果分别显示CF与VLESS延迟，存在VLESS结果时按VLESS成功率/延迟筛选排序。可仅显示全部轮次通过的候选，或按下载速率排序。下载Mbps按正文读取时间计算，不含握手；完整请求总耗时保存在证据中，小文件结果不代表持续带宽。

## 下载成功结果批量导出节点

结果区点击“导出 VLESS 节点”，可使用本任务已保存模板或粘贴其他完整 VLESS 分享链接。选择当前筛选、已勾选或任务全部下载成功项，下载每行一个分享链接的 TXT，可导入 v2rayN 等客户端。

只导出成功下载且速度大于0的结果；替换IP与端口，按IP:端口去重，保留UUID、SNI、Host、路径及原始查询参数。IPv6地址自动加方括号。备注默认追加新地址，也可保留原备注。更换模板不会重新验证链路。勾选全选仅作用于当前显示的最多500行，“当前筛选/全部成功”导出则以服务器完整结果为准。

**节点TXT包含模板凭据。** 这与不含模板的普通结果CSV/JSON不同；导出不会修改任务保存的模板。

## 目标规模、并发与速率

- 每任务默认最多 **200万个IP:端口**，网页的“单任务目标上限”或CLI的`-max-targets`可继续提高，没有固定应用层最高值。
- 单个CIDR不再有65536地址限制。程序先合并重叠范围并计数，启动扫描时按需生成目标，创建任务不展开全部地址。
- 并发数为任意正整数，不再设512上限；实际worker数不超过待测目标数。
- 每秒请求数为任意非负整数，不再设2000上限；**0表示不限速**。初筛、复验、真连接和下载共用该任务的限速器。
- 默认仍为32并发、每秒50请求；实际承载能力取决于电脑资源。结果日志逐条落盘，当前结果和历史结果载入后保存在内存中，扩大任务规模也会增加结果占用。
- 最多16个端口；CF请求超时100–30000ms，VLESS单轮超时100–60000ms，重复轮数1–10。输入请求体最多64MiB。只扫描自有或获授权目标。

过大的IPv6范围会在计数阶段根据当前任务上限拒绝，不会尝试展开整个/48或/0。大范围需要同时调整任务上限并具备足够资源。

## 任务与模板保存

配置保存为`<id>.json`，完整结果逐条追加到`<id>.jsonl`并fsync。刷新或关闭网页不停止任务；暂停取消在途请求，继续时跳过已经完整落盘的目标。重启后未完成任务转为暂停，需手动继续。启动时从日志恢复计数，修复末尾半条记录；中间损坏会报错，不静默丢弃。

**为支持续扫，VLESS模板会以明文保存在本机任务文件中。** 列表/详情接口隐藏模板，CSV/结果JSON不含模板。任务目录备份包含凭据，不要公开上传。Linux文件以0600创建，Windows访问权限由所在目录ACL决定。

同一目录只允许一个进程，操作系统文件锁会在进程退出后释放。Web与CLI不可同时打开同一目录；同一实例同时运行一个任务。暂无任务删除、修改已有任务配置或自动调度功能。备份请先停止程序，再复制完整目录。

## CLI

将自己的完整VLESS分享链接保存到UTF-8文件，避免凭据出现在命令参数中：

```sh
./release_assets/ipdata-web -cli -mode vless -vless-file private-template.txt \
  -file authorized-ips.txt -ports 443 -rounds 2 -rate 0 -concurrency 1000 \
  -max-targets 2000000 -speed=false -name '模板真连接' -out results.jsonl

# 同时开启通道下载测速（不需要外部代理核心）
./release_assets/ipdata-web -cli -mode combined -vless-file private-template.txt \
  -file authorized-ips.txt -ports 443 -speed=true -speed-bytes 1048576 \
  -node-timeout 10000 -speed-timeout 10000 -format csv -out results.csv

# Ctrl+C / SIGTERM暂停；继续沿用原配置和已保存模板
./release_assets/ipdata-web -cli -data ./ipdata-tasks -resume TASK_ID -out results.jsonl
./release_assets/ipdata-web -data ./ipdata-tasks -tasks
```

CF模式使用`-mode cf`。为兼容旧版，单独设置`-speed-url`也会启用下载，显式`-speed=false`优先关闭。旧任务仍按原保存配置运行。

## 验证与来源

[批量导出与桌面UI验证报告](docs/testing-v0.4.0.md)及[内置VLESS验证报告](docs/testing-v0.3.0.md)记录WS/TCP测试、错误认证无直连回退、内层证书验证、下载开关、模板恢复脱敏和IP复制。Windows amd64/arm64会交叉构建，当前尚无原生Windows实机测试。

既有 [2026-11-06抽样报告](docs/testing-2026-11-06.md)和[指定节点报告](docs/supplied-node-test-2026-11-06.md)为历史记录，后者对应v0.2.0使用外部核心的验证。`data-sources/`仅为来源备查，不内置到程序。可选脚本`tools/update_dmit.py`只更新离线文本/JSON，不探测IP，不恢复界面选择。

CF响应头和Trace可被仿造，命中不能单独证明Reality或业务代理可用。协议编码参考 [Xray VLESS实现](https://github.com/XTLS/Xray-core/blob/main/proxy/vless/encoding/encoding.go)；WebSocket帧处理使用Gorilla。

## 部署与许可证

默认只监听本机且无登录认证。远程可用SSH隧道：`ssh -L 13335:127.0.0.1:13335 user@your-server`，或放在有认证的HTTPS反向代理后。CLI/Web任务与证据格式相同。

采用GPL-3.0，见[LICENSE](LICENSE)；依赖许可证随便携包提供。
