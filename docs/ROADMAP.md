# go-nx 开发路线图

- 制定：2026-10-28。依据：[`review-2026-10-28-nightdesk-integration-notes.md`](review-2026-10-28-nightdesk-integration-notes.md) 与 [`IMPLEMENTED.md`](IMPLEMENTED.md)。
- 状态：☐ 未开始　◐ 进行中　☑ 完成。每完成一项就更新本表。

## 前提与约束

1. **go-nx 是单一上游**：NightDesk `native/` 里的通用修复回移进来，不再各改各的。
2. **只有一台日常使用的 Windows PC**：改缩放、锁屏、UAC、长跑这类会影响本地使用的实机项只写成人工清单（`ACCEPTANCE.md` §3），由用户择机执行。GitHub `windows-latest` 当一次性 Windows。
3. **NightDesk 源码暂时只读**（它不是最新版本）：只读取快照做回移参考；NightDesk 改依赖 go-nx 产物的事留到以后。回移在 `backport-ledger.md` 里登记来源与快照日期。
4. **每完成一项可交付的工作就提交并推送**到 `origin main`（用户授权，仅限本仓库）。检查没过不提交；不 amend、不 force push、不 `git add -A`。
5. **授权边界**：`release.sh` 发新版本、打 tag / GitHub Release、装依赖、改认证，仍需单独问。
6. 协议改动必须保持对 NightDesk 已发布客户端的兼容（它们发 `MsgText=4`、`KindWheelH=7`）。
7. 测试与探针只用回环地址和内核分配端口；不新增 tailscale serve 路径。

## 对 NightDesk 的接口

不做公开 Go API。go-nx 自己构建 `nx-host.exe`、`nx-probe`、gomobile AAR（`mobile/` 与 `internal/*` 同一模块，可直接 import）；NightDesk 以后再引用某个版本的产物。需要稳定的是三份契约：线上协议（`docs/protocol.md`）、`nx-host` 的 JSON Lines schema、像素 / 坐标语义。

## 阶段

| # | 阶段 | 内容 | 状态 |
|---|---|---|---|
| 0 | 基线与可信的现状 | 测试基线；`IMPLEMENTED.md` / `ACCEPTANCE.md` / `README.md`；设计文档加"非实现"横幅；CI 与 Windows 冒烟 spike（结论：windows-latest 可做 GDI 抓屏与 SendInput 冒烟，见 `ACCEPTANCE.md` §1b） | ☑ |
| 1 | 安全基线 | ☑ 恶意主机上限 + fuzz（`proto`、`tile`）　☑ JSON 原子写 + 损坏文件隔离　☑ 撤销设备（全部忘记 + 忘记主机；逐设备列表待做）　☑ 按来源限流 + 未认证连接上限　☑ 监听地址可选（`-listen`）　☑ 一次性配对码　☑ 默认开启认证（设置过的保持原值，从没设置过的变为开）　☑ 首次连接指纹核对（校验前不发密码）　⏸ 密码哈希 + DPAPI（暂缓，见下） | ☑ |
| 2 | 协议规范与版本协商 | ☑ `docs/protocol.md`　☑ v0 冻结（含 `MsgText` / `WheelH`）+ 手写字节向量　☑ 新 ALPN `go-nx/1`（版本 + 能力位、长度前缀、未知消息可跳过、Welcome）　☑ 新旧四种组合的兼容矩阵测试 + 冻结 v0 查看器 / 原始 v1 字节的主机测试　☑ fuzz | ☑ |
| 3 | 回移 NightDesk 修复 | `backport-ledger.md`；B0 模块改名 `github.com/orangepro2001/go-nx`；B1 文本 / 横向滚轮；B2 多显示器 + per-monitor DPI + 帧内光标 + `VIRTUALDESK` + 尺寸下发；B3 `pinnedClientTLS` / `FPSFn` / 回调；B4 合成源 + `nx-probe`；B5 `mobile/` + `nx-host` + AAR 脚本；像素 / 鼠标 / 光标几何契约测试 | ☐ |
| 4 | 输入与捕获可靠性 | 按键顺序确定；异步发送；退出与断线释放；`Grab` 失败重建；`Win+L` 策略；键表补全；坐标往返属性测试；自更新加固 | ☐ |
| 5 | 光标闪烁取证 | `CAPTUREBLT` 运行时开关（默认不变）；人工 A/B 清单；据结果定默认值 | ☐ |
| 6 | 度量与弱网验收 | 合成源基准（1080p / 4K × 四类画面）；用户态有损 UDP 代理复现 1 Mbps / 80 ms / 1% 丢包 | ☐ |
| 7 | 可持续维护 | `compat-matrix.md`；`nx-host` / AAR 产物构建；人工实机清单随功能更新 | ☐ |

> `build.sh` 现在对 `cmd/*/` 里每个目录都用 `-H=windowsgui` 构建。阶段 3 加入 `nx-host` / `nx-probe`（控制台程序）前必须先改这个循环。

## UI 重设计（独立于阶段 0–7）

方案在 [`ui-redesign.md`](ui-redesign.md)（v2）：继续用 Ebiten、自研极简 UI 层、不新增模块；以远程使用效率为先，不对齐任何现有产品；**主题色蓝色；界面语言简体中文 / English / 한국어**（均为用户 2026-10-28 的决定）。U0 技术验证（按需渲染、三种字体与内存、`exp/textinput` 在中文 / 韩文输入法下的表现、基线数字）先于任何实现。状态：**UI 阶段只写文档，没有代码**（严格 / 开放模式切换是例外，已先行实现并发布于 0.3.0）；待定问题见该文档第 13 节。

## 暂缓项

- **主机密码哈希 + DPAPI 保护私钥与连接密码**（原属阶段 1）。暂缓的理由：① 威胁面小——同一 Windows 用户的进程本来就能调用 DPAPI 解密，其他用户已被 `%AppData%` 的 ACL 挡住，只有"配置目录被整个拷走"才受益；② 主机密码改存哈希后，"My password"输入框不能再回填，而且现在每敲一个字符就生效并落盘（`setPassword`），要先把它改成"按 Enter 提交"才能避免每个按键都做一次 scrypt，这是一次 UI 交互改动，应当和别的输入框改动一起设计。重新评估的时机：计划支持多人共用的 PC，或阶段 7 整理存储格式时。
- 在此之前，README 与 `IMPLEMENTED.md` 都明确写着凭据是明文。

## 验证口径

- 自动化通过 ≠ 真实体验正确。`nx-probe` 走合成源和日志注入器，**不能**证明真实 GDI / `SendInput`；写进 `ACCEPTANCE.md` 时必须这样标。
- 光标闪烁、混合 DPI、锁屏 / UAC 在拿到实机证据前一律标"待验证"，不写成已修复。
