# 上下文恢复（新会话先读这个）

最后更新：2026-10-28。每次任务完成后更新本文件，并另写一份带日期的 `docs/handoff-*.md`。

## 项目一页纸

go-nx：Windows 10/11 上的轻量局域网远程控制。纯 Go、无 cgo、单个静态 exe（`build/go-nx.exe`，windows/amd64，GUI 子系统）。
既是被控端（共享本机屏幕）也是控制端；QUIC（`quic-go`）+ TLS 1.3，TOFU 配对；托盘（`fyne.io/systray`）+ Ebiten 窗口；关闭窗口只是藏进托盘。
配置、身份、信任列表、日志都在 `%AppData%\go-nx\`（`go-nx.log`）。单实例用命名互斥量 `Local\go-nx-single-instance`。
仓库：`git@github.com:orangepro2001/go-nx.git`，本地 `main`。设计文档在 `docs/`（`minimal-plan.md` 是定稿范围）。

## 当前状态（2026-10-28）

- **线上最新是 0.5.0（versionCode 500，`go-nx-0.5.0-win-x64.exe`，18.5 MiB，sha256 `552708ee…fe41e`）**。0.1.0–0.4.0 仍在列表里。0.5.0 在 0.4.0 之上加了：多显示器与分辨率变化、帧内指针（按需）、确定的释放顺序 / Win+L 默认拒绝 / 逐像素精确的绝对坐标、异步发送队列与关闭时输入先到主机、更新器加固。发布后用真实更新器代码、默认地址和内置公钥独立验证过（假装 0.4.0 → 下载 19,418,624 字节、sha256 与本地一致；假装 0.5.0 → 最新）。
- **路线图阶段 0–4、6、7 与回移 B0–B5 全部完成**（见 [`ROADMAP.md`](ROADMAP.md)、[`backport-ledger.md`](backport-ledger.md)）；**阶段 5** 只差用户在自己的 PC 上做 H6 的 A/B（开关 `-layered=false` 已在，默认值未变）。**U0 技术验证已完成**（[`ui-baseline.md`](ui-baseline.md)），它改了界面方案的内存预算与字体策略；**界面代码仍然没有**（用户要求 UI 阶段只写文档），等用户明确说开始 U1。
- 本次任务的完整交接在 [`handoff-2026-10-28-pre-ui-work.md`](handoff-2026-10-28-pre-ui-work.md)：做了什么、怎么验证的、**没验证什么**、需要你在自己 PC 上做的清单（`ACCEPTANCE.md` H1–H29，⚠ 的会改系统状态）、遗留。
- 没有验证、要小心：**用户 PC 上的任何一项真实行为**（主页 / 严格模式 / 更新按钮的绘制和点击、真实 GDI 抓屏与 `SendInput`、多显示器、改分辨率、指针闪烁、更新替换并重启、异步发送队列的手感）；AAR 的完整构建（本机没有 Android SDK/NDK）。CI 的 `windows-latest` 只证明协议、注入与抓屏的字节级行为、改名运行中的 exe、`nx-host` 的完整流程、Ebiten + DirectX 软件渲染下的 U0。
- 所有提交已推送到 `origin main`；CI（linux + windows-latest）在 Release 提交 `0b778df` 上通过。
- 全局 git 身份：`orangepro2001 <yangziyi0801@gmail.com>`。`98bda14` 的作者是 `tony`，没有改写。

## 工作约束（用户 2026-10-28 明确）

- **本仓库每完成一项可交付的工作就自动 commit + push 到 `origin main`**，无需询问。先跑完检查，通过才提交；只按文件名暂存，不用 `git add -A`，不 amend / reset / force push。
- 这条授权**不含**：打 tag / GitHub Release、装依赖、改认证、公钥轮换——这些仍需单独问。（发新版本由下一条覆盖。）
- **不要改动 NightDesk 侧任何源码**（`/home/tony/dev/nightdesk-*`、`/home/tony/dev/native` 等）：那个项目不是最新版本，只读，当回移参考；改它对 go-nx 的依赖留到以后。
- **每完成一项改变程序行为的改动就发布一个新版本**（用户 2026-10-28 要求）：先提交并推送，再在干净树上 `scripts/ship.sh --bump patch|minor "说明"`（它会等 CI 绿才发布并验证）。只改文档 / CI / 测试的提交不发。`--dry-run` 只看计划。
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
scripts/ship.sh --bump patch "改了什么"   # 每次改动的发布：升版本 → 推送 → 等 CI 绿 → release.sh → 验证
NOTES="改了什么" ./release.sh  # 只发布当前 VERSION；需要干净工作区；ALLOW_DIRTY=1 可跳过
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
| `docs/release.md` | 发布流程（含每次改动的 `ship.sh`）、更新入口在哪、客户端行为、公钥轮换、测试 |
| `scripts/ship.sh` | 一条命令发布一次改动：预检线上版本 → 升 `VERSION` → 提交推送 → 等 CI → 发布 → 验证 |
| `docs/ROADMAP.md` | 开发路线图与各阶段状态（**先读**） |
| `docs/ui-redesign.md` | UI 重设计方案 v2（只有设计，没有代码）：已定 = 效率优先、蓝色、中 / 英 / 韩三语；含技术栈决策、三语字体与输入法、设计令牌、界面、性能预算、U0–U6 计划、第 13 节待定问题 |
| `docs/IMPLEMENTED.md` | 实现状态，以代码为准，带文件:行；设计文档只是意图 |
| `docs/ACCEPTANCE.md` | 已验证 / 未验证；CI runner 实测结果；人工实机清单 H1–H12 |
| `docs/review-2026-10-28-nightdesk-integration-notes.md` | 对 NightDesk 集成经验文档的逐条核对与发现的缺陷 |
| `.github/workflows/ci.yml` | linux（gofmt / vet / -race / 可移植核心交叉编译）+ windows-latest（build / test / 桌面冒烟） |
| `internal/{capture,inject}/*_windows_test.go` | 真实桌面冒烟；需 `GO_NX_WINDOWS_E2E=1`，会画窗口、动光标，**别在日常 PC 上开** |
| `README.md` | 简介、限制、构建与运行 |
| `internal/hostd/`、`cmd/nx-host/` | 无界面主机（sidecar）：JSON Lines 控制接口，契约 `docs/nx-host.md`；`-test-mode` 在任何机器上安全 |
| `internal/{synth,logsink,probe}/`、`cmd/nx-probe/` | 合成画面（五种场景 + 四角颜色标记）、只记录输入的注入器、验收探针（固定指纹连接、核对回执与画面） |
| `mobile/`、`scripts/build-aar.sh` | gomobile 的手机端核心，契约 `docs/mobile.md`；`mobile/api.golden` 固定 Java API；`--check-api` 不需要 SDK/NDK |
| `tools/u0/`、`docs/ui-baseline.md` | U0 技术验证：`go run ./tools/u0 all`（Windows 直接跑；Linux 用 `xvfb-run -a`）；结果与对计划的影响见 `ui-baseline.md`；CI 的 linux 与 windows job 每次都跑并保留报告 |
| `tools/perf/`、`docs/perf.md`、`internal/netem/` | 合成画面上的每帧成本与弱网（1 Mbit/s、80 ms、1% 丢包）验收；`netem` 是纯 Go 的用户态 UDP 代理 |
| `scripts/artifacts.sh`、`docs/compat-matrix.md` | 构建全部程序、写哈希与清单、验证可复现；版本 ↔ 协议 ↔ 契约 |
| `scripts/e2e-host-probe.py` | 把真实的 `nx-host` 与 `nx-probe` 作为独立进程跑一遍（CI 的 Linux 与 Windows 都跑） |
| `docs/backport-ledger.md` | NightDesk → go-nx 的回移台账（B0–B5 全部完成） |

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

1. **阶段 2 协议规范与版本协商**（下一个，等用户对 ALPN 名称、帧格式和能力位的意见）。其中包含兼容性修复：NightDesk 的 `MsgText=4` / `KindWheelH=7` 现在会让 go-nx 主机直接断会话。随后是阶段 3（回移 NightDesk 修复，来源只读：`/home/tony/dev/nightdesk-native-ux-20261028`）。
2. UI 重设计：**用户要求 UI 阶段只写文档**，不要开始实现；等用户对 `ui-redesign.md` 第 13 节三个问题的回复（哪些 B 层功能要做、阶段 2 是否排在 UI 实现之前、何时启动 U0）。
3. 仍待用户在 Windows 上做：`ACCEPTANCE.md` §3 的 H1–H19（自更新 H1–H3、主页更新按钮 H18、阶段 1 与模式按钮的新界面 H13–H17、H19）。
4. 阶段 1 已完成（密码哈希 + DPAPI 经评估暂缓，理由见 `ROADMAP.md`「暂缓项」）。
5. 可选：go-nx 专用 CI token；Windows 代码签名；把签名私钥备份另存一份到本机之外；逐设备配对列表（属于 UI 重设计的一部分）。

## 最近一份交接文档

[`docs/handoff-2026-10-28-ui-plan-v2.md`](handoff-2026-10-28-ui-plan-v2.md)：UI 方案 v2（效率优先、蓝色、中 / 英 / 韩三语），含对 v1 的更正和这次从源码查实的事实。
更早：[`docs/handoff-2026-10-28-mode-button-ui-plan.md`](handoff-2026-10-28-mode-button-ui-plan.md)（模式按钮 0.3.0 与 UI 方案 v1）。
更早：[`docs/handoff-2026-10-28-release-0.2.0.md`](handoff-2026-10-28-release-0.2.0.md)：回答"有没有自动推送更新"、主页更新按钮、`ship.sh`、0.2.0 的发布与独立验证。
更早：[`docs/handoff-2026-10-28-phase1-security-baseline.md`](handoff-2026-10-28-phase1-security-baseline.md)：阶段 1（安全基线）的成果、行为变更（发布前要写进说明）、验证、问题、取舍、经验。
更早：[`docs/handoff-2026-10-28-phase0-baseline.md`](handoff-2026-10-28-phase0-baseline.md)（阶段 0）、[`docs/handoff-2026-10-28-release-publish.md`](handoff-2026-10-28-release-publish.md)（接入 release-publish）。
