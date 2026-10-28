# 核对记录：《go-nx 集成经验与独立维护建议》

- 日期：2026-10-28
- 被核对的文档：NightDesk × go-nx 集成经验（2026-10-28 11:06 整理，正文不在本仓库）
- 核对对象：
  - 本仓库 `52361cb`（v0.1.0），即该文档所说的 go-nx 基线 `124331b` 加 release-publish 自更新
  - NightDesk 源码快照 `/home/tony/dev/nightdesk-native-ux-20261028`（Go：`mobile/`、`cmd/nx-host`、`cmd/nx-probe`、`internal/{cursor,synth,probe}` 等）
  - `/home/tony/dev/nightdesk-android-ux-20261028`（Kotlin）
- **NightDesk 侧只读**：用户要求暂不改动 NightDesk 源码（它不是最新版本）。以上快照可能落后于实际最新版，下文关于 NightDesk 的结论都是"在该快照中成立"。

## 1. 逐条结论

| 文档论断 | 结论 | 证据 / 补充 |
|---|---|---|
| §2.1 控制端与被控端同在一个 Ebiten 应用；核心在 `internal/` 不可导入 | 成立，但耦合比文档说的轻 | `proto/tile/trust/profiles` 只依赖标准库，`remote` 只依赖 quic-go，经 `Source`/`Injector` 接口接触平台层；这些包在 linux / darwin / windows / `android/arm64` 上都能编译。真正的障碍是模块名 `go-nx`（非合法导入路径）和全在 `internal/` |
| §2.2 设计文档的 DDA / LZ4 / xxHash 不是实际路径 | 成立，且更广 | 见 [`IMPLEMENTED.md`](IMPLEMENTED.md) §7 的完整对照表 |
| §2.3 仅主屏 GDI，无光标，无多屏 / DPI 模型 | 成立 | DPI 感知只是 `NewScreen` 调 `SetProcessDPIAware` 的副作用（system-aware，非 per-monitor），无 manifest |
| §2.4 无协议版本 / 能力协商，功能不全 | 成立 | Hello 无版本字段，ALPN 固定 `go-nx`，未知消息直接结束会话 |
| §2.5 安全默认值需收紧 | 成立，且更糟 | 认证默认关；密码与私钥明文；监听全接口；首次连接在校验指纹前发密码；无法撤销已信任设备；锁定是全局的 |
| §3 NightDesk 侧改造 | 在快照中成立 | `cmd/nx-host`（stdio JSON 事件）、`pinnedClientTLS`、`Policy.OpenPairing/ClosePairing`、`internal/{synth,probe}`、`cmd/nx-probe`、`mobile/session.go` |
| §4.1 帧内合成光标 | 代码成立，像素效果未验证 | `capture_windows.go` 的 `drawCursor`，几何在 `internal/cursor/position.go` |
| §4.2 颜色：Android 交换 B/R 并置 alpha=255；WPF 用 Bgr32 | Android 成立；WPF 无法核对 | `RemoteScreenView.kt` 的 `applyTile` + `RemoteScreenColorInstrumentedTest.kt`；WPF 源码不在本机。**go-nx 自带的 Ebiten 查看器从没有这个问题**（`bgraToRGBA` 已交换并置 alpha） |
| §4.3 鼠标释放跳左上角：缓存最后坐标并串行化 | 成立 | `mobile/session.go` 的 `mouseX/Y` + `inputMu`，`Move` 失败不覆盖。**这个 bug 只存在于 mobile session**；go-nx 查看器每个事件都带 x,y |
| §4.4 物理光标闪烁未复现；源码无 `ShowCursor/SetCursor/ClipCursor` | "无这些调用"成立，但漏了最可疑的一项 | go-nx 和 NightDesk 快照都用 `BitBlt(..., SRCCOPY\|CAPTUREBLT)`。据记忆，`CAPTUREBLT` 是 GDI 抓屏时宿主光标闪烁的常见原因——**待验证假设，未实测**。A/B 开关列入路线图阶段 5 |
| §8 Windows 1.6.4 / Android 1.3.4 已发布、工作树有未提交变更 | 无法核对 | 快照无 `.git`；1.6.1–1.6.4 交接文档不在本机（只找到 stage4 的两份）；WPF 源码在 Windows 机器上 |

## 2. 文档没列出、核对中发现的缺陷

- **客户端对恶意主机无防护**：`tile.NewFrame(W,H)` 可到 ~16 GiB；raw tile 先 `io.ReadAll(flate)` 后校验（解压炸弹）；RLE 2→1024 字节放大；帧流无读超时；`LimitReader` 静默截断（`client.go`、`tile.go`）。
- **JSON 非原子写**（`trust.go`、`profiles.go`），启动解析失败是 `log.Fatal`。
- **`Grab` 出错即结束会话且不重建 `Screen`**；主机从不下发尺寸变化。
- **`Win+L` 会被转发**到主机。
- **按键发送顺序随机**：遍历 map（`viewer_windows.go`、`input.go`），同一帧内的 Shift+A 可能先 A 后 Shift。
- **退出 / 崩溃不释放输入**：`quitApp` 直接 `os.Exit`，主机最多 45 秒后才察觉；失败的释放也会清空状态、不重试。
- **清理性释放让主机光标跳到按下位置**；拖出视口再松开，抬起被夹到边缘导致跳变。
- **绝对坐标可能偏 1px**（`x*65535/(w-1)` 截断）；键表缺 F13–F24 / PrintScreen，扩展键缺 `VK_APPS` / `VK_NUMLOCK`。
- **`SendInput` 在 UI 线程同步写**，流阻塞会冻结界面。
- **自更新**：`Install` 前不重新校验 `.new`；两次 rename 非原子，中途崩溃会既无 `go-nx.exe` 也不自恢复；多项边界无测试（见 `ACCEPTANCE.md` §2）。
- **零测试**：`cmd/go-nx`、`capture`、`inject`、`relhub/*`。

## 3. 与 NightDesk 分叉的漂移（回移范围）

模块路径不同（`github.com/orangepro2001/nightdesk/native`）。`tile` 完全一致；`proto`（+`MsgText=4`、`KindWheelH=7`、`MaxTextLen`）、`remote/{client,host,tls}`、`trust/{policy,trust}`、`capture`、`inject`（`NewDisplay` + `VIRTUALDESK` + `InjectText`）已分歧。分叉里没有 `ui/profiles/update/relhub/logging`。

**直接后果**：NightDesk 的 Android 发 `MsgText` 给原版 go-nx 主机，会走 `ReadMsg` 的未知类型分支，整个会话被结束。所以协议 v0 必须把这些扩展正式收进来（见路线图阶段 2 / 3）。

## 4. 本次未能核对

- WPF 查看器是否用 `Bgr32`（§4.2）。
- NightDesk 1.6.1–1.6.4 / 1.3.1–1.3.4 的交接与验收记录；"已发布""有未提交变更"的现状（§8）。
- 手势、IME 与真机手感（§4.5）。
- 所有需要真实 Windows 桌面的像素 / 输入 / 光标行为——见 [`ACCEPTANCE.md`](ACCEPTANCE.md)。
