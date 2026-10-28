# go-nx

Windows 10/11 之间的轻量局域网远程查看与操作。纯 Go，无 cgo，单个静态 `go-nx.exe`（GUI 子系统）。同一个程序既能共享本机屏幕（被控端），也能连接别人（控制端）。

- 传输：QUIC（`quic-go`）+ TLS 1.3，自签证书，客户端 TOFU 固定主机指纹
- 画面：GDI `BitBlt` 抓主屏，64×64 tile 差分，无损编码，默认 10 fps（设计目标是静止时码率接近 0，**尚未测量**）
- 界面：Ebiten 窗口 + 系统托盘，关闭窗口只是藏进托盘
- 更新：应用内自更新（经本机 release-publish，签名校验）。入口在窗口主页右上角的按钮和系统托盘菜单；只在你点击后才替换并重启。每次改动的发布流程见 [`docs/release.md`](docs/release.md)

## 现状与限制（请先读）

这是早期版本（见 `VERSION`）。**以下是当前真实状态，不是目标：**

- **认证默认开启**：新设备要先配对（主机点 "Pair new device"，在新设备的 Password 里填主机显示的 6 位码）或输入主机密码。若在主页取消勾选 "Require pairing or password"，任何能连到 UDP 7000 的人都能控制这台机器。首次连接一台主机时，控制端会显示它自报的 ID，请与主机主页上的 `ID:` 比对后再按 Enter。
- 密码和身份私钥以**明文**存在 `%AppData%\go-nx\`。
- 只采**主屏**；远端画面里**看不到鼠标指针**；分辨率或显示器变化后需重启主机。
- 没有音频、剪贴板、文件传输、多显示器、协议版本协商。
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
go-nx.exe [-connect host:port] [-port 7000] [-listen IP[:port]] [-fps 10] [-hidden] [-no-update]
```

- 被控端：启动即在监听。点 "Pair new device (2 min)" 开 2 分钟配对窗口，页面会显示一次性 6 位码；新设备连接时把它填在 Password 里，成功后窗口即关闭，之后该设备不用密码。"Forget paired devices" 清空配对（点两次确认）。
- 控制端：主页填 `IP:端口` 后点 "Connect"（回车切换字段，最后一个字段回车即连接）；`F3` 看日志；`Ctrl+Alt` 组合键用于标签页操作，细节见界面提示。
- `-listen 192.168.1.5` 只在这块网卡上监听（默认所有接口）；只接受 IP，不接受主机名。
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
| `internal/update`、`internal/relhub` | 自更新与 release-publish SDK 副本 |

## 文档

- [`docs/context-recovery.md`](docs/context-recovery.md)：新会话先读这个
- [`docs/IMPLEMENTED.md`](docs/IMPLEMENTED.md)：实现状态（以代码为准）
- [`docs/ACCEPTANCE.md`](docs/ACCEPTANCE.md)：已验证 / 未验证
- [`docs/release.md`](docs/release.md)：发布与自更新
- `docs/handoff-*.md`：每次任务的交接记录
