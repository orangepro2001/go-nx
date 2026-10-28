# 回移台账：NightDesk → go-nx

go-nx 是单一上游。NightDesk 的 `native/` 里的通用修复在这里**重新实现**（参照其接口和线上格式，不拷贝文件），并保持线上兼容。

- **NightDesk 侧只读**：只读取快照，不写入、不提交、不改它们的依赖。用户要求暂不改动 NightDesk（它不是最新版本）。
- 来源快照：`/home/tony/dev/nightdesk-native-ux-20261028`（目录时间 2026-10-28 07:34）。该快照没有 `.git`，可能落后于 NightDesk 的真实最新源码；**真要对 NightDesk 动手之前必须重新对照它的最新源码**。
- 模块路径差异：NightDesk 是 `github.com/orangepro2001/nightdesk/native`，go-nx 在 B0 之后是 `github.com/orangepro2001/go-nx`。
- 状态：☐ 未开始　◐ 进行中　☑ 完成　✕ 不做（附原因）

## 条目

| ID | 内容 | 来源（快照内） | go-nx 位置 | 线上 / API 影响 | 状态 |
|---|---|---|---|---|---|
| B0 | 模块路径改为 `github.com/orangepro2001/go-nx` | — | `go.mod` 与全部 import | 无 | ☑ |
| B1 | `MsgText`(=4)、`KindWheelH`(=7)、`MaxTextLen`；主机 `TextInjector`；Windows `InjectText`（`KEYEVENTF_UNICODE`，64 单元分块）与横向滚轮 | `internal/proto`、`internal/remote/host.go`、`internal/inject` | 同名包；与快照的差异：文本走 `InputController`（被顶替的旧会话不能输入文字）、不拆代理对、非法 UTF-8 替换、统一的 `runOp` | v0 加两种消息；已发布的 NightDesk 客户端依赖它 | ☑ |
| B2 | 多显示器枚举与选择（`Displays`/`Open(selector)`）、per-monitor DPI v2、帧内光标合成、`internal/cursor` 几何、`NewDisplay` + `MOUSEEVENTF_VIRTUALDESK`、主机侧分辨率变化 | `internal/capture`、`internal/cursor`、`internal/inject` | 同名包；与快照的差异：枚举回调全进程只建一次（`syscall.NewCallback` 有上限，`Refresh` 每秒枚举）、`Screen` 有 `Refresh` / 可开关的指针与 `CAPTUREBLT`、光标默认关并按查看器要求打开（快照对所有查看器都画）、绝对坐标换算改为对每个像素都精确的 `ceil(p·65536/n)` | 帧头本来就带尺寸；新增能力位 `CapResize`、`CapCursor`（v1） | ☑ |
| B3 | `pinnedClientTLS`（按指纹固定，用于二维码配对）、`FPSFn`（输入活跃时临时升帧）、`OnInput`/`OnFrame` 回调 | `internal/remote/{tls,host}.go` | 同名包；与快照的差异：固定指纹经 `DialOptions.ExpectFingerprint` 提供（不是另一个 Dial 函数），写法宽松（大小写、冒号、空格）、格式不对立刻报错而不是当成"主机变了"，此时主机库可为 nil 且既不读也不写；`FPSFn` 对 ≤0 保持当前速率、对过大值夹到 `MaxFPS`（快照对 0 会除零、对 2³⁰ 会让 `Ticker.Reset` panic——变异测试证实）；新增 `Boost`（`Touch`→`OnInput`，`Rate`→`FPSFn`）；`OnInput` 只对输入 / 文本、不对确认 | 无（纯本地行为） | ☑ |
| B4 | 合成画面源、仅记录输入的注入器、`nx-probe`、`logsink` | `internal/{synth,probe,logsink}`、`cmd/nx-probe` | 同名包；与快照的差异：`synth` 有五种场景（`blocks` 即快照原样的小块 + 进度条；`static`、`docscroll`、`terminal`、`video` 是阶段 6 基准要用的），四个角有红 / 绿 / 蓝 / 黄标记，`synth.VerifyMarkers` 能识别颜色顺序错、上下翻转、左右镜像，所有画面第 4 字节为 255；`logsink` 可给定屏幕尺寸，指针位置越界就返回错误（真桌面会悄悄夹取，掩盖发送方的 bug），历史有上限；`probe` 走 `DialOptions.ExpectFingerprint`，结果 JSON 带 `schema`、协议版本、主机名与能力，按主机声明的能力决定发哪些输入（不会向没有文本能力的主机发文本），可 `-v0` 以原始协议连接，`-markers` 校验合成主机的画面；`nx-probe` 的 `run()` 可测，退出码 0 / 1 / 2 | 无 | ☑ |
| B5 | `mobile/` 会话（最后坐标缓存 + 输入互斥）、`cmd/nx-host`（stdio JSON Lines，补 schema 版本 / 请求关联 ID / 超时）、AAR 构建脚本 | `mobile/`、`cmd/nx-host` | `mobile/`、`cmd/nx-host`、`internal/hostd`（可测的无界面主机，进程壳只做参数和信号）、`scripts/build-aar.sh`、`scripts/e2e-host-probe.py`；差异逐条写在 `docs/nx-host.md` 与 `docs/mobile.md` 的末节 | `nx-host` 的控制接口（JSON Lines schema 1）由 go-nx 定义并有契约文档；`mobile` 的 Java API 固定在 `mobile/api.golden`。**AAR 的完整构建没有在本机验证**（无 Android SDK/NDK）；绑定生成与 API 比对已验证 | ☑（AAR 实构建待补） |

## 刻意不回移的

| 内容 | 原因 |
|---|---|
| NightDesk 的 WPF / Android 界面代码 | 不在 go-nx 范围；go-nx 只提供它们依赖的核心与契约 |
| NightDesk 把 `Policy` 的配对码做成"空码接受任意设备" | go-nx 已有自己的配对码实现（阶段 1），语义更严（成功即关窗、累计错码关窗）；保留无码模式只为不经界面的流程 |

## 待 NightDesk 跟进（现在不做）

NightDesk 改为引用 go-nx 产物（`nx-host.exe`、AAR、协议文档）属于另一件事，等用户要求再做。届时需要核对：协议 v0 是否仍与其已发布客户端兼容（见 `protocol.md` 的兼容矩阵）。
