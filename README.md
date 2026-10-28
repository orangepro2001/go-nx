# go-nx

Windows 10/11 之间的轻量局域网远程查看与操作。纯 Go，无 cgo，单个静态 `go-nx.exe`（GUI 子系统）。同一个程序既能共享本机屏幕（被控端），也能连接别人（控制端）。

- 传输：QUIC（`quic-go`）+ TLS 1.3，自签证书，客户端 TOFU 固定主机指纹
- 画面：GDI `BitBlt` 抓主屏，64×64 tile 差分，无损编码；**帧率是动态的**：画面在变时 10 fps，控制端操作时（最近一次输入后 2 s 内）升到 30 fps，画面和输入都静止 3 s 后降到 2 fps，任何输入立刻唤醒（静止时码率接近 0）
- 界面：Ebiten 窗口 + 系统托盘，关闭窗口只是藏进托盘
- 更新：应用内自更新（经本机 release-publish，签名校验）。入口在窗口主页右上角的按钮和系统托盘菜单；只在你点击后才替换并重启。每次改动的发布流程见 [`docs/release.md`](docs/release.md)

## 现状与限制（请先读）

这是早期版本（见 `VERSION`）。**以下是当前真实状态，不是目标：**

- **默认是严格模式**：新设备要先配对（主机点 "Pair new device"，在新设备的 Password 里填主机显示的 6 位码）或输入主机密码。主页上的模式按钮可以切到**开放模式**（任何能连到 UDP 7000 的人都能控制这台机器，需要点两次确认）；切回严格模式时，因开放而放进来的、未经验证的连接会**立刻断开**，已配对的设备不受影响。首次连接一台主机时，控制端会显示它自报的 ID，请与主机主页上的 `ID:` 比对后再按 Enter。
- 密码和身份私钥以**明文**存在 `%AppData%\go-nx\`。
- 默认共享主屏，可用 `-display` 选另一块；分辨率或显示器变化会在几秒内跟上（0.5.0 起，**未在真机上验证**）。远端画面默认不画鼠标指针（自带指针的桌面查看器不需要），手机端等可以请求把指针画进画面。
- 协议有版本与能力协商（v1），同时仍接受原来的 v0 查看器；没有音频、剪贴板、文件传输。
- 自动化测试**没有**在真实 Windows 上跑过 GDI 抓屏和 `SendInput`，见 [`docs/ACCEPTANCE.md`](docs/ACCEPTANCE.md)。

逐项带代码位置的清单：[`docs/IMPLEMENTED.md`](docs/IMPLEMENTED.md)。`docs/` 里其余的设计文档是设计意图，不是实现状态。

## 构建与测试

```sh
./build.sh                  # genicon → vet → Windows vet → test → 交叉编译到 build/，版本取自 VERSION
go test -race -cover ./...  # 核心包在 Linux 上即可测试；cmd/go-nx、capture、inject 只在 Windows 上编译
./scripts/e2e-update.sh     # 更新路径端到端（临时数据目录 + 回环端口，不碰线上服务）
```

需要 Go（见 `go.mod`）。`build.sh` 在装有 `x86_64-w64-mingw32-windres` 时重新生成图标资源，否则沿用已提交的 `.syso`。

## 运行

```
go-nx.exe [-connect host:port] [-port 7000] [-listen IP[:port]] [-fps 10] [-fps-active 30] [-fps-idle 2] [-hidden] [-no-update]
```

端口和监听地址也可以在 **设置 → 网络** 里改，立即生效；命令行给了 `-port` / `-listen` 时以命令行为准。开机启动在 **设置 → 常规**。

- 被控端：启动即在监听。在"本机"页点"开始配对（5 分钟）"开 5 分钟配对窗口，页面会显示一次性 6 位码；新设备连接时把它填在 Password 里，成功后窗口即关闭，之后该设备不用密码。"Forget paired devices" 清空配对（点两次确认）。
- 控制端：主页填 `IP:端口` 后点 "Connect"（回车切换字段，最后一个字段回车即连接）；`F3` 看日志；`Ctrl+Alt` 组合键用于标签页操作，细节见界面提示。
- `-listen 192.168.1.5` 只在这块网卡上监听（默认所有接口）；只接受 IP，不接受主机名。
- 帧率：`-fps` 是画面在变化时的帧率，`-fps-active` 是控制端操作时的帧率（`0` 关闭升帧），`-fps-idle` 是画面和输入都静止 3 s 后的帧率（`0` 关闭降帧）。`-fps-active 0 -fps-idle 0` 就是固定帧率。
- 配置、身份、信任列表、日志都在 `%AppData%\go-nx\`（日志 `go-nx.log`）。单实例（命名互斥量 `Local\go-nx-single-instance`）。

## 代码结构

| 路径 | 作用 |
|---|---|
| `internal/proto` | 线上格式：控制流消息、帧头、opId 去重 |
| `internal/tile` | tile 差分与编码/解码 |
| `internal/remote` | QUIC 主机/客户端会话、TLS、输入控制器（经 `Source`/`Injector` 接口接触平台层） |
| `internal/trust` | 信任库与准入策略 |
| `internal/capture`、`internal/inject` | 仅 Windows：GDI 抓屏、`SendInput` 注入 |
| `internal/ui`、`cmd/go-nx` | Ebiten 界面、托盘、主程序 |
| `internal/hostd`、`cmd/nx-host`、`cmd/nx-probe` | **核心**：无界面主机（stdin/stdout JSON Lines）和验收探针；作为单独的应用 `go-nx-core` 发布（`CORE_VERSION`，zip 含契约），见 [`docs/release.md`](docs/release.md) |
| `mobile` | gomobile 的手机端核心（契约 [`docs/mobile.md`](docs/mobile.md)） |
| `internal/update`、`internal/relhub` | 自更新与 release-publish SDK 副本 |

## 文档

- [`docs/context-recovery.md`](docs/context-recovery.md)：新会话先读这个
- [`docs/IMPLEMENTED.md`](docs/IMPLEMENTED.md)：实现状态（以代码为准）
- [`docs/ACCEPTANCE.md`](docs/ACCEPTANCE.md)：已验证 / 未验证
- [`docs/release.md`](docs/release.md)：发布与自更新
- `docs/handoff-*.md`：每次任务的交接记录
