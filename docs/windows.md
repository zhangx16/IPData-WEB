# Windows 便携版 v0.3.0

提供Windows 10/11普通Intel/AMD 64位（amd64）和Windows ARM64包。内嵌本地网页与VLESS客户端，无需安装Go、Python、Node、sing-box或Xray。

## 启动与测试

1. 完整解压ZIP到可写文件夹，双击`IPData-WEB.exe`或`Start-IPData.cmd`。
2. 控制台会保留，默认浏览器自动打开；打不开时手动访问控制台网址。默认只监听本机，不需要管理员权限；端口13335被占用时自动选另一个本地端口。
3. 手动输入IP/CIDR或导入文件，填写端口。内置DMIT网段选择与数据接口已移除。
4. 选择“仅测VLESS真连接”或“CF初筛后验证VLESS”，粘贴完整分享链接。支持WS+TLS、普通TCP+TLS；Reality、Vision、gRPC和指纹模拟等会报不支持。
5. 默认不下载测速文件；需要时展开“带宽测速”，勾选“启用下载测速”。VLESS模式的下载会经过模板通道。
6. 点击结果IP:端口可复制**纯IP**，IPv6不含方括号和端口。点击行其他位置查看逐次证据。

`one.one.one.one`是CF第二个测试域名，并不是节点SNI。VLESS真连接会自动使用模板里的SNI/Host/路径；选仅测VLESS时，两个CF域名框禁用。美国出口模板也可以测试其他地区入口，出口仍由业务后端决定。

## 规模与限速

默认每任务200万个IP:端口，可调高“单任务目标上限”。目标在扫描时按需生成，不再限制单CIDR只能65536地址。并发和每秒请求数没有固定数值上限，**每秒请求数填0表示不限速**；默认32并发、50次/秒。实际worker数不超过待测目标，运行能力与结果内存占用取决于电脑资源。

## 数据与退出

任务固定保存在EXE旁的`ipdata-tasks`，从快捷方式或不同工作目录启动仍使用同一位置，可用`-data`覆盖。升级只替换EXE，保留目录。

**模板为续扫而以明文保存在本机任务文件中；备份目录包含节点凭据。** 列表/详情隐藏模板，结果导出不含模板。不要公开上传任务目录。Windows文件权限由所在文件夹ACL决定。

关闭浏览器不退出程序。建议先暂停任务，再在控制台按Ctrl+C。直接关闭控制台或强制结束时，已完整落盘记录仍可恢复，未完成目标在续扫时重试。不要启动两个实例共享同一目录。

CSV/JSON保存在浏览器下载目录。接口与界面隐藏模板，打开历史任务时不会回显UUID；继续任务不需要再次输入，创建新任务则需重新粘贴模板。

## PowerShell

```powershell
.\IPData-WEB.exe -version
.\IPData-WEB.exe -open=false
.\IPData-WEB.exe -listen 127.0.0.1:13336 -data 'D:\IPData任务'
```

停止Web实例后可使用CLI。将自己的完整分享链接保存到UTF-8文件：

```powershell
.\IPData-WEB.exe -cli -mode vless -vless-file .\private-template.txt -file .\authorized-ips.txt -ports 443 -rate 0 -concurrency 1000 -speed=false -out .\results.jsonl
.\IPData-WEB.exe -cli -resume TASK_ID -out .\results.jsonl
```

程序测量的是此电脑→候选→CF/业务后端的路径；若启用了TUN/VPN，应为候选设置直连，避免结果包含既有代理的路径。Mbps按正文读取时间计算，完整耗时见证据，不代表持续带宽。

## 开发打包与验证

开发机需要Go和Python3，软件用户不需要：

```sh
python3 tools/build_windows.py --version v0.3.0
```

输出`release_assets/windows-v0.3.0/`内含两种架构EXE、ZIP、SHA256校验和及源码构建清单。内置依赖许可在`THIRD_PARTY_NOTICES.md`。

已覆盖Linux功能与竞态检测、真实TLS/WS/VLESS协议、错误UUID、证书检查、暂停/重启恢复、浏览器复制及Windows交叉构建；当前未在原生Windows电脑上完成实机验证。
