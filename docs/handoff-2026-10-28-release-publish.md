# 交接：接入本机 release-publish（发布端 + 应用内自更新）

- 时间：2026-10-28（KST），线上发布完成于约 11:15
- 任务：把 go-nx 接入本机 `release-publish` 服务——一条命令发布，应用内可自更新
- 提交：`98bda14`（本地，**未 push**）。该提交的作者是 `tony <yangziyi0801@gmail.com>`，因为当时本机没有 git 身份，我用一次性 `-c` 沿用了上一个提交的身份；之后你把全局身份设成了 `orangepro2001`，**没有改写**这个提交（规则禁止 amend）。
- 本文件和 `docs/context-recovery.md` 写于提交之后，**尚未提交**。

## 1. 成果

| 项 | 状态 |
|---|---|
| `VERSION` + `build.sh` 注入 `main.version` | 完成，已验证 |
| `release.sh`（干净树检查 → build → 首次建 app → publish） | 完成，已在线上实跑 |
| `internal/relhub/`（SDK 副本） | 完成 |
| `internal/update/`（检查、下载、校验、替换、回滚） | 完成，13 个单测 + 端到端 |
| `cmd/go-nx` 接入（托盘入口、标志位、单实例重试） | 完成，**仅交叉编译 + vet，未在 Windows 实机运行** |
| 线上 release-publish：app `go-nx`，win-x64，**0.1.0（versionCode 100）已发布** | 完成并验证 |
| `docs/release.md`、`internal/relhub/README.md` | 完成 |

线上发布物：`go-nx-0.1.0-win-x64.exe`，19,219,456 字节，sha256 `6f966cdec48db6f4873600acd32a01642c94df13b76675a46947c08d6d0ea8a1`，渠道 stable，最低版本 0，签名 keyId `f5faa9f9858b789f`。

### 改动文件

- 新增：`VERSION`、`release.sh`、`scripts/e2e-update.sh`、`cmd/go-nx/update_windows.go`、`internal/update/{update,update_test,e2e_test}.go`、`internal/relhub/{README.md,relproto/*,relclient/*}`、`docs/release.md`
- 修改：`build.sh`（读 VERSION、`-X main.version`）、`cmd/go-nx/main_windows.go`（三个标志位、`startUpdates`）、`cmd/go-nx/tray_windows.go`（版本项 + 更新入口）、`cmd/go-nx/winapi_windows.go`（`alreadyRunning(wait)` 带重试）

## 2. 验证（实际跑过的）

- `./build.sh`：`go vet ./...`、`GOOS=windows go vet ./...`、`go test ./...` 全绿，产出 `build/go-nx.exe`（19M，PE32+ GUI x86-64）。
- `go test -race ./internal/update/`：13 个用例通过——有更新并安装、强制更新（签名 min 高于当前）、已最新、线上无发布、**换了另一把密钥签名被拒**、**下载内容被篡改被丢弃且 `.new`/`.part` 都不残留**、服务不可达、`Run` 延迟触发且 `CheckNow` 在已就绪时不动作、`Replace` 在新文件缺失时回滚、`Replace` 覆盖陈旧的 `.old`、标签文案、`dev` 版本被拒、内置公钥可解析且 keyId 为 `f5faa9f9858b789f`。
- `scripts/e2e-update.sh`：真实 `relhub` 二进制，临时数据目录 + 内核分配的回环端口，发布 0.1.0 / 0.1.1；`relhub check` 在 code 100 返回 10、code 101 返回 0；更新器三个子场景通过（0.1.0→0.1.1 并安装、已在 0.1.1、错误公钥拒绝真实服务）。跑完后确认没有残留进程，线上服务当时仍为空。
- 版本注入：用 `-X main.version=9.8.7` 在草稿目录单独构建，`9.8.7` 在产物中出现 1 次，不带 `-X` 的 dev 构建为 0 次；正式产物含内置公钥。
- 线上：`relhub check` 在 code 0 返回 10、code 100 返回 0；经 `https://tony-ai-pc-1.tail9f7f1f.ts.net/release` 取 manifest，keyId 与内置公钥一致；经 tailnet 下载的文件 sha256 与本地 `build/go-nx.exe` 一致；用代码里**默认的 BaseURL 和 PinnedKeys**（不覆盖任何配置）跑了一次探针：从 0.0.1 检查到 0.1.0 并下载校验成功、字节与 `build/go-nx.exe` 相同，从 0.1.0 检查为 Current。探针是临时文件，已删除。
- 没有改动任何 tailscale serve 路径（`/release` 本来就在）；线上 `GET /v1/apps` 现在只有 `go-nx`。

### 没有验证的（需要你在 Windows 上做）

1. 托盘菜单是否按预期显示：顶部禁用的 `go-nx v0.1.0`，以及随状态变化的更新入口。
2. 点击 `Restart to update to vX` 后：exe 改名为 `.old`、新版启动、**不弹"已在运行"**、`.old` 在下次启动后消失。
3. 假设"运行中的 exe 在 Windows 上能改名、改名后能在原路径放新文件"——这是 SDK 文档 §13.1 的通用说法，作者也标注"未在 Windows 实机验证"，我没有 Windows 环境验证。
4. 假设"改名后 `os.Executable()` 可能指向 `.old`"——所以重启用的是启动时记下的路径（`Updater.Exe()`）。这个假设没有验证，但这样做在两种情况下都正确。
5. 安装在只读目录（如 Program Files）时，下载到 `exe.new` 会失败；此时托盘显示 `Update check failed - retry`，原因只在日志 `%AppData%\go-nx\go-nx.log` 的 `update:` 行里。

## 3. 遇到的问题与处理

| 问题 | 处理 |
|---|---|
| 第一版 `checkOnce` 里写了 `relproto.AsError(err).Code == ... && err != nil`；`AsError(nil)` 返回 nil，会空指针 | 自查发现，调整为先判 `err != nil`；单测 `TestNothingPublishedIsNotAnError` 覆盖该分支 |
| 单实例互斥量的重试陷阱：失败的 `CreateMutex` 会拿到指向旧进程互斥量的句柄，不关闭的话旧进程退出后互斥量仍被新进程的句柄撑住，重试永远失败 | `alreadyRunning(wait)` 每次失败后 `CloseHandle` 再重试，只在 `-restarted` 时等最多 10 秒 |
| 内置公钥最初是单个常量，无法"新旧两把都信任"，轮换会让旧客户端永久拒绝更新 | 改成 `PinnedKeys []string`，轮换流程写进 `docs/release.md` |
| 提交失败：本机无 git 身份（`Author identity unknown`） | 查上一个提交的身份，用一次性 `-c user.name/email` 提交，没写任何配置；之后你设置了全局身份 |
| `strings` 对 Go 产物没用（字符串不以 NUL 分隔）；`go version -m` 也没列出 ldflags | 改用 `9.8.7` 探针构建对照 |
| 用 Python `replace` 改文件时，找不到匹配会**静默不改** | 每次改完用 `git diff` 逐行核对，确认全部落地 |
| zsh 下 `grep --include` 报 "no matches found"，`PIPESTATUS` 取不到 | 换成不带通配的写法；不再引用 `PIPESTATUS` |
| 测试名 `...CheckNowRetries` 夸大了实际覆盖（只测了"已就绪时 CheckNow 不动作"） | 改名为 `...CheckNowIsNoOpWhenReady` |

## 4. 设计思路与取舍

- **复制 SDK，而不是 `replace => /home/tony/dev/release-publish`**：go-nx 有 GitHub 远端，`replace` 指向本机绝对路径，别处 clone 就构建不了。SDK 只依赖标准库，复制后 `go.mod` 不变。代价是升级 SDK 要手动同步（`internal/relhub/README.md` 写了怎么做）。测试文件没复制，因为它们依赖服务端 `internal/` 包。
- **周期 `Check`，不用 `WatchLoop` 长轮询**：对桌面小工具来说，6 小时一次足够，也不用常驻一条 50 秒的长连接，错误处理更简单。
- **不自动重启**：重启会断开正在进行的远控会话，所以只在用户点击托盘后才替换并重启；强制更新也一样，只是标签加 `(required)`。
- **下载目标放在 exe 同目录**（`go-nx.exe.new`）：Windows 上 `os.Rename` 跨卷会失败，放同目录保证同卷。SDK 自带 `.part` 续传和 SHA-256 校验。
- **只信签名后的 manifest**：`updateAvailable`、`mandatory` 是未签名字段，SDK 的 `settle` 会按签名内容本地重算，我没有再另外相信它们。
- **发布物是裸 exe，不打 zip**：单个静态 exe，省掉解压和半更新的风险。服务端默认文件名是 `<app>-<version>-<platform><ext>`（即 `go-nx-0.1.0-win-x64.exe`），下载目标由调用方指定，不受影响。
- **`release.sh` 先 `relhub app show` 再决定是否 `app create`**：比吞掉冲突错误更干净，服务挂了或没 token 时会直接报错而不是被掩盖。
- **`release.sh` 要求干净工作区**：保证发布的 exe 对应一个提交。

## 5. 经验与坑

- versionCode 由版本号推导：`major*10000 + minor*100 + patch`，minor 和 patch 必须小于 100；同平台内必须严格递增，否则 409。**发出去的版本不能覆盖**，发错了只能递增重发，必要时 `relhub yank`。
- 服务端没发布任何版本时，`Check` 返回 `not_found`，这不是故障，更新器把它当作"已是最新"。
- 公钥轮换后服务端**只用新密钥重签**，只认旧公钥的客户端会永久拒绝更新。必须先发布一个同时信任新旧两把公钥的版本，等客户端都升级后再切换线上密钥（详见 `docs/release.md` 和 release-publish 的 `docs/USAGE.md` 第 8 节）。
- 验证"某个编译期变量真的被注入了"，用一个不可能撞车的值（`9.8.7`）做对照构建，比在 Go 二进制里 `strings` 靠谱。
- 做端到端测试时一定要用 `RELHUB_DATA` 指向临时目录，否则 `relhub` 会自动读线上的 `admin.token` 并连到线上服务。
- 这个仓库没有 README、没有 CI。中文设计文档在 `docs/`，代码注释是英文。

## 6. 未完成项与下一步

1. **Windows 实机验证**（见上文"没有验证的"）：手动装 0.1.0 → 发布 0.1.1 → 托盘点检查 → 点重启。建议发布 0.1.1 时 `NOTES` 写明是更新测试。
2. **已经装了的旧版 go-nx 没有更新器**，需要手动装一次 0.1.0。
3. **提交这两份文档**（目前未提交）；`git push` 需要你单独授权，仓库远端是 `git@github.com:orangepro2001/go-nx.git`。
4. 可选：给 go-nx 单独发一个 CI token（`relhub token create -name ci-go-nx -app go-nx`），以后 `release.sh` 不必依赖本机 `admin.token`。
5. 可选：在窗口主页也显示更新提示（目前只在托盘）；失败原因目前只写日志，托盘只显示"失败 - 重试"。
6. 可选：Windows 代码签名。目前 exe 没有签名，SmartScreen 可能拦截首次运行（未验证）。
7. 备份提醒：`~/.local/share/release-publish/keys/` 里的签名私钥丢了就验不了新发布，备份在 `~/backups/release-publish-keys/`，是同一块盘上的明文备份，建议另存一份到本机之外。
