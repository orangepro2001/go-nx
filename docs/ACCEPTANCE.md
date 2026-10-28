# go-nx 验收状态

- 更新：2026-10-28，阶段 0–7 的自动化部分、回移 B0–B5 和 U0 完成之后。最新发布 **0.5.0**（含异步发送队列、更新器加固、多显示器等）；`nx-host` / `mobile` / `tools/*` 不在 `go-nx.exe` 里，只构建不发布
- 原则：只写**实际跑过**的东西。自动化通过 ≠ 真实桌面体验正确；合成测试证明协议与逻辑，不证明真实 GDI / `SendInput` / 物理显示器。
- "待验证"表示没有证据，不是"大概没问题"。

## 1. 自动化（Linux，已跑）

| 检查 | 结果 | 备注 |
|---|---|---|
| `gofmt -l .` | 干净 | |
| `go vet ./...`、`GOOS=windows go vet ./...` | 通过 | |
| `go test -race -cover ./...` | 全部通过 | 覆盖率（Linux 构建；`capture` / `inject` 的 Windows 专用代码不在其中，它们靠 CI 的真实桌面测试）：hostd 91.8%、mobile 95.4%、netem 93.8%、remote 92.0%、update 93.5%、proto 93.6%、probe 91.7%、trust 91.9%、tile 87.4%、cmd/nx-probe 87.2%、cmd/nx-host 84.8%、profiles 84.8%、fsutil 78.8%、logging 68.6%、ui 31.6%、tools/u0 19.1%（窗口部分只在 CI 里跑）。另有 fuzz：`proto` 4 个目标、`tile` 1 个，各跑 15 s 无新崩溃 |
| 核心包交叉编译 | 通过 | `proto/tile/trust/remote` 在 linux、windows、darwin、`android/arm64` 上能编译 |
| `scripts/e2e-update.sh` | 通过（2026-10-28，更新器加固后重跑） | 真实 `relhub` + 临时目录 + 回环端口 |
| `scripts/e2e-host-probe.py`（真实的 `nx-host` 与 `nx-probe` 两个进程） | 通过（Linux）；CI 的 Linux 与 Windows 两个 job 都跑 | 配对、v1 与 v0 连接、画面标记完好、8 项输入全有回执、错误指纹在握手时被拒、错误码被拒并报 `denied`、状态计数、`stop` 后退出码 0、stdout 每行都是合法事件。**Wine 里跑不了**：quic-go 在 Wine 上监听 UDP 会报 `wsaioctl: winapi error #10045`（Wine 的限制，不是 go-nx 的问题），所以 Windows 版靠 `windows-latest` |
| `scripts/build-aar.sh --check-api` | 通过（Linux） | 在临时副本里加 `golang.org/x/mobile`、用 `gobind` 生成 Java 绑定、与 `mobile/api.golden` 逐行比较；故意删一行能检出漂移。仓库的 `go.mod` 不变 |
| `go run ./tools/perf`、弱网验收 | 通过（Linux，i7-8700） | 合成画面的每帧成本、"1 Mbit/s / 80 ms / 1% 丢包"下可操作、断网恢复；数字和局限见 `perf.md`。同一台机器上 `-count=3` 重复通过 |
| `scripts/artifacts.sh --verify` | 通过（Linux 交叉编译，Go 1.27.3） | 两次空缓存构建的哈希相同。换机器 / 换 Go 版本 / Windows 本机构建是否相同未验证 |
| 变异检查 | 本任务新增的每个行为都做过 | 故意改坏实现，对应测试确实失败（updater、FPSFn / OnInput / OnFrame、固定指纹、probe、mobile、hostd）；做的过程中还抓到并修了几个真问题，见交接文档 |
| 反复运行 | `hostd`、`mobile`、`remote` 的关闭相关测试在 `-race -count=10` 下重复多轮通过 | 中途抓到的两个竞态（关闭时确认写入把连接关掉、`EOF` 被当成错误）已修并各有失败在先的测试 |

**仍没有测试**的包：`cmd/go-nx`（窗口程序，只在 Windows 上编译）、`internal/relhub/*`（release-publish 客户端 SDK 的副本，由 `internal/update` 的测试和 `e2e-update.sh` 间接覆盖）、`tools/*`。`internal/capture`、`internal/inject` 的测试只在 Windows 上编译；CI 的 windows job 在真实桌面上跑它们，Linux 上用 Wine 跑过。

## 1b. GitHub `windows-latest` 一次性 Windows（CI，2026-10-28 首次运行）

用来回答"没有可折腾的 Windows 机时，哪些实机行为能自动验证"。CI 里设置 `GO_NX_WINDOWS_E2E=1` 才会运行；这些测试会在桌面上画窗口、移动真实光标，**默认不要在日常 PC 上开启**。

| 结论 | 证据 |
|---|---|
| runner 上有可交互桌面：1024×768、100% 缩放、单显示器 | `TestGrabSolidColours` 日志 `primary screen 1024x768` |
| GDI 抓屏的字节顺序是 **B,G,R,X**：红 = `0,0,255`，绿 = `0,255,0`，蓝 = `255,0,0`，橙 = `0,128,255` | 同上，四种纯色窗口抓到的像素 |
| 第 4 字节在该 runner 上恰好是 255；这**不是**契约，Android 修复里"置 255"仍然必要 | 同上日志；协议层继续按"第 4 字节无定义"处理 |
| `SendInput` 绝对移动：10 个目标像素全部落在精确位置，无 1px 偏差 | `TestAbsoluteMoveLandsOnPixel`（1024×768）。**其他分辨率未测**：本地 wine（1280×800）里出现过 3/10 偏 1px，真 Windows 上是否同样要看更多分辨率 |
| 按下 / 抬起各自带坐标时，抬起后光标位置不变；左键与 F24 的按下 / 抬起状态可被观察到 | `TestButtonUpKeepsPosition`、`TestKeyDownUp` |

**这些结果不覆盖**：真实显卡 / 物理显示器、DPI 缩放、多显示器、鼠标指针像素、光标闪烁（肉眼观察）、锁屏 / UAC。对应 H4 / H5 在 §3 里改为"部分覆盖"。

**U0 在 runner 上又得到的**（`ui-baseline.md`；每次 CI 运行都重跑并保留报告）：runner 上 Ebiten 用 **DirectX（软件渲染）** 跑得起来；`segoeui.ttf`、`msyh.ttc`、`malgun.ttf` 都在 `C:\Windows\Fonts`；`SendInput` 注入到 Ebiten 窗口的按键、Unicode 文本到达（`hello한中`），VK_HANGUL / VK_HANJA 没有键事件；自更新要的"运行中的 exe 可以改名、备份副本是活进程的映像所以删不掉"在真 Windows 上成立（`TestReplaceOfARunningExe`）；`nx-host` 与 `nx-probe` 作为两个进程的完整流程在真 Windows 上通过。

## 2. 自更新（internal/update）

已被单测覆盖（2026-10-28 加固后；每条都做过变异检查——故意改坏实现，对应测试确实失败）：签名被他钥拒绝、下载被篡改丢弃、强制更新、`dev` 版本被拒、内置公钥可解析；**更低版本的 manifest 不当作更新**；**错 app / platform / channel 的合法签名 manifest 被拒（`invalid_argument`）且不下载**；**截断 / 超长下载不可安装**；**`.part` 续传（Range `bytes=8-`）**与**陈旧 `.part`（等长 / 更短 / 更长）下次检查自愈**；**`Install` 重验**（改一个字节 / 截断 / 追加 / 删除 → 拒绝、删除候选、状态 `Failed`、重新下载后可装）；**重复与并发 `Install` 只装一次且不动备份**；`Install` 进行中 `Status` 不阻塞；缺新版本 / 缺 exe / 新版本是目录时**什么都不改**（包括更早的 `.old`）；改名被占用时重试后成功；一直装不上则放回旧版本；连放回也失败时错误信息给出 `.old` 路径与改回的方法；只读目录（非特权 Unix）；`New` 拒绝 `.old` / `.new` 路径；`Recover` 的三种情形；`.old` 在首次延迟之后才清理。
**在 Wine 与 `windows-latest` 上还跑**：`TestReplaceOfARunningExe`（把测试二进制复制成 `go-nx.exe` 并运行，再 `Replace` 它：运行中的 exe 可被改名、备份副本是活进程的映像所以删不掉、进程结束后能删）、`TestARunningBackupCopyTakesTheRealNameBack`（从 `go-nx.exe.old` 启动的进程把自己改回 `go-nx.exe`）。
**仍未覆盖**：真实 `Install` 中途断电（窗口只有两次相邻改名之间；用 `Recover` 与上面两个测试覆盖了它的后果，没覆盖"断电本身"）；杀毒软件占用 `.new` 的真实行为（用注入的改名失败模拟）；用户 PC 上的完整更新流程（H2、H3、H26）。

## 3. 待在真实 Windows 上验证（人工，由用户择机执行）

只有一台日常使用的 Windows PC，下列项目都不要求破坏性操作之外的权限；**标 ⚠ 的会改系统状态或影响本地使用，请自行选合适时机**。结果回填到本表。

| # | 项目 | 怎么验 | 记录什么 | 状态 |
|---|---|---|---|---|
| H1 | 托盘版本项与更新入口 | 装 0.1.0，托盘右键（Windows 11 常在右下角 `^` 溢出区里） | 顶部是否有禁用的 `go-nx v0.1.0`，更新项文案 | 待验证 |
| H2 | 自更新替换并重启 | 装着 0.1.0 的机器：托盘点更新项（线上已有 0.2.0）→ `Restart to update to v0.2.0` | 是否不弹"已在运行"；`.old` 是否在下次启动后消失 | 部分：用户 2026-10-28 报告已从 0.1.0 更新到新版本，未提供细节；"不弹已在运行"和 `.old` 清理没有单独确认 |
| H3 | 只读目录下的更新失败 | 把 exe 放到只读目录 | 托盘文案；`go-nx.log` 的 `update:` 行 | 待验证（非特权 Unix 上有单测：什么都不改；Windows 的只读目录不阻止改名，这一项更多是权限 / 杀毒占用） |
| H4 | GDI 抓屏颜色正确 | 被控端显示纯红/绿/蓝窗口，另一台查看 | 颜色是否一致、有无变暗/透明 | 部分覆盖：CI runner 上抓到的 B,G,R 正确（§1b）；真实显卡与查看器端到端仍待验证 |
| H5 | `SendInput` 点击/拖动/滚轮/键盘 | 控制端远程操作记事本和资源管理器 | 位置是否准确、松开后指针是否跳动 | 部分覆盖：CI runner 上移动 / 点击 / 按键生效（§1b）；拖动、滚轮、真实应用待验证 |
| H6 | 被控端物理光标闪烁（`CAPTUREBLT` 的 A/B） | 在**被控端那台电脑上**，先用默认设置（`go-nx.exe`），再用 `go-nx.exe -layered=false`（等价于设置里 `capture_layered`=`0`）各观察一遍这六种情形：①没有连接 ②有连接但不动 ③控制端移动指针 ④控制端点击后松开 ⑤控制端停止输入 ⑥断开。每种情形盯着被控端自己的指针看 10–20 秒 | 每种情形下被控端指针是否闪烁 / 消失；同时记下显卡与驱动版本、刷新率、缩放比例、指针主题（是否反色 / 动画指针）。若 `-layered=false` 时不再闪烁：再看菜单、提示、下拉框在控制端是否还能看到（`-layered=false` 的代价）——**CI runner 上无论有无 `CAPTUREBLT` 分层窗口都抓得到（§1b），这不能推广到真实显卡 / DWM** | 待验证（假设：`CAPTUREBLT` 导致，没有证据；开关已在，默认值未变；没复现就写"未复现"） |
| H7 ⚠ | 125% / 150% 显示缩放 | 改缩放后重启主机 | 画面尺寸与点击位置是否一致 | 待验证 |
| H8 | 多显示器 | 副屏存在时连接 | 抓的是哪块屏；点击落点 | 待验证（当前只支持主屏） |
| H9 ⚠ | 锁屏 / UAC / `Win+L` | 远端按 `Win+L`；触发 UAC | 会话是否终止；能否恢复 | 待验证 |
| H10 | 退出控制端后被控端的按键状态 | 按住 Shift 时强退控制端 | 被控端 Shift 是否保持按下、多久恢复 | 待验证（推测最多 45 s） |
| H11 | 长时间稳定性 | 连接 ≥ 2 h | 内存、CPU、有无断线 | 待验证 |
| H12 | 真实网络（Wi-Fi、不同子网/tailnet） | 跨网段连接 | 首帧时间、帧率、码率 | 待验证 |
| H13 | 主页 "Forget paired devices" | 配对一台设备后点两次；确认第二次点击前文案是 `Sure? Forget N device(s)`；点别处应取消 | 配对数归零；正在控制的设备是否被断开并看到"removed from the host's paired devices" | 待验证（仅编译 / vet，UI 未在 Windows 上跑过） |
| H14 | 主机证书变化后的 `Ctrl+F` | 重装主机或换机同 IP，用控制端连接 | 先看到 `host identity changed`；`Ctrl+F` 后提示可重连；`Enter` 后连上并重新固定 | 待验证（同上） |
| H15 | 启动时 JSON 损坏提示 | 退出 go-nx，把 `%AppData%\go-nx\connections.json` 改成无效内容再启动 | 弹窗列出被改名的文件；原文件在 `*.corrupt-*`；能正常启动 | 待验证（同上；逻辑已有单元测试） |
| H16 | 配对码流程 | 主机点 "Pair new device"，看到 `Pairing open … code 123 456`；另一台在 Password 填该码连接；再断开重连不填密码 | 码是否清晰显示；错码被拒并提示；成功后无需密码；窗口到期 / 成功后码失效 | 待验证（逻辑有端到端单元测试，界面未在 Windows 上跑过） |
| H17 | 首次连接的指纹核对 | 用一台从没连过的主机，在控制端新建连接 | 标签页是否显示 `First connection to … ID is …`，且与主机主页 `ID:` 相同；Enter 后才连接并只在这时发送密码 / 配对码；Esc 后标签页显示未发送任何内容；之后再连不再提示 | 待验证（核心有单元测试，界面未在 Windows 上跑过） |
| H18 | 主页的版本号与 Check for updates 按钮 | 装 0.2.0 后打开窗口；再发布一个更高版本 | 右上角是否有按钮、版本号是否在标题旁；点击后文字是否依次变化；Ready 时是否高亮且需点两次；是否能更新并重启 | 待验证（布局有单元测试，绘制和点击未在 Windows 上跑过） |
| H19 | 严格 / 开放模式按钮 | 主页点模式按钮：严格 → 开放（第一次变红并提示再点一次，第二次生效）；用一台未配对的 PC 连上；再点回严格 | 未配对的 PC 是否被立刻断开并看到"host switched to strict mode"提示；已配对 / 已验证的 PC 是否不受影响；按钮文字在三种状态下是否完整显示 | 待验证（核心有真实 QUIC 单元测试，界面未在 Windows 上跑过） |
| H20 | 多显示器 | 副屏在主屏左侧（坐标为负）、右侧、上方各试一次：`-display "\\.\DISPLAY2"` 启动主机，用另一台电脑连接并点击 | 画面是不是那块屏；点击落点是否精确；主屏仍正常 | 待验证（CI runner 只有一块屏；虚拟桌面换算有逐像素单元测试） |
| H21 | 会话中改分辨率 / 缩放 | 连接后在被控端改显示分辨率，再改缩放比例 | 查看器画面是否在几秒内换成新尺寸；点击是否仍然精确；会话是否保持 | 待验证（逻辑有单元测试；真实改分辨率未试） |
| H22 | 帧内光标 | 手机端（或带 `WantCursor` 的查看器）连接，在被控端移动指针 | 画面里能否看到指针，位置和形状是否对；自带指针的桌面查看器不应看到第二个 | 待验证（CI 上有像素级测试，是否通过见 `ACCEPTANCE.md` 1b） |
| H23 | 拖出窗口再松开 | 在远程桌面里按住左键拖选，把指针拖出查看器窗口，在窗口外松开 | 被控端指针是否停在画面边缘（不跳回起点）；选区是否到边缘 | 待验证（坐标夹取有单元测试；真实鼠标未试） |
| H24 | 退出时被控端的按键状态 | 按住 Shift 时从托盘退出控制端 | 被控端 Shift 是否立刻释放（之前最多等 45 s）；强杀进程时约 20 s 内释放 | 待验证（主机看门狗有单元测试） |
| H25 | Win+L | 控制端按 Win+L | 被控端不应锁屏；查看器状态行应出现一次失败的操作；其他 Win 组合（Win+E）正常 | 待验证（策略有单元测试） |
| H26 | 被打断的更新可恢复 | 关闭 go-nx；在资源管理器里把 `go-nx.exe` 改名为 `go-nx.exe.old`（模拟两次改名之间被打断）；双击 `go-nx.exe.old` | 它应启动，并在资源管理器里变回 `go-nx.exe`；`go-nx.log` 有 `an earlier update was interrupted; restored ...`；此后更新器正常工作 | 待验证（`Recover` 有单测，真实 Windows 上"进程改自己的名字"由 CI 的 `TestARunningBackupCopyTakesTheRealNameBack` 覆盖） |
| H27 | AAR 构建与 Android 联调 | 装好 Android SDK/NDK 与 `gomobile` 后 `scripts/build-aar.sh`；集成到手机 App，扫码 / 输入指纹和配对码连接一台主机 | 构建是否成功；画面颜色（应用交换红蓝并置 alpha=255）；触摸移动 / 点击 / 滚动 / 文本；松开按钮时主机指针是否停在原地 | 待验证（本机没有 Android 工具链；Java API 已由 `--check-api` 固定） |
| H28 ⚠ | `nx-host` 在真实 Windows 桌面上 | `nx-host.exe -listen 127.0.0.1:7000 -pair`，另一个窗口 `nx-probe -addr 127.0.0.1:7000 -fingerprint <ready.identity> -code <pairing.code> -input`。**`-input` 会真的动鼠标、在屏幕中央点击、滚轮、按 A 并输入文字到当前获得焦点的窗口**——先打开一个空记事本 | 画面能收到；`inputs: 8 sent, 8 receipts`；记事本里出现 `go-nx probe 你好`；`nx-host` 退出码 0 | 待验证（`-test-mode` 的完整流程已在 CI 验证；真实 GDI / `SendInput` 在 `nx-host` 里没跑过） |
| H29 | U0 在你的 PC 上（真实 GPU、输入法） | ① 在你的 PC 上运行 `go run ./tools/u0 all -seconds 60 -png u0.png -out u0.json`（会开一个小窗口，约 3 分钟；`input` 一项会往窗口里注入按键，所以运行时别动键盘）；② 若装有中文 / 韩文输入法：写一个临时用例或直接用 Ebiten 的 `examples/textinput`，用输入法打几个词，看预编辑文本、候选窗位置、提交；③ 在一台装有韩文输入法的远端上，用现有注入路径发 VK_HANGUL（0x15）/ VK_HANJA（0x19），看远端输入法是否切换 | 真实 GPU 上的空闲 CPU（60 s）、首帧时间、空窗口的私有内存、tile 上传、着色器是否通过；输入法开启时 `exp/textinput` 的行为；会话里本地输入法关闭后按键是否仍原样转发；VK 是否切换远端输入法 | 待验证（`windows-latest` 软件渲染上已全部跑过，见 `ui-baseline.md`；这一项补真实硬件和输入法） |
| H30 | 动态帧率 | 在被控端用任务管理器（详细信息 → `go-nx.exe` 的 CPU）看三种情形：①控制端连着，被控端桌面完全不动（无闪烁光标）至少 10 s；②控制端在远程桌面里持续移动鼠标 / 打字；③停止操作后再等 5 s。`go-nx.log` 启动行 `frame rate watching 10/s, working 30/s, idle 2/s` 确认策略。可用 `-fps-active 0 -fps-idle 0`（固定 10 fps）作对照 | ①的 CPU 明显低于对照（约五分之一的抓屏 + 比较开销）；②的画面更流畅；③回落；控制端的第一次操作没有明显的"卡一下"；被控端上若有闪烁光标的窗口，①可能降不下来（已知） | 待验证（核心行为有单元测试和变异检查；真实 GDI 抓屏的成本只有 Windows 机器能测） |

## 4. 已知缺陷与风险

见 [`IMPLEMENTED.md`](IMPLEMENTED.md) 的安全与会话表；修复排期见各阶段交接文档。当前最重要的几条：首次连接不核对主机指纹（TOFU，密码在核对前就发出）、凭据与私钥明文、`Grab` 出错即结束会话、退出 / 崩溃时不释放输入、按键发送顺序不确定、没有协议版本协商。已处理的（认证默认开启、一次性配对码、可撤销设备、恶意主机上限、原子写）见 `ROADMAP.md` 阶段 1。
