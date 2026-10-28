# go-nx 实现状态（以代码为准）

- 核对日期：2026-10-28。行号取自基线 `52361cb`（v0.1.0）；之后阶段 1 改动过的行已按行为更新并标注日期，**文件:行 会随改动偏移，以函数 / 常量名为准**
- 用途：`docs/` 里的几份设计文档写的是**设计意图**，不是实现。判断"go-nx 现在到底能做什么"以本文件和代码为准；本文件每条都给出文件:行，改代码后要同步更新。
- 状态：**已实现** / **部分** / **未实现**（设计里提过）/ **不做**（`minimal-plan.md` 明确排除）
- 真实 Windows 上做过的自动化验证只有 CI 的 windows-latest 冒烟（GDI 抓屏的字节顺序、`SendInput` 移动 / 点击 / 按键，见 `ACCEPTANCE.md` §1b）；其余界面与行为都没在 Windows 上跑过。

## 1. 采集与画面

| 项 | 状态 | 实际情况 | 位置 |
|---|---|---|---|
| 采集方式 | 已实现 | GDI `BitBlt`（`SRCCOPY\|CAPTUREBLT`）到 32bpp 自上而下 DIB。**没有** DXGI Desktop Duplication / WGC | `internal/capture/capture_windows.go:36,103-104` |
| 显示器范围 | 已实现（2026-10-28；此前只采主屏） | `-display <primary\|序号\|设备名如 \\.\DISPLAY2>` 或设置 `display` 选择要共享的显示器；采集用该显示器在虚拟桌面里的矩形做 `BitBlt`，所以左侧 / 上方（坐标为负）的副屏也行。注入侧对非主屏用 `MOUSEEVENTF_VIRTUALDESK` + 显示器原点；主屏沿用原来的映射。**一次只共享一块显示器**，没有会话内切换；多屏同时共享、显示器选择界面都没有 | `internal/capture`（`Displays`、`Open`、`SelectDisplay`）、`internal/inject`（`NewDisplay`）、`cmd/go-nx` |
| 分辨率/显示器变化 | 已实现（2026-10-28；此前尺寸启动时固定） | 主机每秒让 `Screen.Refresh` 重新枚举显示器；尺寸或位置变了就重建采集面，下发新尺寸的画面（第一个更新是完整的），并通过 `OnSourceChanged` 重设注入器的坐标映射。查看器本来就按帧头的尺寸重建缓冲。显示器消失则按"抓屏失败"处理。真实显示器热插拔 / 改分辨率**未在实机上验证** | `internal/remote/host.go`、`internal/capture`、`cmd/go-nx/host_windows.go` |
| DPI | 已实现（2026-10-28；此前是 system-aware 的副作用） | 进程在 `main` 最开头设为 **per-monitor v2**（`capture.EnableDPIAwareness`，退路 `SetProcessDPIAware`），采集尺寸和注入坐标都按物理像素。这也让 Ebiten 回到它自己的默认行为（以前是被 `NewScreen` 抢先设成 system-aware）。混合 DPI 多屏 / 125%–200% 缩放**未在实机上验证**（ACCEPTANCE H7、H8） | `internal/capture`、`cmd/go-nx/main_windows.go` |
| 鼠标指针 | 已实现，按需（2026-10-28；此前未实现） | `BitBlt` 不含指针，所以 `Screen.SetCursorVisible(true)` 时用 `GetCursorInfo` + `GetIconInfo` + `DrawIconEx` 把它画进抓到的画面（热点和显示器原点已计入，`internal/cursor` 有几何测试）。**默认关**：只对在 v1 Hello 里带 `CapCursor` 的查看器打开（`DialOptions.WantCursor`，给没有自己指针的手机端用），因为自带指针的桌面查看器会看到两个。主机在 Welcome 里声明 `CapCursor` | `internal/capture`、`internal/remote/host.go`、`internal/cursor` |
| 采集失败恢复 | 已实现（2026-10-28；此前 `Grab` 出错就结束会话） | 能刷新的源（`Refresher`）抓屏失败时会话保留，主机每个 tick 重试 `Refresh` 并上报一次 `capture paused`；恢复后上报 `capture resumed`，查看器期间保留最后一帧；超过 `GrabRetryFor`（默认 60 s）仍不恢复才结束会话。不能刷新的源仍是失败即结束。锁屏 / UAC 的真实行为**未在实机上验证**（ACCEPTANCE H9） | `internal/remote/host.go`、`internal/capture` |
| 帧率 | 部分 | 固定帧率，默认 10（`-fps`）；Ack 驱动的停等流控 + 过期帧取消，不做质量自适应 | `cmd/go-nx/main_windows.go:99`、`host.go:148` |

## 2. tile 与编码

| 项 | 状态 | 实际情况 | 位置 |
|---|---|---|---|
| tile 大小 | 已实现 | 64×64 | `internal/tile/tile.go:13` |
| 像素格式 | 已实现 | 线上与内存都是 BGRA、自上而下；第 4 字节**无定义**（GDI 不保证是 alpha；在 GitHub windows-latest 上实测恰好是 255，不能当契约），查看器渲染时必须丢弃/置 255 | `tile.go:14,24`、`viewer_windows.go:101-106` |
| 差分方式 | 已实现 | 与上一帧逐 tile `bytes.Equal`。**没有** xxHash | `tile.go:66` |
| 编码 | 已实现 | 纯色 / 调色板+RLE / flate(`BestSpeed`) 原始。**没有** LZ4 | `tile.go:18-21,193` |
| 脏矩形、周期随机抽检、`ACCESS_LOST` 重建 | 未实现 | 设计文档 `final-converged-plan.md` 提过 | — |
| 关键帧/刷新请求 | 未实现 | 只有会话首帧是全量 | `tile.go:54` |

## 3. 协议与会话

| 项 | 状态 | 实际情况 | 位置 |
|---|---|---|---|
| 传输 | 已实现 | QUIC（quic-go）+ TLS 1.3，ALPN 固定 `"go-nx"`，keep-alive 5s / idle 45s | `internal/remote/tls.go:22,41-42` |
| 流划分 | 已实现 | 一条双向控制流（Hello/Ack/Input/Receipt）+ 每帧一条主机发起的单向流。**没有** S0/S1/S2/S4 分流 | `internal/proto/proto.go:3-6,18-24` |
| 对恶意主机的上限 | 已实现（2026-10-28） | 公布的帧尺寸 ≤ 16384 px/边、≤ 2²⁵ 像素，否则结束会话（`proto.ErrFrameSize`）；单个更新流 ≤ 132 MiB，超出结束会话；调色板 RLE 展开前校验；raw tile 的 deflate 最多读 size+1 字节；每帧记录数 ≤ tile 数；更新流 15 s 无数据即放弃；主机自己的屏幕超出上限时 `Serve` 直接报错 | `proto.go`（`MaxFrameDim`、`ValidFrameSize`）、`client.go`（`stallReader`）、`tile.go`（`decodePalette`、`Decode`）、`host.go` |
| 版本/能力协商 | **已实现**（2026-10-28；此前没有） | 版本由 TLS 的 ALPN 决定：`go-nx/1`（v1，带长度前缀的控制帧，未知类型可跳过、多余尾字节忽略、主机放行后发 Welcome 通告能力 / 屏幕尺寸 / 名字）和 `go-nx`（v0，冻结，收入了 `MsgText` 与横向滚轮）。主机两个都提供且优先 v1，查看器两个都提供，所以新旧任意组合都能连上。能力位：`CapText`、`CapWheelH` 已使用，`CapResize`、`CapCursor` 已预留。**没有**能力的主机上 `Client.SendText` 返回 `ErrUnsupported`，不会再把老主机的会话弄断。完整规范与兼容矩阵见 `protocol.md` | `internal/proto/v1.go`、`internal/remote/codec.go`、`docs/protocol.md` |
| 输入种类 | 已实现 | 鼠标移动/按下/抬起、竖直滚轮、**横向滚轮**（`KindWheelH`）、按键按下/抬起（Windows VK，`0x100` 位表示扩展键）、**Unicode 文本**（`MsgText`，主机用 `KEYEVENTF_UNICODE` 注入，与键盘布局和输入法状态无关，单条 ≤ 1024 字节 UTF-8，非法 UTF-8 被替换为 U+FFFD）。后两项（2026-10-28）是 NightDesk 客户端已经在发的 v0 扩展，线上字节有逐字节向量测试；文本与其他输入一样有 opID 去重和回执，并且只对当前活动会话生效。**没有** X 键。老版本主机不认识 `MsgText`，收到会直接结束会话，所以只能发给确知支持的主机（见协议版本协商） | `internal/proto`、`internal/remote/{host,input,client}.go`、`internal/inject` |
| opId 与去重 | 已实现 | `u64` opId，主机保留最近 1024 个；opId=0（鼠标移动、按键自动重复）不去重也无回执。**不是** UUID，无 `clientTs` | `proto.go:178`、`host.go:210` |
| 回执状态 | 部分 | 只有 Executed / Failed；没有 Accepted / Timeout | `proto.go:38-39` |
| 未确认提示 | 已实现 | 3 s 没收到回执只提示"不确定"，不自动重试 | `viewer_windows.go:30,170` |
| 单控制者 | 已实现 | 同时只有一个控制者，新连接顶替旧连接，旧会话持有的按键/鼠标键被释放 | `internal/remote/input.go`、`cmd/go-nx/host_windows.go` |
| 断线后重连 | 部分 | 首次拨号最多尝试 4 次（握手时限 2/3/4/6 s）；断线后需手动重连 | `cmd/go-nx/tabs_windows.go:88` |

## 4. 安全与配对

| 项 | 状态 | 实际情况 | 位置 |
|---|---|---|---|
| 严格 / 开放模式（主页按钮） | 已实现（2026-10-28） | 主页的模式按钮在 **Strict**（默认，需配对或密码）和 **OPEN**（不验证）之间切换；严格 → 开放要点两次，开放 → 严格立即生效。判决新增 `AllowedOpen`（因开放而放进来、未验证）：`Policy.Check` 对已配对设备在两种模式下都返回 `Allowed`（所以随便切换不受影响），开放模式下的陌生人返回 `AllowedOpen`；配对在开放模式下仍可用。`remote.Sessions` 记录在线会话是否经过验证，切到严格时 `DropUnverified` 以 `CloseUnverified`(7) 立刻断开未验证的会话，已验证的保留；放行时也在同一把锁里复查严格状态，不会有会话漏过切换瞬间。新建的 `Policy` 默认严格（失败即关闭）。界面部分未在 Windows 上跑过（ACCEPTANCE H19） | `internal/trust/policy.go`、`internal/remote/sessions.go`、`cmd/go-nx/{host,main}_windows.go`、`internal/ui/home.go` |
| 认证默认值 | **默认开启**（2026-10-28 改；此前默认关闭） | `settings.json` 里从没设置过 `require_auth` 的主机现在要求配对或密码；设置过 `"0"`/`"1"` 的保持原值（`trust.Store.Bool`）。关闭时（界面会警告）任何能连到端口的人都被允许。**这是行为变更**：升级后未配对的控制端会被拒绝，需要先配对 | `cmd/go-nx/host_windows.go`（`requireAuthDefault`）、`internal/trust` |
| 证书/指纹 | 已实现 | 自签证书；客户端 TOFU 固定主机指纹，变化则 `ErrHostChanged` 硬失败；服务端要求任意客户端证书，信任由 `Policy` 决定 | `tls.go:57,72-91` |
| 配对 | 部分（2026-10-28 改） | 主机上点按钮开 2 分钟窗口并显示**一次性 6 位码**；新设备把它填在 Password 里才配对，成功即关窗，全体来源累计错 10 次也关窗。无码模式（`StartPairing`）保留给不经界面的流程。**仍没有** 指纹核对、本机确认弹窗 | `internal/trust/policy.go`（`OpenPairing`、`NewPairingCode`）、`cmd/go-nx/host_windows.go`、`internal/ui/home.go` |
| 密码 | 部分 | 可选。错误次数**按来源 IP** 计（端口不算）：10 分钟内错 5 次锁该来源 1 分钟，连续锁定每次加倍，上限 15 分钟；安静 10 分钟后失败计数清零；输对一次清除该来源的记录；来源表上限 1024 条。配对过的设备不受影响。（2026-10-28 改；此前是全局 1 分钟/5 次、不衰减） | `internal/trust/policy.go` |
| 未认证连接上限 | 已实现（2026-10-28） | `remote.Gate`：等待认证的连接总数 ≤ 16、同一 IP ≤ 4，超出的以 `CloseBusy`(6) 拒绝；Hello 10 s 内未到也会放弃。QUIC 握手本身的并发未限制 | `internal/remote/gate.go`、`host.go`、`cmd/go-nx/host_windows.go` |
| 首次连接 | 已处理（2026-10-28；此前密码在校验指纹前就发出） | 控制端界面连接未固定的主机时，先 `remote.Probe`（只握手取指纹，不发 Hello、不写任何东西），标签页显示该主机自报的 16 位 ID，让用户与主机主页的 `ID:` 比对，按 Enter 才 `PinHost` 并正常连接，Esc / 关闭标签 / 2 分钟无应答都算拒绝；密码或配对码只会发给已固定的身份。**不经界面的调用者**（`remote.Dial` 直接用）仍是首次信任。界面部分未在 Windows 上跑过（ACCEPTANCE H17） | `internal/remote/tls.go`（`Probe`、`PinHost`）、`cmd/go-nx/tabs_windows.go`、`main_windows.go` |
| 撤销已信任设备 | 部分（2026-10-28） | `Store.Delete/Clear/Keys`、`Policy.Revoke/RevokeAll`；主页新增两次点击确认的 "Forget paired devices"，会清空配对并以 `CloseRevoked`(5) 断开当前控制者。**还没有逐设备列表**（只能全部忘记） | `internal/trust`、`internal/ui/home.go`、`cmd/go-nx/host_windows.go` |
| 忘记主机身份（控制端） | 已实现（2026-10-28） | 主机证书变化时仍硬失败（`ErrHostChanged`）；用户确认后在该标签页按 `Ctrl+F` 忘记旧指纹，再 `Enter` 连接并重新固定。此前提示的"手改 hosts.json"在程序运行时无效 | `remote.ForgetHost`、`cmd/go-nx/main_windows.go`、`viewer_windows.go` |
| 凭据存储 | **明文** | 主机密码在 `settings.json`；连接密码在 `connections.json`；`identity.pem` 私钥未加密；Windows 上 0600 无效 | `host_windows.go:88-95`、`internal/profiles/profiles.go:12-17` |
| 配置文件写入 | 已实现（2026-10-28） | `trusted/settings/hosts/connections.json` 与 `identity.pem` 经临时文件 + fsync + rename 原子替换；启动时读不懂的 JSON 被改名为 `<名>.corrupt-<时间>` 并以空状态继续，弹窗告知，而不是 `log.Fatal`。`identity.pem` 本身损坏仍是明确报错（自动重建会改变指纹、使所有配对失效）；读取失败（权限 / I/O）也仍报错 | `internal/fsutil`、`trust.OpenStoreRecovering`、`profiles.LoadRecovering`、`cmd/go-nx/main_windows.go` |
| 监听范围 | 默认全接口；可限定（2026-10-28） | 默认 `":7000"`。新增 `-listen <IP[:端口]>`（只接受 IP 字面量，不接受主机名；`remote.ParseListen`），主页只显示该 IP。**还没有设置界面里的选项**，只能用命令行 | `internal/remote/tls.go`（`ParseListen`）、`cmd/go-nx/host_windows.go`、`main_windows.go` |
| 权限分级 | 不做 | 配对成功即可操作 | `minimal-plan.md` |
| 审计日志 | 部分 | 只有 `go-nx.log` 里的连接/拒绝行 | `internal/logging` |

## 5. 功能面

| 项 | 状态 |
|---|---|
| 音频 | 未实现（不做） |
| 剪贴板 / 文件传输 | 未实现（不做） |
| 多显示器选择 | 未实现（不做） |
| mDNS 发现 / 二维码配对 | 未实现（不做） |
| view/control 分级、国密、视频编码、DATAGRAM/FEC、Windows 服务 | 未实现（不做） |
| Win7 / Win8 | 不支持（`minimal-plan.md`） |
| 自更新 | 已实现（托盘；Windows 实机流程**未验证**），见 `release.md` |

## 6. 代码结构

- 可复用核心：`internal/{proto,tile,trust,profiles}` 只依赖标准库，`internal/remote` 只依赖 quic-go，经 `Source` / `Injector` 接口（`host.go:20-29`）接触平台层；这些包在 linux / android / darwin 上都能编译并通过测试。
- 仅 Windows：`internal/capture`、`internal/inject`、`cmd/go-nx`（Ebiten + systray）。
- 全部在 `internal/` 下，模块名是 `go-nx`，其他模块**不能导入**。
- 无测试的包：`cmd/go-nx`、`internal/capture`、`internal/inject`、`internal/relhub/*`、`tools/*`。

## 7. 设计文档中未被代码支持的说法

| 文档 | 说法 | 实际 |
|---|---|---|
| `minimal-plan.md:13`、`final-converged-plan.md:42,73,204` | xxHash64 比对 tile | `bytes.Equal`（`tile.go:66`） |
| `minimal-plan.md:14`、`final-converged-plan.md:43,79,205` | LZ4 | flate（`tile.go:193`） |
| `final-converged-plan.md:41,71-75,203`、`feasibility…:337` | DDA 优先、GDI 兜底 | 只有 GDI |
| `minimal-plan.md:22`、`final-converged-plan.md:111` | 6 位配对码 | 2 分钟"下一台胜出"窗口 |
| `final-converged-plan.md:84-90` | S0 控制流含版本协商/心跳，S1/S2/S4 分流 | 一条控制流，无协商 |
| `final-converged-plan.md:98,100,102` | UUID opId、ACCEPTED/TIMEOUT、扫描码/`KEYEVENTF_UNICODE` | u64、仅 Executed/Failed、仅 VK |
| `final-converged-plan.md:104,182` | 主机拒绝 Ctrl+Alt+Del / Win+L 并回 `FAILED(SECURE_DESKTOP)` | 无此逻辑，Win 键照常注入 |
| `final-converged-plan.md:113,159` | view/control 分级 | 无分级，新连接顶替旧连接 |
| `final-converged-plan.md:117` | 客户端预渲染光标 | 无 |

`plan-v2-converged-win10.md` 已被 v3（`final-converged-plan.md`）取代。
