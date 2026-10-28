# nx-host：无界面的主机（sidecar）

`nx-host` 是 `internal/hostd` 外面的一层进程壳：和带窗口的 go-nx 一样的 QUIC 会话、配对和信任规则，但没有窗口——由另一个程序（比如 NightDesk 的 WPF 界面）把它作为子进程启动，用 **stdin / stdout 上的 JSON Lines** 控制它。它自己不碰显示器拓扑。

**这份文档是控制接口的契约**：`internal/hostd/events.go`、`control.go` 的实现以它为准，`scripts/e2e-host-probe.py` 和 `hostd` 的测试按它检查。

## 启动

```
nx-host -listen 127.0.0.1:7000 -state-dir <目录> [-pair] [-no-auth] [-fps 10] [-boost 30] [-display primary] ...
nx-host -test-mode -listen 127.0.0.1:0 -size 640x480        # 合成画面 + 只记录输入，任何机器上都安全
```

| 参数 | 默认 | 含义 |
|---|---|---|
| `-listen` | `127.0.0.1:7000` | UDP 监听地址。**默认只听本机回环**；要让别的机器连，显式写 `0.0.0.0:7000`。端口写 `0` 由系统选，实际地址在 `ready.listen` |
| `-state-dir` | 每用户目录（Windows：`%LOCALAPPDATA%\go-nx\nx-host`）；`-test-mode` 下是用完即删的临时目录 | 身份（`identity.pem`）和已配对设备（`trusted.json`）。**身份变了，所有查看器的固定都会失效** |
| `-display` | `primary` | 共享哪块显示器：`primary`、序号或 `\\.\DISPLAY2` |
| `-layered` | `true` | 抓屏是否包含分层窗口（提示、菜单） |
| `-fps` / `-boost` | `10` / `30` | 空闲时的抓屏帧率 / 查看器在操作时的帧率（最近一次输入后 2 s 内）。`-boost 0` 关闭 |
| `-pair` | 关 | 启动后立刻开一个配对窗口（等同于发 `pair`） |
| `-no-auth` | 关 | 开放模式：任何能连上的人都放行。**只用于开发**；会发 `warning` 事件 |
| `-allow-win-l` | 关 | 允许查看器按 Win+L（会锁定主机，而查看器无法解锁） |
| `-control` | `stdio` | `stdio`：stdin/stdout 的 JSON Lines；`none`：控制台主机，用人读的句子写 stderr，stdout 为空 |
| `-test-mode` | 关 | 合成画面 + 只记录输入，**永远不碰桌面**。等价于 `-source pattern -inject log`；与别的取值同用是错误 |
| `-source` / `-inject` | `gdi` / `sendinput`（test-mode 下 `pattern` / `log`） | `pattern` 和 `log` 只能在 `-test-mode` 下用——否则一个看起来是真主机的进程其实是假的 |
| `-scene`、`-size` | `blocks`、`1920x1080` | 合成画面的场景（`blocks` `static` `docscroll` `terminal` `video`）和尺寸 |
| `-write-timeout` | `30s` | 控制端多久不读事件就被当作已经离开（见"控制端消失"） |
| `-version` | | 打印 `nx-host <版本>` 后退出 |

真实抓屏和输入**只在 Windows 上有**；别的系统没有 `-test-mode` 会得到一个说明这一点的 `error` 事件，而不是假装工作。

退出码：`0` 正常停止；`2` 启动失败（先发一个 `error` 事件）；`3` 控制端不再读事件。

## 线路格式

- stdin：一行一个 JSON 对象，是**命令**。stdout：一行一个 JSON 对象，是**事件**。stderr 是给人看的日志，**不要解析**。
- 一行最长 **64 KiB**；更长的行被丢弃，回一个 `line-too-long` 错误，然后继续读下一行。空行忽略。
- 只有本文档列出的字段有意义；**不认识的字段忽略**。新增命令、事件或字段**不改** `schema`；只有删除名字或改变含义才升 `schema`。控制端不要依赖字段顺序。
- 命令可带 `"schema": N`（控制端懂的最高版本，可省略）；主机的版本更低则回 `unsupported-schema`。
- 命令可带 `"id"`（字符串或数字，最长 64 字节）。**对这条命令的回答**带同样的 `"id"`（数字也以字符串回显，`7` → `"7"`）。没有 `id` 的命令，回答也没有。由命令引起的**异步**事件（`connected`、`disconnected`、`denied`）不带 `id`。

## 生命周期

```
ready  [warning]  ...命令与事件...  stopped
```

`ready` 一定是第一个事件，`stopped` 一定是最后一个（除非控制端已经消失）。命令在 `ready` 之后才会被处理。

主机停止的原因（`stopped.reason`）：`stop`（收到 `stop` 命令）、`controller-closed`（stdin 到了结尾——**控制端关了管道就是它走了，主机随之退出**）、`interrupted`（Ctrl+C / SIGTERM）、`shutdown`。

## 命令

| `cmd` | 参数 | 回答 |
|---|---|---|
| `status` | | `status` |
| `pair` | `seconds`（1–3600，默认 300） | `pairing`，带 `code`（6 位）和 `expiresIn`。配对窗口一次一个码；码在成功配对一台设备或到期后失效 |
| `pair-cancel` | | `pairing`（`expiresIn: 0`，没有 `code`） |
| `untrust` | `fingerprint`（64 位十六进制，大小写不拘） | `status`；该设备若正连着就被断开（`disconnected.reason = revoked`）。指纹不在已配对列表里：`error`（`not-found`） |
| `untrust-all` | | `status`；断开当前控制端 |
| `fps` | `value`（1–60） | `status` |
| `mode` | `mode`：`strict` 或 `open` | `status`，带 `dropped`：切到 `strict` 时被结束的、因开放模式才放行的会话数（已配对或已验证的设备不受影响） |
| `stop` | | `ack`，然后 `stopped` |

## 事件

所有事件都有 `event`。其余字段按事件而定（没列出的不会出现）。

| `event` | 字段 |
|---|---|
| `ready` | `schema`、`version`、`pid`、`listen`（**实际绑定的地址**）、`display`、`width`、`height`、`dpi`、`identity`（主机指纹，查看器要固定它）、`fps`、`requireAuth`（`true`＝严格模式）、`paired`（已配对设备数）、`testMode`、`source`、`inputMode` |
| `warning` | `message`（给人看）。比如测试模式、开放模式、配对文件损坏被收起来 |
| `pairing` | `code`、`expiresIn`（秒；`0` 表示窗口已关闭） |
| `connected` | `addr`、`identity`（查看器指纹）、`name`（查看器自报的名字，v0 查看器为空）、`protocol`（0 或 1） |
| `disconnected` | `addr`、`reason`、`message`。`reason`：`closed`（对方正常结束）、`replaced`（被新查看器顶替）、`revoked`（被 `untrust`）、`unverified`（切到严格模式）、`timeout`（对方静默过久）、`error`（`message` 有说明） |
| `denied` | `addr`、`reason`（`untrusted`、`bad-code`、`locked`）、`identity`（被拒者的指纹） |
| `status` | `fps`、`trusted`（已配对设备指纹，排序）、`requireAuth`、`listen`、`display`、`width`、`height`、`identity`、`addr`（当前控制端，没有则无）、`source`、`inputMode`、`testMode`、`frames`、`inputs`（累计）、`uptime`（秒）、`dropped`（仅 `mode` 的回答） |
| `ack` | 无（`stop` 的回答） |
| `error` | `errorCode`、`message`。`errorCode`：`bad-json`、`bad-command`、`unknown-command`、`bad-argument`、`unsupported-schema`、`line-too-long`、`not-found`、`internal`、`startup`（启动失败，之后进程退出码 2） |

`errorCode` 是稳定的词，`message` 是给人看的句子，**不要解析 `message`**。命令出错**不会**停止主机。

## 控制端消失

事件由单独一个 goroutine 写 stdout，主机的其他部分只是把事件排进队列（容量 1024），**不会被慢的控制端拖住**。如果一次写 stdout 超过 `-write-timeout` 仍不返回（管道满了而没人读），或者写失败（管道断了），主机就当控制端已经没了：往 stderr 写一行说明、停止、**退出码 3**。一个活着却不再读事件的控制端会留下一个没人能停的主机——这是不允许的。

## 用 nx-probe 验证

```
nx-probe -addr <ready.listen> -fingerprint <ready.identity> -code <pairing.code> -input -markers -json
```

`nx-probe` 用固定指纹的方式连接（**指纹不符在握手时就拒绝，配对码不会发给错的主机**）、收画面、对每种输入要回执。`-markers` 检查合成主机画面四角的颜色标记，能认出红蓝颜色顺序错、上下翻转和左右镜像。它**只证明协议路径**，不证明真实抓屏 / `SendInput`——那需要真实桌面（`ACCEPTANCE.md`）。

`scripts/e2e-host-probe.py --host <nx-host> --probe <nx-probe>` 把两者作为独立进程跑一遍完整流程（配对、v1 与 v0 连接、错误指纹、错误码、状态、停止、退出码、stdout 全是合法事件），CI 在 Linux 和 Windows 上都跑它。

## 与 NightDesk 快照里的 nx-host 的差异

go-nx 是上游；以下是将来 NightDesk 改用这个 nx-host 时要知道的（NightDesk 现在没有改动）：

- `ready` 带 `schema`；`listen` 是**实际绑定的地址**（NightDesk 回显命令行参数，用 `:0` 时拿不到端口）。
- 所有回答回显 `id`；新增 `untrust-all`、`mode`；`untrust` 对未知指纹回 `not-found`（原来静默成功）。
- `connected` 增加 `name`、`protocol`；`denied` 增加 `identity`；`disconnected.reason` 是稳定的词（原来是错误文本）。
- 过长的命令行被跳过并报错（原来 bufio.Scanner 遇到会停止读取，主机随后把控制端当作消失而退出）。
- 控制端不读事件时主机退出码 3（原来会在写 stdout 处永远卡住）。
- 默认 `-boost 30` 复现 NightDesk 固定的 30 fps 交互帧率，现在由 `remote.Boost` 实现。
- 真实抓屏沿用 go-nx 的 `capture.Open`：每秒检查显示器变化并跟随分辨率变化，不再用"拓扑变了就失败"的包装。
