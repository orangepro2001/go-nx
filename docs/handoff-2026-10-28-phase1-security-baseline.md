# 交接：阶段 1 — 安全基线

- 日期：2026-10-28（KST）
- 任务：执行 `ROADMAP.md` 的阶段 1
- 工作方式：用户授权 go-nx 仓库内每完成一项就 commit + push；NightDesk 侧源码只读（本阶段没有碰它，也没有从它回移任何代码——`OpenPairing` 是参照其接口形状自己重写的）
- 提交（`main`）：`2553cc6` 恶意主机上限、`f76f583` 原子写、`c5db4f8` 撤销设备、`220c814` 限流 + 未认证连接上限、`d8c8165` 默认开认证 + 配对码、`e06a085` 首次连接指纹核对、`84ab5e4` 修门限测试，以及本次收尾提交（`-listen`、文档）
- **没有发布新版本、没有改 `VERSION`**。线上仍是 0.1.0。下面的行为变更在发布新版本之前不会影响任何已安装的实例。

## 1. 成果

| 项 | 行为 | 主要位置 |
|---|---|---|
| 恶意主机上限 | 公布的帧尺寸 ≤ 16384 px/边、≤ 2²⁵ 像素；单个更新流 ≤ 132 MiB；调色板 RLE 展开前校验；raw tile 的 deflate 最多读 size+1 字节；每帧记录数 ≤ tile 数；更新流 15 s 无数据即放弃；主机自己的屏幕超限时 `Serve` 报错 | `internal/proto`、`internal/tile`、`internal/remote/{client,host}.go` |
| fuzz | `FuzzReadMsg`、`FuzzParseFrameHeader`、`FuzzDecode`，约 600 万次执行无发现 | 各包 `_test.go` |
| 原子写 + 损坏文件隔离 | 状态文件经临时文件 + fsync + rename；读不懂的 JSON 改名为 `*.corrupt-<时间>`、以空状态继续并弹窗告知，不再 `log.Fatal` | `internal/fsutil`、`trust.OpenStoreRecovering`、`profiles.LoadRecovering` |
| 撤销 | `Store.Delete/Clear/Keys`、`Policy.Revoke/RevokeAll`；主页 "Forget paired devices"（点两次）并以 `CloseRevoked` 断开当前控制者；控制端主机证书变化后 `Ctrl+F` 忘记旧指纹 | `internal/trust`、`internal/ui/home.go`、`cmd/go-nx` |
| 限流 | 按来源 IP：10 分钟内错 5 次锁 1 分钟，连续锁定加倍至 15 分钟，安静后衰减，来源表上限 1024；未认证连接 ≤ 16（同 IP ≤ 4），超出以 `CloseBusy` 拒绝 | `internal/trust/policy.go`、`internal/remote/gate.go` |
| 默认开认证 | 从没设置过 `require_auth` 的主机默认要求配对或密码；设置过的保持原值 | `trust.Store.Bool`、`cmd/go-nx/host_windows.go` |
| 一次性配对码 | 点 "Pair new device" 显示 6 位码；新设备填在 Password 里；成功即关窗；全体来源累计错 10 次也关窗 | `trust.Policy.OpenPairing`、`NewPairingCode` |
| 首次连接指纹核对 | 未固定的主机先 `remote.Probe`（只握手），标签页显示主机自报的 16 位 ID 让用户与主机 `ID:` 比对，Enter 才固定并连接；密码 / 配对码只发给已固定的身份 | `internal/remote/tls.go`、`cmd/go-nx/tabs_windows.go` |
| 监听地址 | `-listen <IP[:端口]>`（只接受 IP 字面量） | `remote.ParseListen` |

## 2. 用户可见的行为变更（发布前必须写进发布说明）

1. **升级后，没设置过 `require_auth` 的主机会要求认证。** 以前未配对的控制端直接能连；现在会被拒绝并提示先配对。用户自己的另一台 PC 如果靠"默认开放"连这台，升级后要先配对一次。
2. **配对要输码。** 以前"下一台连入的设备被信任"，现在要把主机主页上的 6 位码填进新设备的 Password。
3. **第一次连一台主机会停在核对页**，要按 Enter 才连接。已经固定过的主机不受影响。
4. 主机主页多了一个 "Forget paired devices" 按钮。

## 3. 验证（实际跑过的）

- 本地每个步骤都跑了 `gofmt -l`、`go vet`、`GOOS=windows go vet`、`go test -race ./...`，全绿。
- 变异检查：去掉 deflate 限制后，炸弹测试会失败（分配 157 MiB）。
- 门限测试连跑 30 次 `-race`。
- 端到端（真实 QUIC，回环）：恶意主机（超大帧头 / 超限更新 / 停住的流）、配对码全流程（无码被拒、错码被拒、对码配对、之后免密）、主机证书变化 → `ForgetHost` → 重新固定、`Probe` 不发 Hello 且不写 store、固定后对另一身份的主机不发 Hello。
- CI（linux + windows-latest）每次推送都跑；详见下面"遇到的问题"里的一次失败。

### 没有验证的

- **所有新增界面**都只编译 + vet 过，没在 Windows 上跑：`Forget paired devices` 按钮、配对码显示、首次连接的核对页、`Ctrl+F`、启动时的损坏提示框、新的拒绝文案。见 `ACCEPTANCE.md` H13–H17。
- 升级路径：没有用一份真实的旧版 `settings.json` 在 Windows 上启动验证"保持原值 / 缺省为开"。逻辑由 `Store.Bool` 的单测覆盖。
- 限流的真实网络表现（只有时钟注入的单测）。

## 4. 遇到的问题与处理

| 问题 | 处理 |
|---|---|
| 用 Python `replace` 批量改文件，**又一次静默没改**：`recvFrames` 里的缩进是 2 个 tab，我写成了 3 个；另一次是 gofmt 把结构体字段对齐改宽，旧文本对不上（这次脚本因 assert 中止，前几步已落地） | 前一次是靠测试失败发现的。之后改用 Edit 工具做关键替换，并在脚本里对每处 `assert old in s`；中止时先 `git diff` 看哪些已落地再续做 |
| CI 上 `TestServeTurnsAwayConnectionsWhenGateIsFull` 失败，本地通过 | 竞态在测试：主机的 `CloseBusy` 有时在 `Dial` 写 Hello 前就到，错误出现在 `Dial` 而不是 `Run`。改成两处都接受，连跑 30 次 `-race`。**这个失败连续推了两个提交才被我发现**——我在 push 之后没等 CI 就继续了。之后改为：推送后先确认 CI 再往下 |
| CI `concurrency: cancel-in-progress` 让我连续推送时前一次运行被取消 | 要验证哪个提交，就等它的运行结束再推下一个 |
| 想在握手后挂起等用户核对指纹再发 Hello | 不行：主机收不到 Hello 10 秒就放弃。改成两步：`Probe`（只握手）→ 用户核对 → `PinHost` → 正常 `Dial`，副作用是首次连接多一次握手 |
| 配对码每个来源只允许 5 次，我最初把"全体来源累计"的上限也设成 5，导致"单来源被锁"这个状态永远走不到（配对窗口先关了） | 全体上限改为 10，两层限制分开测试 |
| 计划写的是"已有 `settings.json` 保持原值，只改新装默认" | 实际无法区分"新装"和"从没碰过这个设置的旧装"。改为：**缺省 = 开，显式保存的 `0` / `1` 保持**。这比计划更严格，上面第 2 节已列为行为变更 |

## 5. 设计思路与取舍

- **配对码走 Password 字段**，不改线上协议，所以对现有客户端（含 NightDesk 已发布版本）兼容：它们本来就把用户填的东西放在 Hello 的密码里。
- **限流按 IP 而不按全局**：全局锁会让攻击者锁掉真用户；按 IP 则只影响他自己。QUIC 握手需要往返，IP 不能随意伪造。代价：同一 NAT 后的多台机器共用额度。
- **未认证连接用 `Gate` 而不是调 QUIC 参数**：quic-go 对"握手阶段并发"的控制有限，`Gate` 管的是"已握手、等 Hello"这段，正好是我们能控制的。
- **恶意主机上限放在协议层（`ParseFrameHeader`）和解码层（`Decode`）两处**：任何调用者（包括将来的 Android / mobile 客户端）都自动受益。
- **暂缓 DPAPI + 密码哈希**：理由写在 `ROADMAP.md`「暂缓项」。要点是收益小，且需要先把"My password"从逐字符落盘改成按 Enter 提交。
- 没有做逐设备的配对列表和单个撤销界面：`Policy.Revoke(fp)` 已有，只缺 UI。

## 6. 经验与坑

- **本地绿 ≠ CI 绿**：QUIC 的错误出现在哪个调用上取决于时序，测试不要假设。
- 批量文本替换后一定 `git diff` 核对；断言 `old in s` 只能说明某一处匹配，不能说明整个脚本都落地。
- zsh 里 `for t in "a b"; do set -- $t` 不会按空格拆分（不像 bash），会得到一个参数。
- `ui` 包的布局测试（`TestLayoutHasNoOverlaps`）值得保留：加按钮时它会立刻告诉你有没有压到别的区域。
- 配对码的"一次性"要包含失败路径（窗口在 N 次错码后关闭），否则 6 位数字能被穷举。

## 7. 未完成项与下一步

1. **阶段 2：协议规范与版本协商**（`ROADMAP.md`）。注意：回移 NightDesk 的 `MsgText` / `WheelH` 属于阶段 2/3 的兼容性工作，来源只读。
2. 用户在 Windows 上做 `ACCEPTANCE.md` H13–H17（新界面）和 H1–H3（自更新）。
3. 发布新版本（需要用户单独授权）：改 `VERSION`、写发布说明（第 2 节四条）、`NOTES=… ./release.sh`。
4. 可选：逐设备配对列表与撤销；密码哈希 + DPAPI（见暂缓项）。
