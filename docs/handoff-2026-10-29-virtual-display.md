# 交接：画质放大 + 按主控端窗口大小的虚拟显示器（2026-10-29，0.9.0）

计划与设计在 `plan-virtual-display.md`。

## 用户要求与决定

> 能否添加选项允许使用主控端的分辨率进行显示渲染（相当于一个由主控端管理的新的虚拟显示器）或者提供upscale分辨率的算法？

问过用户后定下：①虚拟显示器用 **Parsec VDD**；②go-nx **内置签名安装包，缺驱动时安装**。画质放大两者之外都做。

## 做了什么

| 部分 | 内容 | 文件 |
|---|---|---|
| 画质放大 | Kage 着色器 `gfx.Scaler`：放大用 Catmull-Rom 双三次并限制在最近 4 个像素的范围内（边缘锐利、无光晕）；缩小按覆盖面积取平均（最多 8×8，文字不闪）；1:1 原样复制。设置 → 会话 → "画面缩放"：清晰（默认）/ 平滑 / 像素。着色器编译失败时退回双线性并记日志 | `internal/ui/gfx/scale.go`，`internal/app/{session,live,pages_settings}.go` |
| 协议 | 能力位 4 `CapVirtualDisplay`；查看器 → 主机 `ViewSize`（0x05，w/h，0×0 = 回到主机自己的屏幕）；主机 → 查看器 `DisplayState`（0x83，是否在虚拟屏上、实际分辨率、失败原因）。`Serve` 设了 `OnViewSize` 才声明这一位；回执与状态共用一把写锁。NightDesk 已发布的客户端会跳过不认识的类型 | `internal/proto/v1.go`，`internal/remote/{host,client,codec}.go`，`docs/protocol.md` |
| 管理器 | `vdisplay.Manager`：存布局到磁盘 → 建虚拟屏 → 选模式（放得进窗口的最大一个，同尺寸优先 60 Hz）→ 截屏切到虚拟屏 → 只保留虚拟屏。回来：恢复布局 → 截屏切回 → 删虚拟屏 → 删磁盘上的布局。每一步失败都撤销前面的；布局恢复不了时保留虚拟屏和磁盘上的布局，下次启动 `Recover`。请求按"最新的为准"合并；虚拟屏属于发起的会话，被顶替的旧会话结束时不会删掉新会话的虚拟屏 | `internal/vdisplay/vdisplay.go` |
| Windows 实现 | Parsec 驱动的设备接口与 IOCTL（加 / 删 / 保活 100 ms / 版本，缓冲区放在堆上），找到新建的 `\\.\DISPLAYn`，列模式、先测试再设模式（不写注册表），`QueryDisplayConfig` / `SetDisplayConfig` 读写布局、只保留一块。拒绝：没装驱动、远程桌面会话里、ParsecVDisplay 在运行、没有自己的物理显示器 | `internal/vdisplay/system_windows.go`、`topology.go` |
| 驱动安装 | 嵌入 `parsec-vdd-0.45.0.0.exe`（517256 字节，SHA-256 `e2333244…6d5362`，与 NightDesk 携带的同一个），运行前核对大小、哈希和 Authenticode 签名，解到 `%LOCALAPPDATA%\go-nx\driver\`，以管理员身份 `/S` 静默安装并等它结束 | `internal/vdisplay/install*.go` |
| 主机接线 | 设置 `virtual_display`（默认关）；允许时会话的 `OnViewSize` 交给管理器；会话结束 `Release(该会话)`；退出（托盘退出、更新重启）先 `Release(nil)` 再关驱动；启动时先 `Recover` 再打开截屏；驱动状态每 10 s 刷新 | `cmd/go-nx/host_windows.go`、`tray_windows.go`、`main_windows.go` |
| 截屏切换 | `capture.Screen.Use(设备名)`，下次 `Refresh` 报告几何变化，会话随之改画面尺寸、注入器重新对准 | `internal/capture/capture_windows.go` |
| 主机界面 | 设置 → 共享 → "虚拟显示器"：允许开关、驱动状态 / 安装按钮（说明 UAC 要在主机前确认）、正在使用的虚拟屏 | `internal/app/pages_settings.go`（`virtualCard`） |
| 主控端界面 | 工具条"匹配分辨率"（主机声明了能力才出现）；打开后窗口大小稳定 0.5 s 才发送，关闭立刻发 0×0；主机切到虚拟屏时画面改为 1:1（虚拟屏就是按窗口选的），回来恢复原模式；失败弹提示并把按钮关掉。设置 → 会话里可设成连接时默认打开 | `internal/app/{live,toolbar,session}.go` |

## 验证

- 新测试：着色器（GPU，Xvfb 与 Windows 运行器）`TestSharpEnlargingKeepsEdgesWithoutHalos`（边缘比双线性陡、但不是最近邻的硬台阶、无过冲、平坦处不变）、`TestSharpShrinkingAverages`、`TestSharpScalingKeepsToTheClipAndDims`；协议 `TestV1VirtualDisplayMessagesRoundTrip`；真实 QUIC 上 `TestViewSizeReachesTheHostAndItsAnswerComesBack`；管理器 `TestTheStepsHappenInAnOrderThatNeverLosesTheScreen`、`TestAFailureOnTheWayUndoesWhatWasDone`、`TestALayoutThatCannotBePutBackIsKept`、`TestNotReadyChangesNothing`、`TestReleaseWaitsAndResizesCollapse`、`TestAnEndingSessionLeavesItsSuccessorsDisplayAlone`、`TestPickTheLargestModeThatFitsTheWindow`、`TestOnlyWellFormedLayoutsGoToWindows`、`TestTheCarriedInstallerIsParsecs`；Windows 运行器上 `TestWindowsStructuresHaveTheirSizes`（DEVMODEW 220、DISPLAY_DEVICEW 840、SHELLEXECUTEINFOW 112）与 `TestTheDisplayLayoutCanBeReadAndPutBack`（列模式、读布局、在 CI 上重新应用当前布局；CI 里这一步改成 `-v` 输出）；界面 `TestThePictureScalingCanBeChosen`、`TestMatchResolutionAsksTheHostForAVirtualDisplay`、`TestMatchResolutionFromTheSettingsAndOnlyForHostsThatCan`、`TestTheVirtualDisplaySettingsOnTheHost`。
- 预览：`settings-sharing`、`settings-sharing-virtual`，工具条带"匹配分辨率"按钮，布局测试覆盖四种语言 × 四种尺寸。
- `gofmt`、`go vet`（Linux 与 Windows）、Windows 交叉编译、`xvfb-run -a go test -race ./...` 全绿（第一次全量跑时 hostd / probe / text 各有一个时间敏感的测试在高负载下失败，单独各跑 3 次通过，第二次全量全绿）。
- **没有验证**：真机上的 Parsec 驱动（加 / 删虚拟屏、设模式、只保留虚拟屏、恢复）、安装流程（UAC）、截屏切换、崩溃后的恢复 → `ACCEPTANCE.md` H38。GitHub 的运行器上没有这个驱动。

## 风险与注意

- **只保留虚拟屏期间，主机自己的显示器是黑的**。go-nx 崩溃时驱动收不到保活会删掉虚拟屏，Windows 通常会重新点亮物理屏；下次启动 go-nx 也会按磁盘上的布局恢复。恢复失败时虚拟屏保留、提示写明。
- 驱动安装的 UAC 确认框在安全桌面上，**远程看不到也点不了**。要在主机前确认，或用能处理 UAC 的工具（如 RustDesk 服务模式）。
- minipc 上还有 NightDesk。NightDesk 自己也管理 Parsec VDD；两者同时进入"只保留虚拟屏"会互相干扰。go-nx 只在 ParsecVDisplay 运行时拒绝，不检测 NightDesk。
- Parsec VDD 只提供一组固定分辨率（不改注册表加自定义分辨率），所以选"放得进窗口的最大一个"，剩下的边用 1:1 居中显示。

## 给用户的下一步

H38：在 minipc 上 设置 → 共享 打开"允许主控端按它的窗口大小使用虚拟显示器"，没有驱动就点"安装驱动"并在 minipc 前确认；控制端连上后点工具条的"匹配分辨率"。
