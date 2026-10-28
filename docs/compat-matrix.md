# 兼容矩阵与可复现构建

回答三个问题：**哪个 go-nx 版本说哪个协议？** **`nx-host` 的控制接口和 `mobile` 的 Java API 各是哪一版？** **一个产物怎么从源码重新做出来，并证明做出来的是同一个？**

## 1. go-nx 版本 ↔ 能力

线上协议见 `protocol.md`（v0：ALPN `go-nx`，已冻结；v1：ALPN `go-nx/1`）。"线上协议"一列写这个版本的**主机和查看器**会说什么。

| 版本 | 日期 | 线上协议 | 值得知道的变化 |
|---|---|---|---|
| 0.1.0 | — | v0（原始） | 基线：QUIC + 自签名证书、tile + deflate、自更新 |
| 0.2.0 | 2026-10-28 | v0 | 安全基线：默认要求认证、一次性 6 位配对码、首次连接核对主机指纹（校验前不发密码）、限流、撤销设备、恶意主机上限、原子写、`-listen`；主页版本号和更新按钮 |
| 0.3.0 | 2026-10-28 | v0 | 严格 / 开放模式按钮（切到严格会立即断开未验证的会话） |
| 0.4.0 | 2026-10-28 | v0（含 NightDesk 的 `MsgText`、`KindWheelH`）+ **v1**（版本与能力协商） | 文本输入（`KEYEVENTF_UNICODE`）、横向滚轮、v1 握手与能力位 |
| 0.5.0 | 2026-10-28 | 同 0.4.0（线上协议没有变化） | 多显示器与分辨率变化、帧内指针（按需）、确定的释放顺序、Win+L 策略、绝对坐标逐像素精确、异步发送队列、**关闭时输入先到主机**、更新器加固。versionCode 500，sha256 `552708ee4023213bd971d3a8b627f8e3f2dc33249e57e6d12a797e30808fe41e` |
| 0.6.0 | 2026-10-28 | 同 0.4.0（线上协议没有变化） | **动态帧率**：画面在变时 10 fps，控制端操作时 30 fps，静止 3 s 后 2 fps；控制端的输入立刻唤醒抓屏。新参数 `-fps-active`、`-fps-idle`。versionCode 600，sha256 `bd41bd793243fb12653f69ac1dea5800f622bbc01b578fb1fc288b3bd1eafc16` |
| 0.7.0 | 2026-10-28 | 同 0.4.0（线上协议没有变化；新增连接关闭码 8，见 `protocol.md`） | **新界面**（U1–U4）：设备 / 本机 / 日志 / 设置 / 会话，简体中文 / English / 한국어，蓝色主题（深色 / 浅色），会话工具条（适应 / 100% 与平移、全屏、发送文本、统计、修饰键角标、网络断开后自动重连）；已配对设备列表不再显示对方连接时的端口。versionCode 700，sha256 `d6c77efdc2106e37610bd08b549591461cbe2fdc232e747e023779958aade018` |
| 0.7.1 | 2026-10-28 | 同 0.4.0（线上协议没有变化） | 配对被拒的修复：配对码比较忽略空格 / 短横、接受全角数字；被拒时错误卡片可重新输入密码或配对码；密码框可"显示"；配对窗口 5 分钟；托盘重复点配对不换码；主机日志写明被拒原因（不含秘密）。versionCode 701，sha256 `30b796f121f4368983656b45f16ba1e0808934f6560488c8421043be2729707e` |
| 0.7.2 | 2026-10-29 | 同 0.4.0（线上协议没有变化） | 主机每 10 s 检测同一 UDP 端口上的其他程序，"本机"页警告并写明程序名、进程号和地址；"本机"页可关掉输错 5 次锁定（设置 `lockout`）；被拒卡片显示对方主机 ID。versionCode 702，sha256 `a3aec20183c46f7291433c2e629467fa060bab98119366ed16c211ffc454a72c` |
| 0.8.0 | 2026-10-29 | 同 0.4.0（线上协议没有变化） | 设置页分区（常规 / 共享 / 网络 / 会话 / 关于）；设置 → 网络改 UDP 端口与监听地址，立即生效、失败退回（设置 `port`、`listen_ip`，命令行优先）；开机在托盘里启动（HKCU Run）；可允许 Win+L；控制端非默认端口按 `IP:端口` 记主机身份（7000 仍按 IP，旧记录有效）。versionCode 800，sha256 `4246fb2e24db0a99f8ea1232797a443d71bea31b9da6192df2d4e7d707655b21` |
| 0.9.0 | 2026-10-29 | v0 + v1；v1 新增 `CapVirtualDisplay`（bit 4）、`ViewSize`（0x05）、`DisplayState`（0x83），旧对端跳过 | 画质放大（清晰 / 平滑 / 像素）；Parsec VDD 虚拟显示器按主控端窗口大小（主机设置允许、内置驱动安装包）。versionCode 900，sha256 `a3791a0efaa8250f177e6e0f9208241950d1b8709e2671c6bc2359c6f99a1482` |
| 未发布 | — | 同 0.4.0 | 只有文档、CI 和不在 `go-nx.exe` 里的东西：`mobile`、`tools/{perf,u0,mkzip}`、`internal/{netem,synth,logsink,probe}` |

**核心应用 `go-nx-core`**（`nx-host`、`nx-probe` 与契约，版本 `CORE_VERSION`，与 go-nx 的版本无关）：

| 核心版本 | 日期 | 线上协议 | `nx-host` schema | 值得知道的变化 |
|---|---|---|---|---|
| 0.1.0 | 2026-10-28 | v0 + v1 | 1 | 第一次作为单独应用发布：`nx-host`（JSON Lines 控制接口、动态帧率 `-fps` / `-boost` / `-idle`）、`nx-probe`（schema 1）。versionCode 100，zip sha256 `d3bf542e8b307f27132b12bbbd41b90783f83f40ca5d7ba4fb6f1024c34bdfbb` |
| 0.2.0 | 2026-10-28 | v0 + v1 | 1 | 连接关闭码 8（主机方踢出该会话）写入 `protocol.md` 并被客户端识别；`Client.Stats`（RTT / 帧率 / 码率）。线上格式不变。versionCode 200，zip sha256 `a4f8e3d971b8b4fa3b6d660fe360e93857f2952b487f6169324177ccd4b36960`；从发布提交 `c9b3e21` 重建，两次构建字节一致 |
| 0.2.1 | 2026-10-28 | v0 + v1 | 1 | 配对码比较同 go-nx 0.7.1（忽略空格 / 短横、全角数字）；`nx-host` 的 `refused` 日志行多了被拒原因（发来的是码 / 密码 / 无、配对状态、是否设了主机密码）。控制接口与线上格式不变。versionCode 201，zip sha256 `becddcdf786ea4c472aa01204d2fd84c4e8841e52191e71d51fc897dd4629de6`（未做重建比对） |

**协议兼容矩阵**（`remote` 的测试逐对验证过，`protocol.md` 有详表）：任一版本的 v0 查看器能连任一版本的主机；v1 查看器连 v0 主机时回落到 v0（不能声明能力，所以文本 / 横向滚轮需要调用方明确 `AssumeCaps`）；v1 的主机接受 v0 与 v1 两种查看器。NightDesk 已发布客户端使用的 v0 扩展（`MsgText = 4`、`KindWheelH = 7`）是 v0 规范的一部分，不会被移除。

## 2. 产物 ↔ 契约

| 产物 | 契约 | 当前版本 | 变化规则 |
|---|---|---|---|
| `go-nx.exe` | 线上协议（上表） | 见 `VERSION` | 发布到 release-publish，签名 + SHA-256；自更新用 |
| `nx-host.exe` | stdin/stdout JSON Lines，`docs/nx-host.md` | **schema 1**（核心版本见 `CORE_VERSION`） | 新增命令 / 事件 / 字段不升 schema；删除名字或改变含义才升。核心版本在内容变了时由 `ship.sh --core` 递增 |
| `nx-probe.exe` | 命令行 + 一行 JSON，`internal/probe`（`schema` 字段） | **schema 1** | 同上；退出码 0 通过 / 1 失败 / 2 用法错误 |
| `go-nx-core.aar`（`mobile`） | Java API 固定在 `mobile/api.golden`，`docs/mobile.md` | 见 `api.golden`（34 行） | CI 逐行比较；改 API 必须同时更新 `api.golden`；**AAR 实际构建待有 Android 工具链时验证** |
| 协议字节 | `docs/protocol.md` + 金样字节向量测试 | v0、v1 | v0 冻结；v1 新增消息类型不破坏旧对端（未知类型被跳过） |

`nx-host.exe`、`nx-probe.exe` 作为**单独的应用 `go-nx-core`** 发布到 release-publish（zip，含契约文档；发布流程见 `release.md`）。也可以用 `scripts/artifacts.sh core` 从某个提交自己构建，并用 `SHA256SUMS` / `artifacts.json` 核对。

### NightDesk 侧（只记录，现在不改动）

NightDesk 以后要改用这些产物时，要核对（`backport-ledger.md` 有逐项差异）：

- 连接 go-nx 主机：它的 Android / WPF 客户端用 v0 + 扩展，能连任何 0.4.0 及之后的 go-nx 主机，也能连 v1 之前的（文本 / 横向滚轮在更早的 go-nx 主机上不被支持，会结束会话——它只应连带这些扩展的主机）。
- 改用 `nx-host.exe`：命令加了 `id`，事件的 `listen` 是实际地址、`disconnected.reason` 是稳定的词；它读事件的程序必须一直读（控制端 30 s 不读事件，主机会退出码 3）。
- 改用 `mobile` AAR：`NewSession` 的五参数签名保留；第一次 `Move` 之前的按钮 / 滚轮动作现在是错误；`Close()` 在后台完成。

## 3. 可复现构建

**同一个提交、同一版 Go、同样的参数，得到逐字节相同的文件。** 参数：`CGO_ENABLED=0`、`-trimpath`（去掉构建机的路径）、`-ldflags "-s -w -X main.version=$VERSION"`，窗口程序（`go-nx`）再加 `-H=windowsgui`。图标资源 `cmd/go-nx/rsrc_windows_amd64.syso` 是提交进仓库的，所以不依赖 `windres`。

```sh
scripts/artifacts.sh [outdir]      # 构建全部程序（Windows/amd64），写 SHA256SUMS 和 artifacts.json
scripts/artifacts.sh --verify      # 用两个空的构建缓存各构建一次，比较哈希（约 30 秒，12 核）
```

`artifacts.json` 记录：版本、提交、工作树是否有未提交改动（`dirty`）、Go 版本、目标、构建参数、每个文件的大小和 SHA-256。**`dirty: true` 的产物不能再现**，因为它依赖没提交的内容。

2026-10-28 在提交 `9231d2b` 上验证过（Go 1.27.3，Linux/amd64 交叉编译）：两次空缓存构建的哈希相同——

```
c02680e614072fd82e95f3b556fd35a68950e26adceeeb6ca47e293e3ad33d03  go-nx.exe
c1ca060756e4734f290120fc91fb592eff6ca5e760be0e9dcbf4707a54205be9  nx-host.exe
7420000ae433cb46b4702f9d988b7cbf520057b2f018f2c1d1d063ada66f177e  nx-probe.exe
```

（这些哈希对应的是那个提交加上当时工作树里未提交的内容，只作"两次相同"的证据，不是发布物的哈希；发布物的哈希在 `release.md` 的发布记录和 `artifacts.json` 里。）

没有验证的：换一台机器 / 换一个 Go 小版本是否仍然相同（Go 的编译器版本会进入二进制，所以不同版本不同是预期的）；Windows 本机构建与 Linux 交叉编译是否相同。

### 回滚

发布的版本不可覆盖。发错了：递增版本重新发布，必要时 `relhub yank` 撤回那个版本（`release.md`）。已经安装了坏版本的机器，更新器会保留旧版本为 `go-nx.exe.old` 直到新版本运行满 30 秒；被打断的更新，直接运行 `go-nx.exe.old` 会把它自己改回 `go-nx.exe`（`release.md`、`ACCEPTANCE.md` H26）。
