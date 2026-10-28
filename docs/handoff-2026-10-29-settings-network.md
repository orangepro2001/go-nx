# 交接：端口与监听地址可在设置里改；设置页分区、开机启动（2026-10-29）

接着 `handoff-2026-10-29-port-shared.md`（0.7.2：发现 minipc 的 tailnet 地址被另一个程序占着）。

## 用户要求

> 应该是NightDesk的，能用其他端口吗？（把端口配置项也放到程序界面里方便用户控制）（当前设置项太少，rustdesk提供了一大串的可配置项，参考一下）

附图是 RustDesk 的设置页（常规 / 安全 / 网络 / 显示 / 账户 / 打印机 / 关于；服务启停、主题、语言、硬件编解码、音频输入、录屏、其他一长串勾选项）。

## 做了什么（0.8.0）

| 改动 | 文件 |
|---|---|
| **设置 → 网络**：UDP 端口（输入框 + 应用 / 回车）和监听地址（"所有地址"或本机某个地址，Tailscale 地址带标记），**立即生效**：结束当前会话、关掉旧监听、在新地方监听。绑不上就回到原来的地方、不保存，页面上写明原因（例如"UDP 7000 被 nx-host.exe (process N) 占用"）。保存为设置 `port`、`listen_ip`。命令行给了 `-port` / `-listen` 时以命令行为准，设置页只显示 | `cmd/go-nx/host_windows.go`（`bind`、`unbind`、`SetListen`、`newAcceptor`），`internal/app/pages_settings.go`（`networkCard`） |
| `remote.ListenHost` / `HostListener.Shutdown`：自己持有 UDP 套接字，`Shutdown` 当场结束所有连接并释放地址（quic-go 的 `ListenAddr` 监听在最后一个连接排空前一直占着端口，换回同一端口会失败） | `internal/remote/tls.go` |
| 启动时选定的地址还不在本机上（开机时 Tailscale 还没起来）：每 5 s 重试，"本机"页写明"在等这个地址" | `cmd/go-nx/host_windows.go`（`bindWhenThere`） |
| 主机的本机地址每 10 s 刷新一次（原来只在启动时取一次） | `watchPort` |
| **控制端按端口记主机身份**：默认端口 7000 仍按 IP 记（旧记录有效），其他端口按 `IP:端口` 记。minipc 上 NightDesk 的 nx-host 占 7000、go-nx 改到 7001 后，两者各记各的 | `internal/remote/tls.go`（`pinKey`） |
| **设置页分区**：常规（外观、语言、开机启动）/ 共享（允许 Win+L、帧率）/ 网络 / 会话（自动重连、한/영）/ 关于（更新、诊断）。窄窗口时分区按钮换行 | `internal/app/pages_settings.go` |
| **开机启动**：当前用户 Run 键，值 `go-nx` = `"<exe>" -hidden`，不要管理员、不装服务 | `cmd/go-nx/autostart_windows.go`，`app.Platform.Autostart/SetAutostart` |
| **允许对方按 Win+L**（原来只能改 `settings.json`）：即时生效 | `hostState.SetAllowWinL` |
| 端口被占的提示改成"关掉那个程序，或在 设置 → 网络 里换端口" | `cmd/go-nx/winapi_windows.go` |

### RustDesk 里有、这次没加的

音频输入、录屏、打印机、文件传输、硬件编解码、ID / 中继服务器、账户、代理：go-nx 没有这些功能，加开关是假的。"显示"一类（默认画质、默认显示方式）可以在会话视图的现有功能上做，留待下一轮。

## 验证

- 新测试：`TestHostsOnTwoPortsOfOnePCKeepTheirOwnPins`、`TestHostListenerShutdownFreesTheAddressAtOnce`（remote；后者做过变异检查：`Shutdown` 改成普通 `Close` 时测试失败，"address already in use"）；`TestThePortAndAddressCanBeChangedInTheSettings`、`TestAListenAddressFromTheCommandLineCannotBeChangedHere`、`TestStartupAndWinLSettings`（app）；`TestAutostartAddsAndRemovesTheRunEntry`（Windows，CI 运行器上跑，用单独的值名，测完删掉）。
- 改写了依赖"主机身份按 IP 记"的测试：`TestHostPinnedAfterFirstConnection`、`TestForgetHostLetsAChangedHostBePinnedAgain`、`TestPinHostThenDialSendsThePasswordOnlyToThatIdentity`、`TestAnExpectedFingerprintConnectsAndStoresNothing` 改为先在同一地址记下另一个身份，不再靠"随机端口上的两个主机按 IP 冲突"。
- 预览场景 `settings-sharing`、`settings-network`（带错误信息）、`settings-session`、`settings-about`，布局测试覆盖四种语言 × 四种尺寸；看过中文截图。
- `gofmt`、`go vet`（Linux 与 `GOOS=windows`）、Windows 交叉编译、`xvfb-run -a go test -race ./...` 全绿。
- **没有**：在真机上改端口 / 地址、开机启动 → `ACCEPTANCE.md` H37。主机换地址的逻辑（`bind` / `unbind` / 回滚 / 等地址）只在 Windows 上编译，没有自动化测试，只有 `ListenHost` 本身有。

## 给用户的下一步

1. minipc 更新到 0.8.0，**设置 → 网络**把端口改成 7001（或别的空闲端口），点"应用"。建议同时把监听地址选成 Tailscale 地址 `100.109.77.29`，公网就连不到了。
2. 控制端连 `100.109.77.29:7001`。第一次会让你核对主机 ID：应是 minipc"本机"页上的 `CFFC C257 D5F1 8299`。
3. 想让 go-nx 用 7000，就改 NightDesk 那边 nx-host 的监听端口（NightDesk 设置里的 `NxHostListen`；NightDesk 源码按约定只读，没动）。

## 经验

- quic-go 的 `ListenAddr` 监听关闭后，端口要等连接排空才释放；需要"换地址再换回来"的场景要自己持有套接字。
- 主机身份按 IP 记，在"一个端口一个程序"的前提下才成立；端口能改以后要按端口记。
