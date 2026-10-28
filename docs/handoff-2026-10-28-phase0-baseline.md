# 交接：参考文档核对 + 开发计划 + 阶段 0（基线与可信的现状）

- 日期：2026-10-28（KST）
- 任务：核对《go-nx 集成经验与独立维护建议》，制定开发计划，并完成计划的阶段 0
- 前提（用户确认）：go-nx 作单一上游；只有一台日常使用的 Windows PC；**暂不改动 NightDesk 侧源码**；本仓库每次完成后自动提交并推送
- 提交：`52361cb`（补交接文档）、`6289d39`（状态文档）、`820d1f7`（CI + 冒烟测试），以及本次收尾提交。全部已推送 `origin main`

## 1. 成果

| 项 | 位置 |
|---|---|
| 对参考文档逐条核对，并记录文档未列出的缺陷与分叉漂移 | `docs/review-2026-10-28-nightdesk-integration-notes.md` |
| 开发路线图（阶段 0–7，带状态） | `docs/ROADMAP.md` |
| 以代码为准的实现状态（带文件:行），含"设计文档中未被代码支持的说法"对照表 | `docs/IMPLEMENTED.md` |
| 已验证 / 未验证矩阵；CI runner 实测结论；人工实机清单 H1–H12 | `docs/ACCEPTANCE.md` |
| 四份设计文档顶部加"设计意图，非实现状态"横幅 | `docs/{minimal-plan,final-converged-plan,plan-v2-converged-win10,feasibility-nx-remote-control}.md` |
| 仓库 README（先写限制再写用法） | `README.md` |
| CI：linux（gofmt / vet / `-race` / 可移植核心交叉编译）+ windows-latest（build / test / 桌面冒烟） | `.github/workflows/ci.yml` |
| 真实桌面冒烟测试（需 `GO_NX_WINDOWS_E2E=1`） | `internal/capture/capture_windows_test.go`、`internal/inject/inject_windows_test.go` |

没有改动任何产品代码，也没有改 `VERSION`，没有发布新版本。

## 2. 核对结论（要点）

完整表格见核对记录。三条最重要的：

1. **参考文档对 go-nx 本体的描述基本属实**，且现状更糟：认证默认关、密码与私钥明文、无法撤销设备、客户端对恶意主机无上限、`Grab` 出错即结束会话、退出不释放输入。
2. **颜色和鼠标释放坐标这两个 bug 不在 go-nx 本体里**：Ebiten 查看器已经交换 B/R 并置 alpha，每个鼠标事件都带坐标；bug 在 NightDesk 的 Android / `mobile` 层。所以 go-nx 要补的是契约测试，不是修复。
3. **§4.4 的光标闪烁线索漏了 `CAPTUREBLT`**：go-nx 和 NightDesk 快照都用 `BitBlt(..., SRCCOPY|CAPTUREBLT)`，而它是 GDI 抓屏时宿主光标闪烁的常见原因（据记忆，**未实测**）。排在路线图阶段 5 做 A/B。

## 3. 验证（实际跑过的）

- 本地：`gofmt -l .` 干净；`go vet ./...`、`GOOS=windows go vet ./...` 通过；`go test -race -cover ./...` 全部通过（覆盖率见 `ACCEPTANCE.md` §1）。
- 核心包交叉编译：`proto/tile/trust/remote` 在 linux/amd64、darwin/arm64、windows/amd64、android/arm64 都能编译。
- 新测试先在本机 wine（临时 `WINEPREFIX` + Xvfb）里跑通，再上 CI。
- CI 在 `820d1f7` 上两个作业都通过；windows-latest 的桌面冒烟结果：有可交互的 1024×768 桌面；GDI 抓到的字节顺序是 B,G,R（红 `0,0,255` 等四种颜色全部对）；`SendInput` 绝对移动 10 个目标全部精确；按下 / 抬起后光标位置不变；F24 按下 / 抬起可被观察。
- 收尾提交的 CI（把冒烟步骤从非阻塞改成阻塞）见 `gh run list`。

### 没有验证的

- 所有需要真实显卡 / 物理显示器的行为：DPI 缩放、多显示器、鼠标指针像素、光标闪烁、锁屏 / UAC / `Win+L`、长时间稳定性。
- 自更新的 Windows 实机流程（托盘、替换 exe、重启）——上一任务遗留，见 `ACCEPTANCE.md` H1–H3。
- 参考文档的 WPF（`Bgr32`）论断与"已发布 / 未提交"现状：对应源码和记录不在本机。
- `CAPTUREBLT` 与闪烁的关系：纯假设。

## 4. 遇到的问题与处理

| 问题 | 处理 |
|---|---|
| 计划模式下 Explore agent 保守地没有跑 `go test` | 进入执行后自己跑 `go test -race -cover ./...` 作为基线 |
| zsh 对 `grep --include=*.go` 报 "no matches found"，我一开始以为是没有匹配，差点据此下"没有光标调用"的结论 | 加引号重做；agent 的独立结论也一致 |
| 单靠 agent 报告的行号不可靠 | 写 `IMPLEMENTED.md` 前，对每个要引用的行号重新 `grep`/`sed` 核实；发现 README 里关于界面文案的几句是想当然写的，对照 `internal/ui/home.go` 改正；"静止时码率接近 0"其实是设计目标、从未测量，改成如实说明 |
| wine 默认会在 `~/.wine` 建前缀 | 用 `WINEPREFIX` 指向临时目录；`~/.wine` 原本就存在，没动 |
| 要验证窗口 / GDI 测试，但没有 Windows | 先用 wine + Xvfb 调通测试代码，再让 windows-latest 给出真结果；两者在绝对坐标上结论不同（wine 1280×800 偏 1px，runner 1024×768 无偏差），文档里已分别注明 |
| 冒烟步骤用了 `continue-on-error`，成功状态会掩盖失败 | 不看作业总状态，直接拉日志核对每个测试的 PASS/日志；确认后改成阻塞 |

## 5. 设计思路与取舍

- **不做公开 Go API**：`mobile/`（gomobile）和 `cmd/nx-host` 与 `internal/*` 同一模块，可直接 import，所以没必要把 `proto/tile/remote` 搬出 `internal/`。需要稳定的是三份契约：线上协议、`nx-host` 的 JSON Lines schema、像素 / 坐标语义。参考文档里"评估公开 Go API"因此降级为可选。
- **用 windows-latest 当一次性 Windows**：用户只有日常 PC，不能随意改缩放或锁屏。runner 能做抓屏颜色、`SendInput` 位置 / 按键这类"不看肉眼"的检查；闪烁、DPI、多屏、锁屏仍要人工。
- **真实桌面测试用环境变量门控**：`GO_NX_WINDOWS_E2E=1` 才运行。否则有人在自己的 Windows 上 `go test ./...` 会被移动光标、弹出色块窗口。
- **文档先于代码**：阶段 0 只改文档、CI、测试，没有动产品代码，好让阶段 1 的行为变更有清晰的"之前"基线。
- **绝对移动偏差只记录不判失败（>1px 才失败）**：真实偏差与分辨率有关，先量出来，阶段 4 再收紧为精确相等。

## 6. 经验与坑

- 用 agent 并行核对很有效，但它们的行号和"没找到"都要自己复核一遍再写进文档。
- `continue-on-error` 的步骤不会让作业变红；判断 spike 结论一定要读步骤日志。
- Windows 测试可以在 Linux 上 `GOOS=windows go test -c` 交叉编译，再用 wine 或 CI 跑。
- `build.sh` 现在对 `cmd/*/` 全部用 `-H=windowsgui`，阶段 3 加控制台程序前必须改。
- `actionlint` 本机没有，workflow 用 `yaml.safe_load` + 真实跑一遍 CI 把关。

## 7. 未完成项与下一步

1. **阶段 1 安全基线**（见 `ROADMAP.md`）：默认开认证（含旧设置迁移）、一次性配对码 + 指纹核对、校验前不发密码、密码哈希 + DPAPI、撤销设备、按来源限流、监听地址可选、恶意主机上限、JSON 原子写、fuzz。这是行为变更，需要升级迁移与发布说明；发布新版本仍需单独授权。
2. 阶段 2（协议规范 + `go-nx/1`）、阶段 3（回移，来源只读）依次进行。
3. 用户在 Windows 上做 `ACCEPTANCE.md` §3 的 H1–H12。
4. 可选：给 go-nx 单独发 CI token；窗口主页显示更新提示；Windows 代码签名；签名私钥另存一份到本机之外。
