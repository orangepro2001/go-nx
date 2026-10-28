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
- 当前工作是 [`ROADMAP.md`](ROADMAP.md) 的开发计划：**阶段 0 完成**（基线、状态文档、CI）；**阶段 1 已完成**，其内容：「恶意主机上限 + fuzz」「JSON 原子写 + 损坏文件隔离」，「撤销已信任设备 / 忘记主机身份」（逐设备列表还没有）。「按来源限流 + 未认证连接上限」已完成。「默认开启认证」「一次性配对码」已完成。「首次连接指纹核对」已完成。「监听地址可选（`-listen`）」已完成，**阶段 1 完成**（密码哈希 + DPAPI 经评估暂缓，理由见 ROADMAP「暂缓项」）。下一步是阶段 2（协议规范与版本协商）。
- 所有提交已推送到 `origin main`；CI（`.github/workflows/ci.yml`，linux + windows-latest）在 `820d1f7` 上两个作业都通过。
- 全局 git 身份：`orangepro2001 <yangziyi0801@gmail.com>`（2026-10-28 设置）。`98bda14` 的作者是 `tony`，没有改写。

## 工作约束（用户 2026-10-28 明确）

- **本仓库每完成一项可交付的工作就自动 commit + push 到 `origin main`**，无需询问。先跑完检查，通过才提交；只按文件名暂存，不用 `git add -A`，不 amend / reset / force push。
- 这条授权**不含**：`release.sh` 发新版本、打 tag / GitHub Release、装依赖、改认证——这些仍需单独问。
- **不要改动 NightDesk 侧任何源码**（`/home/tony/dev/nightdesk-*`、`/home/tony/dev/native` 等）：那个项目不是最新版本，只读，当回移参考；改它对 go-nx 的依赖留到以后。
- 项目是**单一上游**：NightDesk 的通用修复回移到这里。协议要保持对 NightDesk 已发布客户端的兼容（`MsgText=4`、`KindWheelH=7`）。
- 只有一台日常使用的 Windows PC：会改系统状态的实机项只写成人工清单（`ACCEPTANCE.md` §3）。真实 Windows 行为优先用 CI 的 `windows-latest` 验证。

## 常用命令

```sh
./build.sh                     # genicon → vet → Windows vet → test → 交叉编译，版本取自 VERSION
go test -race -cover ./...     # 核心包 Linux 上即可测；capture/inject/cmd 只在 Windows 编译
GOOS=windows go test -c -o $TMP/x.exe ./internal/capture   # Windows 测试可交叉编译，用 wine 或 CI 跑
gh run list --limit 3          # 看 CI；gh 已登录 orangepro2001
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
| `docs/ROADMAP.md` | 开发路线图与各阶段状态（**先读**） |
| `docs/IMPLEMENTED.md` | 实现状态，以代码为准，带文件:行；设计文档只是意图 |
| `docs/ACCEPTANCE.md` | 已验证 / 未验证；CI runner 实测结果；人工实机清单 H1–H12 |
| `docs/review-2026-10-28-nightdesk-integration-notes.md` | 对 NightDesk 集成经验文档的逐条核对与发现的缺陷 |
| `.github/workflows/ci.yml` | linux（gofmt / vet / -race / 可移植核心交叉编译）+ windows-latest（build / test / 桌面冒烟） |
| `internal/{capture,inject}/*_windows_test.go` | 真实桌面冒烟；需 `GO_NX_WINDOWS_E2E=1`，会画窗口、动光标，**别在日常 PC 上开** |
| `README.md` | 简介、限制、构建与运行 |

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
- **推送后先等 CI 再继续**：CI 用了 `cancel-in-progress`，连续推送会取消前一次运行；本地绿的测试在 CI 上可能因时序失败（门限测试就这样失败过一次）。`gh run list --limit 3` 看结果。
- 批量文本替换（Python `replace`）静默不改过两次：缩进 tab 数、gofmt 重新对齐都会让旧文本对不上。关键改动用 Edit，批量脚本里断言，改完 `git diff` 核对。
- `build.sh` 对 `cmd/*/` 的每个目录都用 `-H=windowsgui` 构建；加入控制台程序（`nx-host`、`nx-probe`）前要先改这个循环。
- zsh 里 `grep --include=*.go` 会报 "no matches found"（被当 glob），要加引号。
- 计划模式下 Explore agent 不会跑 `go test`；测试基线要自己跑。
- 本机 wine 可跑交叉编译出的 Windows 测试（用临时 `WINEPREFIX`，别污染 `~/.wine`；窗口测试要 `xvfb-run`）。wine 的 GDI / `SendInput` 行为**不等于**真 Windows（例如它在 1280×800 下让绝对移动偏 1px，而 windows-latest 的 1024×768 上无偏差）。
- 绝对坐标映射 `x*65535/(w-1)` 的偏差与分辨率有关，只在一个分辨率下通过不代表没问题。

## 下一步清单

按 [`ROADMAP.md`](ROADMAP.md)：

1. **阶段 1 安全基线**（下一个）：默认开认证（含旧设置迁移）、一次性配对码 + 指纹核对、校验前不发密码、密码哈希 + DPAPI、撤销设备、按来源限流、恶意主机上限、JSON 原子写、fuzz。
2. 阶段 2 协议规范与版本协商 → 阶段 3 回移 NightDesk 修复（只读快照 `/home/tony/dev/nightdesk-native-ux-20261028`）。
3. 仍待用户在 Windows 上做：`ACCEPTANCE.md` §3 的 H1–H17（含自更新 H1–H3 和阶段 1 新增界面 H13–H17）。
4. 可选：go-nx 专用 CI token；窗口主页也显示更新提示；Windows 代码签名；把签名私钥备份另存一份到本机之外。

## 最近一份交接文档

[`docs/handoff-2026-10-28-phase1-security-baseline.md`](handoff-2026-10-28-phase1-security-baseline.md)：阶段 1（安全基线）的成果、行为变更（发布前要写进说明）、验证、问题、取舍、经验。
更早：[`docs/handoff-2026-10-28-phase0-baseline.md`](handoff-2026-10-28-phase0-baseline.md)（阶段 0）、[`docs/handoff-2026-10-28-release-publish.md`](handoff-2026-10-28-release-publish.md)（接入 release-publish）。
