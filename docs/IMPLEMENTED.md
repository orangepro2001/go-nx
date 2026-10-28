# go-nx 实现状态（以代码为准）

- 核对日期：2026-10-28，基线提交 `52361cb`（v0.1.0）
- 用途：`docs/` 里的几份设计文档写的是**设计意图**，不是实现。判断"go-nx 现在到底能做什么"以本文件和代码为准；本文件每条都给出文件:行，改代码后要同步更新。
- 状态：**已实现** / **部分** / **未实现**（设计里提过）/ **不做**（`minimal-plan.md` 明确排除）
- 没有任何一项在真实 Windows 机器上做过自动化验证（见 `ACCEPTANCE.md`）。

## 1. 采集与画面

| 项 | 状态 | 实际情况 | 位置 |
|---|---|---|---|
| 采集方式 | 已实现 | GDI `BitBlt`（`SRCCOPY\|CAPTUREBLT`）到 32bpp 自上而下 DIB。**没有** DXGI Desktop Duplication / WGC | `internal/capture/capture_windows.go:36,103-104` |
| 显示器范围 | 部分 | 只采主屏：尺寸取 `GetSystemMetrics(SM_CXSCREEN/CYSCREEN)`，原点固定 (0,0)；无 `EnumDisplayMonitors` | `capture_windows.go:33,65` |
| 分辨率/显示器变化 | 未实现 | 尺寸在启动时固定，主机从不下发尺寸变化；需重启主机 | `host.go`（`Serve` 只读一次 `src.Size()`） |
| DPI | 部分 | 无 manifest；`NewScreen` 调 `SetProcessDPIAware`（system-aware，非 per-monitor）是副作用，依赖它先于 Ebiten 初始化。`shcore` 声明后被丢弃（死代码） | `capture_windows.go:18,22,29` |
| 鼠标指针 | 未实现 | `BitBlt` 不含指针，代码里没有 `GetCursorInfo`/`DrawIconEx`，协议也没有指针消息；查看器只显示本地系统指针 | — |
| 采集失败恢复 | 未实现 | `Grab` 出错即结束会话，不重建 `Screen`（锁屏/UAC/分辨率变化后每个会话都会死） | `internal/remote/host.go:160` |
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
| 对恶意主机的上限 | 已实现（2026-10-28） | 公布的帧尺寸 ≤ 16384 px/边、≤ 2²⁵ 像素，否则结束会话（`proto.ErrFrameSize`）；单个更新流 ≤ 约 138 MiB，超出结束会话；调色板 RLE 展开前校验；raw tile 的 deflate 最多读 size+1 字节；每帧记录数 ≤ tile 数；更新流 15 s 无数据即放弃；主机自己的屏幕超出上限时 `Serve` 直接报错 | `proto.go`（`MaxFrameDim`、`ValidFrameSize`）、`client.go`（`stallReader`）、`tile.go`（`decodePalette`、`Decode`）、`host.go` |
| 版本/能力协商 | **未实现** | Hello 只有 `type, pwLen, password`；未知消息类型直接结束会话 | `proto.go:18,98-137` |
| 输入种类 | 已实现 | 鼠标移动/按下/抬起、竖直滚轮、按键按下/抬起（Windows VK，`0x100` 位表示扩展键）。**没有**横向滚轮、X 键、Unicode 文本 | `proto.go:28-33` |
| opId 与去重 | 已实现 | `u64` opId，主机保留最近 1024 个；opId=0（鼠标移动、按键自动重复）不去重也无回执。**不是** UUID，无 `clientTs` | `proto.go:178`、`host.go:210` |
| 回执状态 | 部分 | 只有 Executed / Failed；没有 Accepted / Timeout | `proto.go:38-39` |
| 未确认提示 | 已实现 | 3 s 没收到回执只提示"不确定"，不自动重试 | `viewer_windows.go:30,170` |
| 单控制者 | 已实现 | 同时只有一个控制者，新连接顶替旧连接，旧会话持有的按键/鼠标键被释放 | `internal/remote/input.go`、`cmd/go-nx/host_windows.go` |
| 断线后重连 | 部分 | 首次拨号最多尝试 4 次（握手时限 2/3/4/6 s）；断线后需手动重连 | `cmd/go-nx/tabs_windows.go:88` |

## 4. 安全与配对

| 项 | 状态 | 实际情况 | 位置 |
|---|---|---|---|
| 认证默认值 | **默认关闭** | `require_auth` 仅当设置值为 `"1"` 才开；关闭时任何能连到端口的人都被允许 | `cmd/go-nx/host_windows.go:72`、`internal/trust/policy.go:35,77` |
| 证书/指纹 | 已实现 | 自签证书；客户端 TOFU 固定主机指纹，变化则 `ErrHostChanged` 硬失败；服务端要求任意客户端证书，信任由 `Policy` 决定 | `tls.go:57,72-91` |
| 配对 | 部分 | 主机上点按钮开 2 分钟窗口，**下一台**连入设备被信任。**没有** 6 位码、没有指纹核对、没有本机确认弹窗 | `policy.go:86-90`、`host_windows.go:25` |
| 密码 | 部分 | 可选；错 5 次**全局**锁 1 分钟，计数不衰减，不按来源 | `policy.go:20-21,96-106` |
| 首次连接 | 风险 | 密码在校验主机指纹之前就发出（TOFU 首次） | `internal/remote/client.go:45` |
| 撤销已信任设备 | **未实现** | `Store` 只有 Get/Len/Set；界面没有取消配对；只能手改 `trusted.json` 并重启 | `internal/trust/trust.go` |
| 凭据存储 | **明文** | 主机密码在 `settings.json`；连接密码在 `connections.json`；`identity.pem` 私钥未加密；Windows 上 0600 无效 | `host_windows.go:88-95`、`internal/profiles/profiles.go:12-17` |
| 监听范围 | 全接口 | `":%d"`，默认 UDP 7000，无绑定选项 | `host_windows.go:151`、`main_windows.go:98` |
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
