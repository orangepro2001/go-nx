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
| 1 | 安全基线 | 默认开启认证（含升级迁移）；一次性配对码 + 指纹核对，校验前不发密码；密码哈希 + DPAPI；撤销设备；按来源限流；监听地址可选；恶意主机上限；JSON 原子写；fuzz | ☐ |
| 2 | 协议规范与版本协商 | `docs/protocol.md`；v0 冻结（含 `MsgText` / `WheelH`）+ 金样向量；新 ALPN `go-nx/1`（版本 + 能力位、长度前缀、未知消息可跳过）；兼容矩阵测试 | ☐ |
| 3 | 回移 NightDesk 修复 | `backport-ledger.md`；B0 模块改名 `github.com/orangepro2001/go-nx`；B1 文本 / 横向滚轮；B2 多显示器 + per-monitor DPI + 帧内光标 + `VIRTUALDESK` + 尺寸下发；B3 `pinnedClientTLS` / `FPSFn` / 回调；B4 合成源 + `nx-probe`；B5 `mobile/` + `nx-host` + AAR 脚本；像素 / 鼠标 / 光标几何契约测试 | ☐ |
| 4 | 输入与捕获可靠性 | 按键顺序确定；异步发送；退出与断线释放；`Grab` 失败重建；`Win+L` 策略；键表补全；坐标往返属性测试；自更新加固 | ☐ |
| 5 | 光标闪烁取证 | `CAPTUREBLT` 运行时开关（默认不变）；人工 A/B 清单；据结果定默认值 | ☐ |
| 6 | 度量与弱网验收 | 合成源基准（1080p / 4K × 四类画面）；用户态有损 UDP 代理复现 1 Mbps / 80 ms / 1% 丢包 | ☐ |
| 7 | 可持续维护 | `compat-matrix.md`；`nx-host` / AAR 产物构建；人工实机清单随功能更新 | ☐ |

> `build.sh` 现在对 `cmd/*/` 里每个目录都用 `-H=windowsgui` 构建。阶段 3 加入 `nx-host` / `nx-probe`（控制台程序）前必须先改这个循环。

## 验证口径

- 自动化通过 ≠ 真实体验正确。`nx-probe` 走合成源和日志注入器，**不能**证明真实 GDI / `SendInput`；写进 `ACCEPTANCE.md` 时必须这样标。
- 光标闪烁、混合 DPI、锁屏 / UAC 在拿到实机证据前一律标"待验证"，不写成已修复。
