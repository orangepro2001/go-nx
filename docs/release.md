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
- 下载到 `go-nx.exe.new`（与 exe 同目录，保证同卷改名），支持断点续传。
- 用户点击托盘的 `Restart to update to vX` 后：`go-nx.exe → go-nx.exe.old`，`.new → go-nx.exe`（第二步失败会回滚第一步），再用 `-restarted` 启动新进程并退出旧进程。下次启动时清理 `.old`。
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

`internal/relhub/{relproto,relclient}` 是从 `/home/tony/dev/release-publish/pkg/` 复制来的（仅非测试文件，import 路径改为 `go-nx/internal/relhub/...`）。
不用 `replace` 指向本机源码，是因为 go-nx 有 GitHub 远端，别处 clone 后会因为找不到那个路径而无法构建。SDK 升级时需要手动同步，见 `internal/relhub/README.md`。
