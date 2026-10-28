# 交接：配对码 / 密码总被拒（2026-10-28 晚）

## 用户报告

> 新版本后感觉这个配对很难成功，配对码也试了，密码也试了，就是被拒绝连接

截图：控制端（0.7.0 新界面）连 `100.109.77.29:7000`（tailnet 里的 `minipc-xoh4t`，Windows）后，卡片显示"已断开 · 已断开：密码或配对码错误。"，只有"重试""关闭"两个按钮。

## 查到的事实

- 这句话对应主机的 `CloseBadPassword`（code 2）：握手和 Hello 都成功了，主机的 `trust.Policy` 判定"有密码 / 码，但不对"。
  所以主机收到了一个**非空且不匹配**的字符串；而在配对没开、主机也没设密码时，结果会是"未配对"（code 1），不是这句。
  → 那一刻要么主机设了密码，要么配对窗口开着。
- 协议与策略本身没问题：`internal/remote` 的 `TestPairingCodeEndToEnd` 在真实 QUIC 上走通"无码拒、错码拒、对码配对、之后免密"。
- **没拿到两台机器的日志**（Windows 机器没有 SSH，也没有在它们上面的会话），所以根因没在真机上确认。下面是代码里能让"正确的码 / 密码"被发错或判错的几处，都修了：
  1. **配对码显示成 `123 456`**（新界面 `groupCode`，0.6.0 显示的是 `123456`），照着打会带空格；主机做的是逐字节比较 → 错。
  2. **错误卡片的"重试"沿用发错的那个密码**，卡片上没有地方改；从已保存设备连时用的是保存时的密码（可能是早已失效的旧码）。连续"重试"只会一直发同一个错的值，还会累计失败次数（同一来源 5 次锁 1 分钟，全体 10 次错码关掉配对窗口）。
  3. **密码框全是圆点，没法看自己打了什么**；主机"本机"页上的主机密码也被遮住（0.6.0 的"My password"是明文），看不到主机密码到底是什么。0.6.0 的首页默认焦点就在主机密码框上，当时随手打字会改掉主机密码。
  4. **配对窗口只有 2 分钟**，而新界面第一次连主机要先核对 ID，再输码；超时后码失效，主机若设了密码就会报"密码或配对码错误"。
  5. 托盘里配对进行中再点"配对新设备"会**换一个新码**，旧码立即失效。
  6. 主机日志只写 `refused <addr>: wrong password`，看不出是码过期、码打错还是密码不对。

## 改了什么（0.7.1 / core 0.2.1）

| 改动 | 文件 |
|---|---|
| 配对码比较忽略空格、短横，全角数字当半角（只对码；主机密码仍逐字比较） | `internal/trust/policy.go`（`codeMatches`、`asCode`） |
| `Policy.Explain`：被拒时日志写明对方发来的是 6 位码 / 密码（只写长度）/ 什么都没发，配对开着还剩多久 / 过期了多久 / 没开，主机是否设了密码；不含任何秘密 | `internal/trust/policy.go`，`cmd/go-nx/host_windows.go`（`check`），`internal/hostd/hostd.go`（`check`） |
| 配对窗口 2 → 5 分钟（猜码受"错 10 次即关窗"限制，与时长无关） | `cmd/go-nx/host_windows.go`（`pairingWindow`），`KeyPairStart` 三语 |
| 托盘：配对进行中再点只把页面调出来，不换码 | `cmd/go-nx/tray_windows.go` |
| 被拒（未配对 / 密码错 / 被锁 / 被移出配对 / 主机转严格）时错误卡片多一个"密码或配对码"输入框，带焦点，回车或"重试"发的是这里的值；"密码错"时先清空（已知是错的），其它情况保留原值；下面一行说明"配对码只能用一次、5 分钟内有效；密码区分大小写" | `internal/app/{tabs,errors,session_ui}.go`（`askAgain`、`refusedAuth`、`refusedCode`） |
| 所有密码框（快速连接、设备对话框、错误卡片、本机密码）右侧加"显示 / 隐藏"；密码框无论显示与否都不启用输入法 | `internal/app/build.go`（`secretField`），`pages_devices.go`，`pages_pc.go` |
| 预览场景 `error-refused`（布局测试覆盖四种语言 × 四种尺寸） | `internal/app/preview.go` |

## 验证

- 新测试：`TestPairingCodeIsReadTheWayItIsShown`、`TestExplainTellsAnExpiredCodeFromAWrongOne`（trust）；`TestARefusedPasswordIsAskedForAgainNotResent`、`TestWhichFailuresAskForAPassword`、`TestPasswordFieldsCanShowWhatWasTyped`（app）。
- 变异检查：把 `askAgain` 关掉后，`TestARefusedPasswordIsAskedForAgainNotResent` 失败，失败时画面上的文字正好是用户截图里的那张卡片（只有重试 / 关闭）。
- `gofmt`、`go vet`（Linux 与 `GOOS=windows`）、`xvfb-run -a go test -race ./...` 全绿；`tools/uipreview` 看过 `error-refused`、`devices`、`this-pc`、`device-dialog` 的中 / 韩截图。
- **没有**：在用户的两台 Windows 上复现或确认根因 → `ACCEPTANCE.md` H35。

## 给用户的下一步

两台都更新到 0.7.1（主机 minipc 和控制端都要更新：码的容错和日志在主机那边）。还被拒的话，在**主机**上按 F3 打开日志，把 `refused ... wrong password (...)` 那一行括号里的内容发过来，就能看出是哪一种。

## 经验

- 遮住的输入框 + 不能编辑的"重试" = 用户无法自己纠错；任何"被拒"类错误都要给出改输入的地方。
- 显示格式（分组空格）和比较格式不一致时，要么显示原样，要么比较时容错。
- 只写"被拒"的日志在远程排查时没用；要写出判定所依据的状态（但不写秘密）。
