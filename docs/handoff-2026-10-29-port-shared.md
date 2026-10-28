# 交接：开放模式下仍被拒——tailnet 地址被另一个程序占着（2026-10-29）

接着 `handoff-2026-10-28-pairing-refused.md`。

## 用户报告

> 允许用户关掉这个限制，然后我甚至切换到开放模式了依然会被拒绝

截图：控制端连 `100.109.77.29:7000`，卡片显示"已断开：密码错误次数过多，请一分钟后再试。"，输入框里是 6 位码。
随后用户发来 minipc（`minipc-xoh4t`）上 go-nx 0.7.1"本机"页的截图，并说明那边确实是开放模式：

> mini-pc我已经确保是开放模式了，如果不一致，说明是bug

截图里：开放模式，地址 `100.109.77.29, 110.15.136.210, 192.168.163.1, 192.168.119.1, 110.15.137.26 · UDP 7000`，本机 ID **`CFFC C257 D5F1 8299`**。

## 查到的事实（从本机 tony-ai-pc-1 经 tailnet / 公网探测）

- 只做 TLS 握手、什么都不发的探测（`remote.Probe`，临时身份）：

  | 地址 | 回应的主机指纹 |
  |---|---|
  | `100.109.77.29:7000`（tailnet） | `28aff5ac a80377e7…`，即 ID **`28AF F5AC A803 77E7`** |
  | `110.15.137.26:7000`（公网） | `cffcc257 d5f18299…`，即 go-nx 的 `CFFC C257 D5F1 8299` |
  | `110.15.136.210:7000`（公网） | 同上，go-nx |

- 不带密码连一次 `100.109.77.29:7000`（`nx-probe`，无 `-code`）：被拒，关闭码 1（未配对）。开放模式下的主机对未配对设备一律放行，所以**回应 tailnet 地址的是另一个处于严格模式的主机**，不是用户看到的那个 go-nx。
- 结论：minipc 上**另一个程序单独绑定了 `100.109.77.29:7000`**（go-nx 绑定的是所有地址 `[::]:7000`）。Windows 允许这样共用端口，并把发往该地址的包交给绑定更具体的那个。于是 go-nx 里开配对、切开放模式、改密码，对 tailnet 连接全都无效；连续"重试"在那个程序上累计失败次数，被它锁定。
- 那个程序是什么还不知道。候选：NightDesk 的 nx-host 旁车（`NightDesk/src/NightDesk/Models.cs` 里 `NxHostListen` 可配，默认 `127.0.0.1:7000`，只读查看，没改）、另一个用户 / 会话里用 `-listen 100.109.77.29` 启动的 go-nx、手动启动的 `nx-host`。它的身份和 go-nx 不同，所以不是同一个配置目录下的 go-nx。
- **安全**：minipc 的 go-nx 处于开放模式，并且在公网地址上可达（上表）。等于互联网上任何能到达 `110.15.137.26:7000` 的人都能控制它。已提醒用户先切回严格模式。

## 改了什么（0.7.2）

| 改动 | 文件 |
|---|---|
| **主机检测同端口上的其他程序**：每 10 s 用 `GetExtendedUdpTable` 列出绑定同一 UDP 端口的套接字（含本地地址和进程），排除自己；有就记日志 `UDP port 7000 is also bound by nx-host.exe (process N) on 100.109.77.29: ...`，"本机"页状态卡变成警告色并写明"另一个程序也在监听 UDP 7000：…发到这些地址的连接会被它接走…这里的配对码、开放模式和密码对它们都不起作用" | `cmd/go-nx/winapi_windows.go`（`udpSockets`、`portSharers`、`processName`），`cmd/go-nx/host_windows.go`（`watchPort`、`Sharers`），`internal/app/{services,pages_pc}.go`（`HostInfo.PortShared`） |
| **可以关掉输错锁定**（用户要求）："本机"页安全卡片新增开关"输错 5 次后暂时锁定"，默认开，存在设置 `lockout`。关掉时立刻清空现有锁定，之后不再锁；配对码错 10 次仍会作废（防猜码）。主机密码仍逐字比较 | `internal/trust/policy.go`（`Lockout`、`SetLockout`），`cmd/go-nx/host_windows.go`（`SetLockout`），`internal/app/pages_pc.go` |
| **被拒卡片显示对方主机 ID**："对方 ID：28AF F5AC A803 77E7。应和被控电脑"本机"页上的 ID 一致；不一致说明在这个地址回应的是另一个程序。"这正是这次判断问题的方法 | `internal/app/session_ui.go` |
| 文案：被拒卡片说明"配对码和开放模式都要在被控电脑上设置"；"被锁"的提示改成"第一次 1 分钟，可以在主机的'本机'页关掉这个锁定"（原来说"一分钟后再试"，加倍后不准） | `internal/ui/i18n/strings.go` |
| 预览场景 `this-pc-shared`，`error-refused` 带对方 ID | `internal/app/preview.go` |
| 修了一个测试竞争：`TestTabsCanBeRenamedAndClosed` 两个标签同时拨号，`conns[0]` 不一定是第一个标签的连接（这次改动后 40 次里失败 3 次） | `internal/app/app_test.go` |

`hostd`（nx-host）没加锁定开关：它没有主机密码，锁定只影响配对码；需要时再加。

## 验证

- 新测试：`TestLockoutCanBeTurnedOff`、`TestWrongCodesStillCloseThePairingWithoutLockout`（trust）；`TestTheLockoutCanBeTurnedOffOnThisPC`、`TestAnotherProgramOnTheHostingPortIsShown`，`TestARefusedPasswordIsAskedForAgainNotResent` 加了对方 ID 的检查（app）；`TestPortSharersNamesAnotherProcess`（Windows，CI 运行器上跑：本进程像主机那样绑所有地址，另起一个进程绑 `127.0.0.1` 同端口，`portSharers` 要点名那个进程和地址、不列出自己；Windows 不允许这种顺序时换个顺序，两种都不允许就跳过）。
- `gofmt`、`go vet`（Linux 与 `GOOS=windows`）、`xvfb-run -a go test -race ./...` 全绿；`tools/uipreview` 看过 `this-pc-shared`、`error-refused` 的中 / 韩截图。
- **没有**：在 minipc 上确认那个程序是谁、0.7.2 的警告在真机上出现 → `ACCEPTANCE.md` H36。

## 给用户的下一步

1. minipc 先切回**严格模式**（公网地址可达）。
2. 在 minipc 上用 PowerShell 查谁占着 7000：
   `Get-NetUDPEndpoint -LocalPort 7000 | % { "$($_.LocalAddress) $((Get-Process -Id $_.OwningProcess).Path)" }`
   关掉那个程序（或让它换端口），tailnet 连接就会到 go-nx。
3. 或者等 minipc 自动更新到 0.7.2，"本机"页会直接写出那个程序的名字和进程号。

## 经验

- "开放模式还被拒"在策略上不可能，先怀疑**回应的不是这个进程**；对比主机 ID（握手就能拿到，不需要认证）最快。
- 程序界面说"正在共享"不代表连接能到它：同端口、更具体的绑定会截走流量，Windows 不会报错。
