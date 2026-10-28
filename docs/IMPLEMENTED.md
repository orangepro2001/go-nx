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
| 鼠标指针 | 已实现，按需（2026-10-28；此前未实现） | `BitBlt` 不含指针，所以 `Screen.SetCursorVisible(true)` 时用 `GetCursorInfo` + `GetIconInfo` + `DrawIconEx` 把它画进抓到的画面（热点和显示器原点已计入，`internal/cursor` 有几何测试）。**默认关**：只对在 v1 Hello 里带 `CapCursor` 的查看器打开（`DialOptions.WantCursor`，给没有自己指针的手机端用），因为自带指针的桌面查看器会看到两个。主机在 Welcome 里声明 `CapCursor`。测试里用注入的固定箭头（`pointerSource`）验证绘制位置与热点——CI runner 没有鼠标，系统不显示真实指针；真实指针的查询由 `TestTheSystemPointerQuery` 在有鼠标的机器上验证（H22） | `internal/capture`、`internal/remote/host.go`、`internal/cursor` |
| 采集失败恢复 | 已实现（2026-10-28；此前 `Grab` 出错就结束会话） | 能刷新的源（`Refresher`）抓屏失败时会话保留，主机每个 tick 重试 `Refresh` 并上报一次 `capture paused`；恢复后上报 `capture resumed`，查看器期间保留最后一帧；超过 `GrabRetryFor`（默认 60 s）仍不恢复才结束会话。不能刷新的源仍是失败即结束。锁屏 / UAC 的真实行为**未在实机上验证**（ACCEPTANCE H9） | `internal/remote/host.go`、`internal/capture` |
| 帧率 | 已实现（2026-10-28；此前固定 10） | **动态帧率**：画面在变时 `-fps`（默认 10）；控制端操作时升到 `-fps-active`（默认 30，最近一次输入后 2 s 内，`remote.Boost`）；画面没有变化且控制端 3 s 没有输入后降到 `-fps-idle`（默认 2，`Options.IdleFPS`）。输入通过 `Serve` 内部的唤醒通道立刻结束静止，不等慢速 tick；静止时发现画面变化后立刻升回。仍是 Ack 驱动的停等流控 + 过期帧取消，不做质量自适应。一个一直闪烁的光标会让画面不断"在变"（每次只有一两个 tile），此时不降帧，和以前一样。真实 GDI 抓屏下省多少 CPU **未在实机上测**（H30） | `internal/remote/{host,boost,rates}.go`、`cmd/go-nx/{main,host}_windows.go`、`internal/hostd` |

## 2. tile 与编码

| 项 | 状态 | 实际情况 | 位置 |
|---|---|---|---|
| tile 大小 | 已实现 | 64×64 | `internal/tile/tile.go:13` |
| 像素格式 | 已实现 | 线上与内存都是 BGRA、自上而下；第 4 字节**无定义**（GDI 不保证是 alpha；在 GitHub windows-latest 上实测恰好是 255，不能当契约），查看器渲染时必须丢弃/置 255 | `tile.go:14,24`、`internal/view`（`BGRAToRGBA`）、`internal/app/session.go` |
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
| 未确认提示 | 已实现 | 3 s 没收到回执只提示"不确定"，不自动重试 | `internal/app/session.go`（`receiptWarnAt`、`problems`；提示在窗口标题里） |
| 单控制者 | 已实现 | 同时只有一个控制者，新连接顶替旧连接，旧会话持有的按键/鼠标键被释放 | `internal/remote/input.go`、`cmd/go-nx/host_windows.go` |
| 输入的收尾 | 已实现（2026-10-28） | 查看器断开 / 被顶替时，主机按固定顺序释放它按住的东西：先鼠标键、再按键，各自"后按的先放"（Ctrl+Shift+点击会被释放成：点击、Shift、Ctrl）；清理性的鼠标抬起带 `NoMove`，**不再把指针拖回当初按下的位置**；被桌面拒绝的释放保留（最多 64 个），下次使用时重试；会话静默超过 20 s（`HeldInputIdle`）仍持有按键 / 按钮就自动释放，不必等 45 s 的 QUIC 空闲超时；查看器按住按钮而指针不动时每 5 s 发一个保活移动。**Win+L 默认被主机拒绝**（回 Failed 回执；设置 `allow_win_l` = `1` 放行），其他 Win 组合不受影响 | `internal/remote/input.go`、`host.go`、`cmd/go-nx/host_windows.go` |
| 查看器的输入 | 已改进（2026-10-28） | 同一帧里的多个按键事件按键盘顺序发送（普通键的抬起、修饰键按下、普通键按下、修饰键抬起），不再随 map 遍历顺序；释放同理，修饰键最后；按住按钮把指针拖出窗口时继续把位置夹到画面边缘并发送，所以松开不会跳；鼠标键按固定顺序；退出 / 更新重启时先关闭所有会话（任何 goroutine 调用都安全），主机立即释放；键表补上 F13–F24、Print Screen、ISO 键盘的额外键 | `internal/view`、`internal/app/{session,keys}.go`、`cmd/go-nx/tray_windows.go` |
| 按指纹固定的连接 | 已实现（2026-10-28） | `DialOptions.ExpectFingerprint`：只接受证书指纹恰好是它的主机（二维码 / 配对码携带指纹的场景）；握手阶段就拒绝别的证书（`ErrHostChanged`），**密码在握手之后才发，所以不会送给错的主机**；不读不写主机库（可为 nil）；写法宽松，格式不对在拨号前就报错 | `internal/remote/tls.go`（`pinnedClientTLS`、`normalizeFingerprint`）、`client.go` |
| 随输入调整帧率与回调 | 已实现机制（2026-10-28）；**主程序尚未接入** | `Options.FPSFn`（每个 tick 询问速率，≤0 保持、>`MaxFPS`=120 夹住）、`OnInput`（每条输入 / 文本消息，含重试，不含确认）、`OnFrame(seq, bytes)`；`Boost{Base, Active, Hold}` 把两者接起来：最近一次输入后 `Hold`（默认 2 s）内用 `Active`，否则 `Base`。是否在主机默认启用等阶段 6 的 CPU / 带宽数据出来再定 | `internal/remote/host.go`、`boost.go` |
| 弱网仿真与验收 | 已实现（2026-10-28） | `internal/netem`：纯 Go 的用户态 UDP 代理，每个方向可设固定延迟 / 抖动、独立丢包（固定种子）、带宽限制（FIFO，队列满丢最新），运行中可改（模拟断网）；不需要 root。`internal/remote/weaknet_test.go` 用它造"1 Mbit/s、往返 80 ms、1% 丢包"的链路：连得上、画面追得上、回执 p95 < 2 s、同一操作编号只执行一次；另有断网 4 s 后恢复。数字见 `docs/perf.md` | `internal/netem`、`internal/remote/weaknet_test.go` |
| U0 技术验证 | 已实现（2026-10-28；**不是界面，是对界面方案假设的核对**） | `tools/u0`：字体（存在、大小、两种加载方式的时间与堆）、离屏渲染（SDF 着色器对公式、三语文字、读回与 PNG 截图）、空闲开销（四种画法 + `ScheduleFrame`）、tile 与整幅画面上传、Windows 上向 Ebiten 窗口注入按键。结果和对计划的影响见 `ui-baseline.md`；CI 在 Linux（Xvfb）和 Windows runner 上跑并保留报告 | `tools/u0` |
| 度量工具 | 已实现（2026-10-28） | `tools/perf`：在合成画面上跑主机每帧的 `Differ` / `Encode` 与查看器的 `Decode`，报告首帧、稳态字节、各阶段耗时、带宽、CPU 占比；结果和局限见 `docs/perf.md` | `tools/perf` |
| 可复现构建与产物清单 | 已实现（2026-10-28） | `scripts/artifacts.sh` 构建全部程序并写 `SHA256SUMS`、`artifacts.json`（版本、提交、是否有未提交改动、Go 版本、参数、哈希）；`--verify` 用两个空构建缓存各构建一次并比较，在同一台机器上字节相同。`build.sh` 复用它。控制台工具（`nx-host`、`nx-probe`）不再被误构建成无控制台的窗口程序（已在 PE 头里核对子系统）。`docs/compat-matrix.md` 记录版本 ↔ 协议 ↔ 契约 | `scripts/artifacts.sh`、`build.sh` |
| 无界面主机 `nx-host` | 已实现（2026-10-28） | `internal/hostd`（可测的主机：身份、已配对设备、配对窗口、严格 / 开放模式、`Sessions`/`Gate`/`InputController`、随输入升帧）+ `cmd/nx-host`（参数、信号、`-test-mode` 保护、控制端消失即退出）。控制接口是 stdin/stdout 上的 JSON Lines：命令带可选 `id` 并在回答里回显、`schema` 版本、一行至多 64 KiB（超长行被跳过并报错）、事件队列不被慢控制端拖住，控制端 30 s 不读事件就退出码 3。契约见 `docs/nx-host.md`。真实抓屏 / 输入只在 Windows（`backend_windows.go` 复用 `capture`/`inject`）；其他系统只有 `-test-mode` | `internal/hostd`、`cmd/nx-host`、`scripts/e2e-host-probe.py` |
| 手机端查看器核心 `mobile` | 已实现（2026-10-28）；AAR 实构建未验证 | gomobile 友好的 `Session`：固定指纹、配对、tile 回调（BGRA）、输入；指针动作用最后一次 `Move` 的位置（之前没有位置则报错，不用角落）、位置夹到画面内、`ReleaseAll` 顺序确定、`Close()` 立即返回并保证释放先到主机、回调 panic 被吞掉、按主机声明的能力决定能否发文本。契约见 `docs/mobile.md`；Java API 固定在 `mobile/api.golden`（`scripts/build-aar.sh --check-api`） | `mobile/`、`scripts/build-aar.sh` |
| 关闭时输入先到主机 | 已实现（2026-10-28） | `Client.Close()` 先把已接受的输入写完（≤300 ms），再结束控制流（FIN），等主机读完并挂断（≤1 s）才关连接——直接 `CloseWithError` 会丢掉已写入但未送达的数据。关闭过程中到来的确认写入不再把连接关掉（会连带丢失尾部输入）。`Flush(timeout)` 可单独使用。主机把"查看器正常结束控制流"当作会话正常结束（`Serve` 返回 nil，而不是 `EOF`） | `internal/remote/client.go`、`outbox.go`、`host.go` |
| 合成画面源与只记录的注入器 | 已实现（2026-10-28） | `internal/synth`：确定性的 BGRA 画面，五种场景（`blocks`、`static`、`docscroll`、`terminal`、`video`），每帧只由帧序号决定；四角有颜色标记和琥珀色边框，`VerifyMarkers` 检查颜色顺序 / 方向。`internal/logsink`：不碰桌面的注入器，记录并校验输入（可选屏幕范围检查）。两者让真实的主机代码能在没有桌面的机器上端到端跑 | `internal/synth`、`internal/logsink` |
| 验收探针 | 已实现（2026-10-28） | `internal/probe` + `cmd/nx-probe`：按指纹固定连接、用配对码当密码、走真实解码路径，收若干帧，可选发送每种输入并核对回执；`-v0` 以原始协议连接；`-markers` 校验合成主机的画面；`-json` 输出一行带 `schema` 的结果；退出码 0 / 1 / 2。**只证明协议路径，不证明真实 GDI / `SendInput`** | `internal/probe`、`cmd/nx-probe` |
| 查看器发送队列 | 已实现（2026-10-28；此前在界面线程里同步写） | `Client.SendInput` / `SendText` 只入队（上限 1024 条，满了返回 `ErrBacklog`，绝不阻塞调用方）；连续的纯移动会被合并为最后一个，按下 / 抬起 / 滚轮 / 按键保序不丢；专门的 goroutine 写控制流，每次写有 10 s 截止时间，超时即关闭连接（`Run` 随之返回），不会永远卡着。确认回执也走同一条写路径 | `internal/remote/outbox.go`、`client.go` |
| 断线后重连 | 部分 | 首次拨号最多尝试 4 次（握手时限 2/3/4/6 s）；断线后需手动重连 | `internal/app/tabs.go`（`dialWaits`；失败卡片上有"重试"） |

## 4. 安全与配对

| 项 | 状态 | 实际情况 | 位置 |
|---|---|---|---|
| 严格 / 开放模式（"本机"页的分段控件） | 已实现（2026-10-28；0.3.0 是主页按钮，0.7.0 起是"本机"页的分段控件 + 开放模式的二次确认对话框 + 所有页面顶部的红色横幅） | 在 **严格**（默认，需配对或密码）和 **开放**（不验证）之间切换；严格 → 开放要确认，开放 → 严格立即生效。判决新增 `AllowedOpen`（因开放而放进来、未验证）：`Policy.Check` 对已配对设备在两种模式下都返回 `Allowed`（所以随便切换不受影响），开放模式下的陌生人返回 `AllowedOpen`；配对在开放模式下仍可用。`remote.Sessions` 记录在线会话是否经过验证，切到严格时 `DropUnverified` 以 `CloseUnverified`(7) 立刻断开未验证的会话，已验证的保留；放行时也在同一把锁里复查严格状态，不会有会话漏过切换瞬间。新建的 `Policy` 默认严格（失败即关闭）。界面部分未在 Windows 上跑过（ACCEPTANCE H19） | `internal/trust/policy.go`、`internal/remote/sessions.go`、`cmd/go-nx/host_windows.go`、`internal/app/pages_pc.go` |
| 认证默认值 | **默认开启**（2026-10-28 改；此前默认关闭） | `settings.json` 里从没设置过 `require_auth` 的主机现在要求配对或密码；设置过 `"0"`/`"1"` 的保持原值（`trust.Store.Bool`）。关闭时（界面会警告）任何能连到端口的人都被允许。**这是行为变更**：升级后未配对的控制端会被拒绝，需要先配对 | `cmd/go-nx/host_windows.go`（`requireAuthDefault`）、`internal/trust` |
| 证书/指纹 | 已实现 | 自签证书；客户端 TOFU 固定主机指纹，变化则 `ErrHostChanged` 硬失败；服务端要求任意客户端证书，信任由 `Policy` 决定 | `tls.go:57,72-91` |
| 配对 | 部分（2026-10-28 改） | 主机上点按钮开 2 分钟窗口并显示**一次性 6 位码**；新设备把它填在 Password 里才配对，成功即关窗，全体来源累计错 10 次也关窗。无码模式（`StartPairing`）保留给不经界面的流程。**仍没有** 指纹核对、本机确认弹窗 | `internal/trust/policy.go`（`OpenPairing`、`NewPairingCode`）、`cmd/go-nx/host_windows.go`、`internal/app/pages_pc.go` |
| 密码 | 部分 | 可选。错误次数**按来源 IP** 计（端口不算）：10 分钟内错 5 次锁该来源 1 分钟，连续锁定每次加倍，上限 15 分钟；安静 10 分钟后失败计数清零；输对一次清除该来源的记录；来源表上限 1024 条。配对过的设备不受影响。（2026-10-28 改；此前是全局 1 分钟/5 次、不衰减） | `internal/trust/policy.go` |
| 未认证连接上限 | 已实现（2026-10-28） | `remote.Gate`：等待认证的连接总数 ≤ 16、同一 IP ≤ 4，超出的以 `CloseBusy`(6) 拒绝；Hello 10 s 内未到也会放弃。QUIC 握手本身的并发未限制 | `internal/remote/gate.go`、`host.go`、`cmd/go-nx/host_windows.go` |
| 首次连接 | 已处理（2026-10-28；此前密码在校验指纹前就发出） | 控制端界面连接未固定的主机时，先 `remote.Probe`（只握手取指纹，不发 Hello、不写任何东西），标签页显示该主机自报的 16 位 ID，让用户与主机主页的 `ID:` 比对，按 Enter 才 `PinHost` 并正常连接，Esc / 关闭标签 / 2 分钟无应答都算拒绝；密码或配对码只会发给已固定的身份。**不经界面的调用者**（`remote.Dial` 直接用）仍是首次信任。界面部分未在 Windows 上跑过（ACCEPTANCE H17） | `internal/remote/tls.go`（`Probe`、`PinHost`）、`internal/app/{tabs,session_ui}.go` |
| 撤销已信任设备 | 已实现（0.7.0；此前只能全部忘记） | `Store.Delete/Clear/Keys`、`Policy.Revoke/RevokeAll`；"本机"页列出已配对的设备（地址、配对时间），可逐个"忘记"或"全部忘记"（都先确认），并以 `CloseRevoked`(5) 断开正在控制的那台 | `internal/trust`、`internal/app/pages_pc.go`、`cmd/go-nx/host_windows.go` |
| 忘记主机身份（控制端） | 已实现（2026-10-28） | 主机证书变化时仍硬失败（`ErrHostChanged`）；用户确认后在该标签页点"忘记已保存的身份"（旧界面是 `Ctrl+F`），再点"重试"连接并重新固定。此前提示的"手改 hosts.json"在程序运行时无效 | `remote.ForgetHost`、`internal/app/session_ui.go` |
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
| 自更新 | 已实现并加固（2026-10-28）。托盘与主页按钮；`Install` 前重验 `.new`、单次安装（`Installed` 状态）、第二次改名重试、`.old` 在新版本运行满首次延迟后才清理、被打断时从 `.old` 启动自愈（`update.Recover`）。真实 Windows 上"改名正在运行的 exe"由 CI 验证；**用户 PC 上的完整流程仍未验证**（H2、H3、H26），见 `release.md` | `internal/update`、`cmd/go-nx/update_windows.go` |

## 5b. 界面（0.7.0 起的新界面；旧的 `internal/ui` 已删除）

| 项 | 状态 | 实际情况 | 位置 |
|---|---|---|---|
| 令牌 | 已实现 | 蓝色主题的深 / 浅两套配色、间距、圆角、字号；测试逐对检查文字与背景对比度 ≥ 4.5:1，并核对设计文档里的数值 | `internal/ui/theme` |
| 三语字符串 | 已实现 | 简体中文 / English / 한국어，167+ 条，一张表三语并排；具名占位符、一句一条、英文复数 One/Other、伪语言 `xx`；测试拒绝缺语言、占位符不一致、printf 动词、猜测韩文助词。**韩文译文是我写的，未经母语者校对** | `internal/ui/i18n` |
| 文字 | 已实现 | UAX #14 断行（无空格中文能换行；韩文按词断，不在词中间断）、按字素簇截断；字体引擎：系统字体（Segoe UI / 微软雅黑 UI / Malgun Gothic，开发机上是 Noto）用 `*os.File` 在后台打开，一次只常驻界面语言的 CJK 字体，别的脚本的字按需加载，加载前用内嵌 Go Regular 顶着 | `internal/ui/text` |
| 工具箱 | 已实现 | 测量 / 布局 / 绘制 / 输入的节点树（Row、Col、Stack、Grid、Box、Label、Button、Chip、Toggle、Segmented、TextField（含输入法预编辑）、Scroll、Dialog、Toasts、Code），焦点与 Tab 顺序、悬停 / 按下、动画（支持"减少动画"）；节点树变了才重建；记录型画布让布局和行为不用 GPU 就能测 | `internal/ui/kit` |
| 画布 | 已实现 | 一个符号距离场着色器画填充 / 描边 / 阴影圆角矩形；用子图实现裁剪；31 个矢量图标；GPU 测试读回像素。**修过一个真问题**：着色器曾把"贴图位置"当"图内位置"（见 `ui-baseline.md` ⑥ 的更正） | `internal/ui/gfx` |
| 页面 | 已实现 | 设备（快速连接、已保存设备卡片、搜索 / 排序 / 置顶 / 编辑 / 删除确认）、本机、设置、日志（只画看得见的行）、会话卡片（连接中、首次连接核对、失败 + 重试 / 忘记身份）、标签条（双击重命名）、通知、侧栏（窄窗口收成图标栏）、托盘菜单随语言 | `internal/app` |
| 按需渲染 | 已实现 | `FPSModeVsyncOffMinimum`；定时器按"下一次需要帧"的时间唤醒（通知过期、光标闪烁、配对倒计时、动画 60 次 / 秒、按住的键）；后台变化（更新进度、日志新行）由 500 ms 的轮询发现，只在有变化时要一帧；界面画进缓存层，树没变只重画远程画面。真实空闲 CPU 与内存**未在实机上测**（H29、H31） | `internal/app/{game,poll}.go` |
| 输入法 | 已实现，**未在真机上验证** | 聚焦到接受输入法的输入框时开一个 `textinput.Composer` 会话：预编辑文字带下划线画在光标处，候选窗跟随光标；提交的文字、Ebiten 的普通字符两条路径只取一条（一帧之内重复的按键认作同一次）；密码框不启用。全部在 `ime.go` 一个文件里（实验性 API）。H29 | `internal/app/ime.go` |
| 还没做（U4 之后） | — | 会话里的悬浮工具条、适应 / 100%、全屏、光标捕获、修饰键角标、统计覆盖层、断线自动重连、한/영 键、发送文本、着色器做 BGRA→RGBA、高对比度、性能回归门槛 | `ui-redesign.md` 第 12 节 |

## 6. 代码结构

- 可复用核心：`internal/{proto,tile,trust,profiles}` 只依赖标准库，`internal/remote` 只依赖 quic-go，经 `Source` / `Injector` 接口（`host.go:20-29`）接触平台层；这些包在 linux / android / darwin 上都能编译并通过测试。
- 仅 Windows：`internal/capture`、`internal/inject`、`cmd/go-nx`（只剩主机适配、托盘、更新器适配、平台适配和 `main`）。界面与会话逻辑在可移植的 `internal/app`（Linux 上也能测试和截图）。
- 全部在 `internal/` 下，模块名是 `go-nx`，其他模块**不能导入**。
- 无测试的包：`cmd/go-nx`（Windows 专用，薄）、`internal/relhub/*`。`internal/{capture,inject}` 只有 Windows 上的测试。

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
