# Windows 便携版

适用于 Windows 10/11，提供普通 Intel/AMD 64位（amd64）和 Windows ARM64 两种包。使用内嵌本地网页作为操作界面，无需安装 Go、Python、Node 或代理客户端。当前 EXE 提供 IP/HTTPS 链路优选，不内置完整 VLESS 客户端。

## 使用

1. 普通电脑下载 `IPData-WEB-windows-amd64.zip`；ARM64电脑选择 `arm64` 包。
2. 完整解压到可写入的文件夹，例如桌面或D盘目录。
3. 双击 `IPData-WEB.exe`（或 `Start-IPData.cmd`）。程序保留控制台并自动打开默认浏览器。
4. 页面中输入目标IP/CIDR、端口与SNI，开始测试。默认仅监听 `127.0.0.1`，不需要管理员权限。
5. 默认端口13335若被占用，双击启动时会自动选择另一个本地端口，正确网址会显示在控制台；手动指定 `-listen` 时按指定端口执行，不自动更改。

浏览器未自动打开时，手动访问控制台显示的网址。启动失败会显示错误窗口，也可以通过CMD脚本保留错误输出。

## 数据与退出

Windows默认任务目录固定为EXE旁的`ipdata-tasks`，从快捷方式、不同工作目录或命令行打开时都使用这个位置。可以用`-data`覆盖。升级时替换EXE即可，保留整个任务目录。

关闭浏览器不会退出程序。退出建议先点击“暂停”，再在控制台按Ctrl+C；直接关闭控制台或强制结束时，已完整落盘的记录仍能在重启后恢复，未完成目标在继续扫描时重试。

不要同时打开两个实例操作同一任务目录；第二个实例会提示目录被占用。导出CSV/JSON的文件保存在浏览器下载位置。

## PowerShell示例

```powershell
.\IPData-WEB.exe -version
.\IPData-WEB.exe -open=false
.\IPData-WEB.exe -listen 127.0.0.1:13336 -data 'D:\IPData任务'
```

关闭Web实例后，也可使用CLI，和网页共用持久化格式：

```powershell
.\IPData-WEB.exe -cli -file .\authorized-ips.txt -ports 443,8443 -out .\results.jsonl
.\IPData-WEB.exe -cli -resume TASK_ID -out .\results.jsonl
```

## 在自己电脑上验证节点

扫描结果反映**运行EXE的电脑到候选IP再到CF的路径**，不是服务器部署位置的测试结果。使用TUN/VPN时请将候选IP设为直连，避免测到既有代理的路径。

CF响应头、双域名Trace和下载成功仍不等于某个VLESS节点必然可用。VLESS+WS+TLS还要保留该节点的UUID、SNI、Host与路径，用实际代理客户端测试。`-version`可查看程序版本、源码提交与架构；ZIP内附有许可证与构建清单。

## 源码打包

在有Go与Python3的开发机器上执行（可在Linux交叉编译）：

```sh
python3 tools/build_windows.py --version v0.2.0
```

输出位于`release_assets/windows-v0.2.0/`，包含两种架构的EXE、便携ZIP、校验和以及构建清单。软件用户无需安装构建工具。

## 已验证范围

Linux功能测试、竞态检测、真实子进程重启恢复测试及Windows两种架构交叉构建；Windows专用浏览器启动/错误弹窗使用系统API。当前没有原生Windows电脑可执行实机验证，不将交叉编译或兼容层运行宣称为Windows实测。
