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
| B2 | 多显示器枚举与选择（`Displays`/`Open(selector)`）、per-monitor DPI v2、帧内光标合成、`internal/cursor` 几何、`NewDisplay` + `MOUSEEVENTF_VIRTUALDESK`、主机侧分辨率变化 | `internal/capture`、`internal/cursor`、`internal/inject` | 同名包 | 无线上变化（帧头本来就带尺寸） | ☐ |
| B3 | `pinnedClientTLS`（按指纹固定，用于二维码配对）、`FPSFn`（输入活跃时临时升帧）、`OnInput`/`OnFrame` 回调 | `internal/remote/{tls,host}.go` | 同名包 | 无 | ☐ |
| B4 | 合成画面源、仅记录输入的注入器、`nx-probe`、`logsink` | `internal/{synth,probe,logsink}`、`cmd/nx-probe` | 同名包 | 无 | ☐ |
| B5 | `mobile/` 会话（最后坐标缓存 + 输入互斥）、`cmd/nx-host`（stdio JSON Lines，补 schema 版本 / 请求关联 ID / 超时）、AAR 构建脚本 | `mobile/`、`cmd/nx-host` | 同名包 | `nx-host` 管理协议由 go-nx 定义 | ☐ |

## 刻意不回移的

| 内容 | 原因 |
|---|---|
| NightDesk 的 WPF / Android 界面代码 | 不在 go-nx 范围；go-nx 只提供它们依赖的核心与契约 |
| NightDesk 把 `Policy` 的配对码做成"空码接受任意设备" | go-nx 已有自己的配对码实现（阶段 1），语义更严（成功即关窗、累计错码关窗）；保留无码模式只为不经界面的流程 |

## 待 NightDesk 跟进（现在不做）

NightDesk 改为引用 go-nx 产物（`nx-host.exe`、AAR、协议文档）属于另一件事，等用户要求再做。届时需要核对：协议 v0 是否仍与其已发布客户端兼容（见 `protocol.md` 的兼容矩阵）。
