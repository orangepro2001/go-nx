# 发布与自更新（release-publish）

go-nx 通过本机的 release-publish 服务分发和更新。发布物是裸的 `go-nx.exe`（app id `go-nx`，平台 `win-x64`，渠道 `stable`）。

```
VERSION ──build.sh──▶ build/go-nx.exe (main.version 已注入)
                          │ release.sh：relhub publish
                          ▼
              release-publish (127.0.0.1:18790)
                          │ tailscale serve /release
                          ▼
   https://tony-ai-pc-1.tail9f7f1f.ts.net/release   ◀── go-nx 托盘：检查 → 下载 → 校验 → 重启
```

## 每次改动发布一个新版本（推荐）

约定（用户 2026-10-28 明确）：**每完成一项改变程序行为的改动，就发布一个新版本**；只改文档、CI、测试的提交不发（exe 没变，发了只是噪音）。
改动先照常提交并推送，然后在干净的工作区执行：

```sh
scripts/ship.sh --bump patch "这次改了什么"      # 修复用 patch；功能或行为变化用 minor
scripts/ship.sh --dry-run "..."                  # 只看会升到哪个版本、线上最新是多少，不改任何东西
```

`ship.sh` 依次：预检（在 `main`、工作区干净、不落后于远端、新 versionCode 大于线上已发布的最大值）→ 升 `VERSION` 并提交 `Release X.Y.Z` → 推送 → **等这个提交的 CI 通过**（失败或 15 分钟没结束就不发布）→ `release.sh` 构建并发布 → 用 `relhub check` 验证旧版本被提供新版、新版本是最新。
发布只是把新版本放到线上；已安装的 go-nx 只有在用户点了 `Restart to update to vX` 之后才会换成新版，不会自动重启。

## 核心文件：单独的应用 `go-nx-core`

用户 2026-10-28 决定：**核心文件作为单独的应用发布**。release-publish 里现在有两个应用：

| 应用 | 平台 | 发布物 | 版本文件 | 谁用 |
|---|---|---|---|---|
| `go-nx` | `win-x64` | 裸的 `go-nx.exe` | `VERSION` | 桌面程序，自更新 |
| `go-nx-core` | `win-x64` | `go-nx-core-<版本>-win-x64.zip` | `CORE_VERSION` | 要嵌入 go-nx 的程序（NightDesk 等），自己检查、下载、解压 |

zip 里是：`nx-host.exe`（无界面主机，契约 `docs/nx-host.md`）、`nx-probe.exe`（验收探针）、各自的 `SHA256SUMS` / `artifacts.json`（版本、提交、是否干净、Go 版本、构建参数、每个文件的大小和哈希），以及随版本的契约 `docs/nx-host.md`、`docs/protocol.md`。zip 由 `tools/mkzip` 打包：条目按名字排序、时间统一为提交时间、权限固定，**同一提交得到逐字节相同的 zip**（`scripts/artifacts.sh --verify` 验证两次空缓存构建的 exe 和 zip 都相同）。

**版本独立**：核心的版本在 `CORE_VERSION`，`nx-host`、`nx-probe` 编进的是它，不是 `VERSION`。只有核心的内容变了才发新版：`scripts/core-changed.sh` 看自上次 `Release core …` 提交以来，两个程序实际依赖的 Go 文件（`go list -deps` 得到的本模块包）、`go.mod` / `go.sum`、契约文档和构建脚本有没有变（测试文件、界面、文档以外的东西不算）。没变就不发，`ship.sh --core` 会拒绝。

发布一次核心：

```sh
scripts/ship.sh --core --bump minor "这次改了什么"   # 与 go-nx 一样：升 CORE_VERSION → 提交推送 → 等 CI 绿 → release-core.sh → 验证
scripts/core-changed.sh                              # 先看有没有变（退出码 0 变了，1 没变，2 判断不了）
```

`ship.sh`（发 go-nx）结束时，如果核心自上次发布后变了，会提示你补发一次核心。消费方怎么检查更新：`relhub check -app go-nx-core -platform win-x64 -code <已装版本的 versionCode>`（退出码 0 最新，10 有更新），或用 SDK（`docs/SDK.md`）；下载得到的 zip 已由 SDK 验过签名和 SHA-256，zip 内文件的哈希在 `SHA256SUMS`。

## 其他产物（不发布）

| 产物 | 作用 | 怎么得到 |
|---|---|---|
| `go-nx-core.aar` | 手机端核心（`mobile`），契约 `mobile.md` | `scripts/build-aar.sh`；需要 Android SDK/NDK，**本机没有，未构建过** |

`scripts/artifacts.sh` 写 `SHA256SUMS` 和 `artifacts.json`；`scripts/artifacts.sh core` 只构建核心并打 zip；`--verify` 用空缓存构建两次并比较（同一台机器上字节相同）。版本与契约的对应在 `compat-matrix.md`。

## 手动发布一个新版本

1. 改 `VERSION`（`MAJOR.MINOR.PATCH`，minor 和 patch 都必须小于 100）。
2. 提交代码。`release.sh` 默认拒绝在不干净的工作区发布，保证发布的 exe 对应一个提交（`ALLOW_DIRTY=1` 可跳过）。
3. `NOTES="这次改了什么" ./release.sh`

`release.sh` 做的事：`build.sh`（`go vet`、Windows vet、`go test`、交叉编译并注入版本）→ 首次发布时 `relhub app create` → `relhub publish`。
本机的 `relhub` 默认连 `127.0.0.1:18790` 并自动读取 `admin.token`，不需要额外配置。

### 版本号规则

服务端对非 Android 平台用 `major*10000 + minor*100 + patch` 推导 versionCode：`0.1.0 → 100`，`1.6.1 → 10601`。
同一平台内 versionCode 必须严格递增，否则服务端返回 409。已发布的版本不能覆盖，发错了请递增版本重新发布（需要时用 `relhub yank` 撤回）。

### 强制更新

`relhub channel min -app go-nx -platform win-x64 -channel stable -min <versionCode>`，或发布时加 `-force`。
低于该值的客户端托盘会显示 `Restart to update to vX (required)`。注意：go-nx 仍然**不会**自动重启，因为重启会断开正在进行的远控会话。

## 在哪里找更新入口

- **窗口主页右上角**：版本号显示在标题旁，右上角是按钮，文字随状态变化：`Check for updates` → `Checking for updates...` → `Downloading vX (n%)...` → `Up to date - check again` / `Update check failed - retry` / `Restart to update to vX`。有新版本待安装时按钮高亮，第一次点击变成 `Click again to restart now`，第二次才安装并重启。（0.2.0 起；0.1.0 的窗口里没有这个按钮。）
- **系统托盘右键菜单**：顶部灰色的 `go-nx vX.Y.Z`，下面是同样文字的更新项。Windows 11 常把托盘图标收在右下角 `^` 溢出区里。
- `dev` 构建（没经过 `build.sh`）和 `-no-update` 没有更新入口：托盘没有更新项，主页没有按钮，点原位置会提示原因。

## 客户端行为（`internal/update`、`cmd/go-nx/update_windows.go`）

- 启动 30 秒后检查一次，之后每 6 小时一次；托盘菜单或主页按钮可立即检查。用周期性 `Check`，不保持长连接。
- 只信任**签名后的 manifest**：是否有新版、是否强制，都由客户端根据签名内容本地重算；下载后校验大小和 SHA-256，不符则丢弃。
- 下载到 `go-nx.exe.new`（与 exe 同目录，保证同卷改名），支持断点续传；遗留的 `.part` 若不是这个版本的前缀，会在哈希校验时被发现、删除，下一次检查从头下载。
- 用户点击托盘的 `Restart to update to vX` 后：
  1. **安装前重新校验** `.new` 的大小和 SHA-256（下载到点击之间文件可能被改坏）；不符则删除、状态变 `Failed`，下一次检查重新下载，**不动 exe**。
  2. 两个文件都在才开始：`go-nx.exe → go-nx.exe.old`，`.new → go-nx.exe`。第二步被拒（常见于杀毒软件刚扫完 `.new`）会等一会儿重试 5 次，仍失败才回滚第一步；连回滚都失败时，错误信息里写明旧版本在哪、该改回什么名字。上一次更新留下的 `.old` 是被这次改名**顶替**的，不是先删除，所以 exe 搬不动时旧备份还在。
  3. 同一时刻只允许一次安装；成功后状态为 `Installed`（"Updated to vX - restart go-nx to use it"），再点只重启、不会再次安装，也不会因重复点击把新版本覆盖到备份上。
  4. 再用 `-restarted` 启动新进程并退出旧进程。
- **备份 `.old` 在新版本运行满首次检查延迟（30 秒）之后才删除**，而不是启动即删：新版本若在这之前就崩溃，`.old` 是回退手段。`-no-update` / `dev` 没有更新器，启动时直接清理。
- **更新被打断**（两次改名之间断电 / 进程被杀，窗口极小）：此时 `go-nx.exe` 不存在，旧版本完整地躺在 `go-nx.exe.old`。直接运行 `go-nx.exe.old`，它启动时会把自己改回 `go-nx.exe`（`update.Recover`）；也可以手动把它改回名字。拿备份副本启动时更新器不会启用（`update.New` 拒绝 `.old` / `.new` 路径），避免把更新装到错误的位置。
- 服务不可达只写日志（`%AppData%\go-nx\go-nx.log` 中的 `update:` 行），不弹窗、不影响远控。
- `dev` 构建（没有经过 `build.sh`）、`-no-update` 都不会联网。`-update-url` 可改服务地址。

### 首次安装

已经装了的旧版 go-nx 没有更新器。第一个带更新器的版本（0.1.0）需要手动拷过去装一次，之后才会自己更新。

## 公钥与轮换

服务端的 Ed25519 公钥编译在 `internal/update.PinnedKeys`（当前 keyId `f5faa9f9858b789f`）。不能运行时从同一服务取，否则恶意服务可以自己担保自己。

**轮换会让只认旧公钥的客户端永久拒绝更新**——服务端轮换后只用新密钥重签。所以顺序是：

1. 在数据目录的副本上生成新密钥，拿到新公钥（步骤见 release-publish 的 `docs/USAGE.md` 第 8 节）。
2. 把新公钥**追加**到 `PinnedKeys`，发布一个新版本（此时服务端仍用旧密钥签名，客户端能正常更新到这一版）。
3. 等客户端都更新到包含两把公钥的版本，再切换线上服务的密钥。
4. 没更新到过渡版本的客户端会卡住，只能手动重装。

签名私钥在 `~/.local/share/release-publish/keys/`，备份在 `~/backups/release-publish-keys/`。本仓库不保存任何 token 或私钥。

## 测试

- `go test ./internal/update/`：用 `httptest` 假服务和真实签名覆盖 有更新 / 已最新 / 无发布 / 签名不符 / SHA-256 不符 / 服务不可达 / 强制更新 / 替换回滚。
- `scripts/e2e-update.sh`：用真实 `relhub` 二进制在临时目录、随机回环端口起一个**独立**服务，发布 0.1.0 和 0.1.1，再让更新器跑完整流程。不碰线上服务和任何 tailscale serve 路径。
- Windows 上的托盘、替换 exe、重启和单实例互斥量，只能在 Windows 实机上验证（Linux 上只做了交叉编译和 vet）。

## SDK 副本

`internal/relhub/{relproto,relclient}` 是从 `/home/tony/dev/release-publish/pkg/` 复制来的（仅非测试文件，import 路径改为 `github.com/orangepro2001/go-nx/internal/relhub/...`）。
不用 `replace` 指向本机源码，是因为 go-nx 有 GitHub 远端，别处 clone 后会因为找不到那个路径而无法构建。SDK 升级时需要手动同步，见 `internal/relhub/README.md`。
