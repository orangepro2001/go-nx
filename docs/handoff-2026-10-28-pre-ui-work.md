# 交接：做完 UI 之前能做的一切（含 U0），完整测试通过，发布 0.5.0

- 日期：2026-10-28（KST）
- 用户的要求："先把在做 UI 之前的能做的都做了并通过完整测试（包括 U0）"。界面本身仍然只有文档（用户之前的决定）。
- 提交：`a14065f` 到 `33eac14`，加上由 `ship.sh` 生成的 `0b778df Release 0.5.0`；全部已推送，CI（linux + windows-latest）在 Release 提交上通过。
- 发布：go-nx `win-x64` **0.5.0（versionCode 500）**，18.5 MiB（19,418,624 字节），sha256 `552708ee4023213bd971d3a8b627f8e3f2dc33249e57e6d12a797e30808fe41e`，线上最新（0.1.0–0.4.0 仍在列表里）。

## 1. 结论

路线图阶段 0–4、6、7 与回移 B0–B5 都做完了，阶段 5 差你的一次人工 A/B，U0 做完了并**改了界面方案的预算**。这一轮最有价值的不是"又多了多少代码"，而是：**实现里抓到并修了几个真问题，U0 推翻或修正了方案里的几个假设**（第 4 节）。没有在你的 PC 上验证的东西很多（第 5 节），因为我只能在 Linux、Wine 和 GitHub 的 `windows-latest` 上跑。

## 2. 做了什么

| 路线图条目 | 内容 | 位置 / 提交 |
|---|---|---|
| 阶段 4 收尾 | 异步发送队列（有界 1024、合并移动、`ErrBacklog`、写超时即断开）；指针绘制可注入并有像素级测试；**自更新加固**：`Install` 前重验大小和 SHA-256、同一时刻只装一次（新状态 `Installed`）、第二次改名遇到杀毒软件占用会重试、改名前不再先删旧备份、`.old` 在新版本运行满首次延迟后才清理、更新被打断时从 `go-nx.exe.old` 启动会自己改回名字（`update.Recover`）、拒绝从 `.old`/`.new` 路径更新 | `eba5f0c`；`internal/update`、`internal/remote/outbox.go` |
| B3 | 按指纹固定的连接（`DialOptions.ExpectFingerprint`：握手就拒绝别的证书，**密码不会发给错的主机**）；`FPSFn`、`OnInput`、`OnFrame`、`Boost`（输入时升帧） | `4253200` |
| B4 | `internal/synth`（五种场景 + 四角颜色标记 + `VerifyMarkers`）、`internal/logsink`、`internal/probe`、`cmd/nx-probe` | `910c436` |
| 关闭的可靠性 | `Client.Close()` 先把已接受的输入写完、结束控制流、等主机读完再关；主机把"查看器正常结束控制流"当作正常结束；`BGRAToRGBA` 移到可测的包 | `e163dd4` |
| B5 | `internal/hostd` + `cmd/nx-host`（stdin/stdout JSON Lines，契约 `nx-host.md`）；`mobile`（gomobile 友好的 `Session`，契约 `mobile.md`，Java API 固定在 `mobile/api.golden`）；`scripts/build-aar.sh`；`scripts/e2e-host-probe.py` | `8b377b2` |
| 阶段 5 | `CAPTUREBLT` 运行时开关早已在（`-layered=false`）；H6 现在有逐步的 A/B 清单 | `da4b62b`（文档） |
| 阶段 6 | `tools/perf`（合成画面上每帧的成本）、`internal/netem`（纯 Go 的用户态 UDP 代理）、弱网验收测试；数字见 `perf.md` | `9231d2b`、`da4b62b` |
| 阶段 7 | `scripts/artifacts.sh`（构建全部程序、写哈希与清单、`--verify` 验证可复现）、`compat-matrix.md`、`release.md` 的产物说明；`build.sh` 不再把控制台工具构建成窗口程序 | `da4b62b` |
| U0 | `tools/u0`（字体、离屏渲染与 SDF 着色器、空闲开销与 `ScheduleFrame`、tile 上传、Windows 上的按键注入），CI 在 Linux（Xvfb）和 Windows runner 上跑并保留报告；结果 `ui-baseline.md`，已回写 `ui-redesign.md` | `234aa65`…`33eac14` |

## 3. 验证（实际跑过的）

- 本地：`gofmt`、`go vet`、`GOOS=windows go vet`、`go test -race -count=1 ./...` 全绿；覆盖率见 `ACCEPTANCE.md` §1。`FuzzReadMsg`、`FuzzParseFrameHeader`、`FuzzReadMsgV1`、`FuzzReadHostMsgV1`、`FuzzDecode` 各 15 s 无新崩溃。`scripts/e2e-update.sh`（真实 `relhub`）、`scripts/e2e-host-probe.py`、`scripts/build-aar.sh --check-api`、`scripts/artifacts.sh --verify`（两次空缓存构建逐字节相同）通过。
- **变异检查**：本轮每个新行为都做过——故意改坏实现，对应测试确实失败（更新器、`FPSFn` / `OnInput` / `OnFrame`、固定指纹、probe、mobile、hostd、netem）。没有失败的改动会改成"补测试"，而不是放过。
- Wine：`capture`（含真实桌面测试）、`inject`、`update`（含"改名运行中的 exe"）全部通过。
- CI（`windows-latest`，真实 Windows）：以上 Windows 专用测试；**`nx-host.exe` 与 `nx-probe.exe` 作为两个进程的完整流程**（配对、v1 与 v0、画面标记、8 项输入回执、错误指纹和错误码被拒、退出码）；U0（DirectX 软件渲染下的字体、着色器、`ScheduleFrame`、按键与 Unicode 文本注入）。
- 发布后**独立验证**（没有只信脚本自己的检查）：用真实 `internal/update` 代码、默认地址和内置公钥，假装 0.4.0 → 检查到 0.5.0、下载 19,418,624 字节并验签，sha256 与本地 `build/go-nx.exe` 一致；假装 0.5.0 → 最新；`relhub check` code 400 → 退出码 10，code 500 → 0。

## 4. 做的过程中抓到、修掉的问题（值得知道）

1. **异步发送队列会丢最后几个按键释放。** 队列让 `Client.Close()` 在输入还没写完时就关了连接。修的时候一层层剥开，每层都有失败在先的测试：①"写完"不等于"送到"，关连接会丢掉已写入未送达的数据，所以要先结束控制流、等主机读完并挂断；②关闭过程中到来的确认写入会因为流已结束而失败、**把整条连接关掉**，连带丢掉尾部输入（`TestCloseWhileAnAcknowledgementIsStillToCome` 用慢主机 + 慢查看器每次都命中这个窗口）；③主机把查看器的正常结束当错误。这个队列在 0.4.0 里没有；是修好之后才随 0.5.0 发布的。
2. **`FPSFn` 的参考实现会崩**：返回 0 会除零，返回 2³⁰ 会让 `Ticker.Reset` panic。现在 ≤0 保持当前速率、过大夹到 120。
3. **断开原因的竞态**：查看器礼貌挂断，`Serve` 有时看到的是错误而不是关闭的连接，`nx-host` 的 `disconnected.reason` 随之忽 `closed` 忽 `error`（`-race -count=3` 才露出来）。
4. **一个测试顺序错觉**：回答和"会话结束"事件的先后不固定，测试改成不依赖顺序。
5. **Windows 上脚本清理失败**：`e2e-host-probe.py` 在 Windows 上删临时目录时自己还开着 stderr 文件（流程本身全部通过，只是清理失败）。
6. **我自己的一个错误结论，已改正。** U0 的字体内存对比里，对照组把解析出来的字体扔掉了，只数了字节，于是得出"用 `*os.File` 反而更费内存、计划要改成读整个文件"。在把它写进文档之前我拿数字对着机制重读，发现对照不公平；保留解析结果后重测（CI 的 Windows runner 上）：**文件句柄才对**（YaHei 37 MB 对 94 MB；面越多差得越大），计划原来的规则保留。文档和提交说明里都写了这个更正。
7. **`gobind` 的 `-mod=mod` 改了 `go.mod`/`go.sum`**（升级了几个依赖、加了 `x/mobile`）。我当场发现并用 `git checkout` 还原；`build-aar.sh` 现在只在**临时副本**里加依赖，仓库的 `go.mod` 不变。
8. **Wine 跑不了 QUIC 监听**（`wsaioctl: winapi error #10045`），所以 `nx-host` 的完整流程靠 `windows-latest`，不靠 Wine。

## 5. 没有验证的 / 局限（请当真）

- **你的 Windows PC 上的任何真实行为**：主页 / 严格模式 / 更新按钮的绘制与点击、真实 GDI 抓屏与 `SendInput`、多显示器、会话中改分辨率、指针闪烁、更新替换并重启（H2）、异步发送队列的手感。CI 的 runner 没有鼠标、没有 GPU。发布 0.5.0 是按你的"每次行为改动发一版"规则做的；**0.5.0 的客户端行为没在真机上跑过**，如果出问题，修复发 0.5.1，必要时 `relhub yank` 撤回 0.5.0。
- **AAR 没有构建过**（本机没有 Android SDK/NDK）；只验证了 Java 绑定的 API（`--check-api`）。
- **性能数字是合成画面上的**，没有真实 GDI 抓屏的成本，没有真实网络（`perf.md` 写明了局限）。弱网验收是独立丢包 + 固定延迟，不是真实 Wi-Fi / 中继。
- U0 的软件渲染数字（Windows runner 的 DirectX WARP）不能当作你的 GPU 的数字；输入法开启时的行为没有测（runner 没有输入法）。
- 换机器 / 换 Go 版本 / Windows 本机构建时可复现构建是否仍然成立：没验证。
- 韩文译文还不存在（UI 没做）；`mobile` 与 NightDesk 的 Kotlin 代码的实际联调没做（NightDesk 仍然只读不改）。

## 6. 需要你做的

**人工清单（`ACCEPTANCE.md` §3，只有你一台日常 PC，⚠ 的会改系统状态）**：

| 编号 | 做什么 | 为什么 |
|---|---|---|
| H2 / H18 | 托盘或主页按钮从 0.4.0 更新到 0.5.0 | 真机上第一次走一遍更新流程（新的重验、`.old` 延迟清理） |
| H6 | 指针闪烁的 `-layered=false` A/B（六种情形，已写明步骤） | 阶段 5 只差这个；没复现就写"未复现" |
| H20、H21、H22、H23、H24、H25 | 多显示器、会话中改分辨率、帧内指针、拖出窗口再松开、退出时按键释放、Win+L | 0.5.0 的新行为 |
| H26 | 被打断的更新能否恢复（把 `go-nx.exe` 改名为 `go-nx.exe.old` 再运行它） | 这是新加的自愈 |
| H28 ⚠ | `nx-host.exe` + `nx-probe -input` 在真实桌面上（会真的点击、滚动、往焦点窗口输入文字，先开一个空记事本） | `nx-host` 的真实抓屏 / 注入从没跑过 |
| H29 | U0 在你的 PC 上（`go run ./tools/u0 all -seconds 60`），以及有中 / 韩输入法时的 `textinput` 与远端 VK_HANGUL | 真实 GPU 数字、输入法 |
| H27 | 装好 Android 工具链后 `scripts/build-aar.sh` | AAR |

**需要你决定的**（不阻塞，但决定后面做什么）：

1. 要不要开始 UI 实现（U1）？U0 的结论是方案成立，但**内存预算已改**（英文 ≤ 45 MB、中 / 韩 ≤ 80 MB 私有内存），请确认可接受。`ui-redesign.md` 第 13 节还有两条待定（B 层要做哪些功能）。
2. `nx-host.exe` / `nx-probe.exe` 要不要作为单独的应用发布到 release-publish？现在只构建不发布。
3. 默认开启随输入升帧（`Boost`）的主程序接入：机制已有，数据在 `perf.md`（1080p 平滑滚动文档约 250 KB / 帧，10→30 fps 带宽 ×3）；我没有接入，等你说。
4. 阶段 5 的默认值（`capture_layered`）等 H6 的结果。

## 7. 怎么接着做

```sh
go test -race -count=1 ./...                     # 全部测试
scripts/e2e-update.sh                            # 更新路径（真实 relhub，临时目录）
python3 scripts/e2e-host-probe.py --host <nx-host> --probe <nx-probe>
scripts/build-aar.sh --check-api                 # 需要 gobind 在 PATH
scripts/artifacts.sh [--verify]                  # 构建全部程序 + 哈希
go run ./tools/perf                              # 每帧成本
xvfb-run -a go run ./tools/u0 all -png s.png     # U0（Windows 上直接跑）
scripts/ship.sh --bump patch "说明"              # 发新版；运行期间不要改仓库文件
```

先读 `docs/context-recovery.md`，再看 `docs/ROADMAP.md`、`docs/ACCEPTANCE.md`（H 编号）、`docs/ui-baseline.md`。NightDesk 侧仍然只读；回移台账 `docs/backport-ledger.md` 里 B0–B5 都是 ☑，"待 NightDesk 跟进"那一节是将来的事。
