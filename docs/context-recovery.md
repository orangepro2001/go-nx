# 上下文恢复（新会话先读这个）

最后更新：2026-10-28。每次任务完成后更新本文件，并另写一份带日期的 `docs/handoff-*.md`。

## 项目一页纸

go-nx：Windows 10/11 上的轻量局域网远程控制。纯 Go、无 cgo、单个静态 exe（`build/go-nx.exe`，windows/amd64，GUI 子系统）。
既是被控端（共享本机屏幕）也是控制端；QUIC（`quic-go`）+ TLS 1.3，TOFU 配对；托盘（`fyne.io/systray`）+ Ebiten 窗口；关闭窗口只是藏进托盘。
配置、身份、信任列表、日志都在 `%AppData%\go-nx\`（`go-nx.log`）。单实例用命名互斥量 `Local\go-nx-single-instance`。
仓库：`git@github.com:orangepro2001/go-nx.git`，本地 `main`。设计文档在 `docs/`（`minimal-plan.md` 是定稿范围）。

## 当前状态（2026-10-28）

- 已接入本机 release-publish：app `go-nx`，平台 `win-x64`，**0.1.0（versionCode 100）已发布**到线上。
- 应用内自更新已实现并通过测试；**Windows 实机上的托盘、替换 exe、重启流程还没验证过**。
- 本地提交 `98bda14`，**没有 push**。`docs/handoff-2026-10-28-release-publish.md` 和本文件写在该提交之后，未提交。
- 全局 git 身份：`orangepro2001 <yangziyi0801@gmail.com>`（2026-10-28 设置）。`98bda14` 的作者是 `tony`，没有改写。

## 常用命令

```sh
./build.sh                     # genicon → vet → Windows vet → test → 交叉编译，版本取自 VERSION
go test -race ./internal/update/
./scripts/e2e-update.sh        # 真实 relhub + 临时目录 + 随机回环端口，不碰线上服务
NOTES="改了什么" ./release.sh  # 需要干净工作区；ALLOW_DIRTY=1 可跳过
relhub check -app go-nx -platform win-x64 -code 100; echo $?   # 0 最新，10 有更新，20 强制
```

发新版：改 `VERSION`（`MAJOR.MINOR.PATCH`，minor/patch < 100）→ 提交 → `./release.sh`。versionCode = `major*10000 + minor*100 + patch`，同平台必须递增。详见 `docs/release.md`。

## 关键文件索引

| 路径 | 作用 |
|---|---|
| `VERSION` | 发布版本号，唯一来源 |
| `build.sh` / `release.sh` | 构建（注入 `-X main.version`）/ 构建并发布 |
| `scripts/e2e-update.sh` | 更新路径的端到端检查 |
| `cmd/go-nx/main_windows.go` | `main`、标志位（`-connect -port -fps -hidden -update-url -no-update -restarted`） |
| `cmd/go-nx/update_windows.go` | `version` 变量、`startUpdates`、`updateClicked`（安装并重启） |
| `cmd/go-nx/tray_windows.go` | 托盘菜单，含版本项和更新入口 |
| `cmd/go-nx/winapi_windows.go` | `alreadyRunning(wait)`：单实例互斥量，重启时带重试 |
| `internal/update/update.go` | 更新器：`New`、`Run`、`CheckNow`、`Install`、`Replace`、`CleanupOld`、`PinnedKeys` |
| `internal/relhub/` | release-publish 客户端 SDK 的副本（来源与同步方法见其 README） |
| `docs/release.md` | 发布流程、客户端行为、公钥轮换、测试 |

## release-publish 服务（本机）

- systemd 用户单元 `release-publish.service`，监听 `127.0.0.1:18790`，经 tailscale serve 暴露为 `https://tony-ai-pc-1.tail9f7f1f.ts.net/release`（仅 tailnet 内）。
- 数据目录 `~/.local/share/release-publish/`（`admin.token`、`keys/signing.ed25519`、`apps/`）；密钥备份 `~/backups/release-publish-keys/`。本文档**不记录**任何 token 或私钥。
- 服务公钥 `k4thZR+OVYSGAT/9j5F39csgGqTL0t3X6GYdNCmuRI8=`（keyId `f5faa9f9858b789f`），已编译进 `internal/update.PinnedKeys`。
- 服务自己的文档：`/home/tony/dev/release-publish/docs/{USAGE,API,SDK}.md`。服务里另有尚未导入的 nightdesk / codex-harness / clipbridge（`relhub import ...`），与 go-nx 无关。
- 本机全局规则：服务只能经 tailscale serve 的 https 域名访问，后端只监听 127.0.0.1；不要新增或改动除 `/release` 以外的 serve 路径。

## 已知的坑

- 公钥轮换会让只认旧公钥的客户端永久拒绝更新；必须先发布同时信任新旧公钥的版本（`docs/release.md`）。
- 已发布的版本不可覆盖；发错了递增重发，必要时 `relhub yank`。
- 做本地端到端测试必须设置 `RELHUB_DATA` 指向临时目录，否则 `relhub` 会读线上 `admin.token` 并连线上服务。
- `alreadyRunning` 重试时必须 `CloseHandle`，否则旧进程退出后互斥量仍被撑住。
- 用 Python 之类批量替换文件时，找不到匹配会静默不改，改完要 `git diff` 核对。
- 安装在只读目录时更新会失败，原因只写在日志里。

## 下一步清单

1. 在 Windows 机器上手动装 0.1.0，再发布 0.1.1，验证托盘检查 → 下载 → 点击重启 → 无"已在运行"弹窗 → `.old` 被清理。
2. 提交 `docs/` 里的两份文档；push 需要用户单独授权。
3. 可选：go-nx 专用 CI token；窗口主页也显示更新提示；Windows 代码签名；把签名私钥备份另存一份到本机之外。

## 最近一份交接文档

[`docs/handoff-2026-10-28-release-publish.md`](handoff-2026-10-28-release-publish.md)：成果、验证了什么/没验证什么、遇到的问题、设计取舍、经验、未完成项。
