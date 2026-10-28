# 交接：新界面（U1–U4）、动态帧率、核心单独发布

- 日期：2026-10-28（KST）
- 用户的请求："开始实现ui。核心文件作为单独应用发布，要动态帧率。"
- 结果：三件事都完成并发布。**线上：`go-nx` 0.7.0（versionCode 700）、`go-nx-core` 0.2.0（versionCode 200）。**
- 没有做的事、没有验证的事见第 5、6 节。**新界面没有在用户的 PC 上由人用过**。

## 1. 发布了什么

| 应用 | 版本 | versionCode | 发布提交 | 内容 | sha256 |
|---|---|---|---|---|---|
| `go-nx` | 0.6.0 | 600 | — | 动态帧率 | `bd41bd793243fb12653f69ac1dea5800f622bbc01b578fb1fc288b3bd1eafc16` |
| `go-nx-core` | 0.1.0 | 100 | `9226ff8` | 核心第一次单独发布（zip，6.4 MiB） | `d3bf542e8b307f27132b12bbbd41b90783f83f40ca5d7ba4fb6f1024c34bdfbb` |
| `go-nx` | **0.7.0** | 700 | `0c8ec1d` | 新界面（U1–U4），19.3 MiB | `d6c77efdc2106e37610bd08b549591461cbe2fdc232e747e023779958aade018` |
| `go-nx-core` | **0.2.0** | 200 | `c9b3e21` | 关闭码 8、`Client.Stats`，6.4 MiB | `a4f8e3d971b8b4fa3b6d660fe360e93857f2952b487f6169324177ccd4b36960` |

每个版本都是 `scripts/ship.sh` 发的：提交 → 推送 → 等这个提交的 CI 绿 → `release.sh` → `relhub check` 验证（旧版本被提供新版本、新版本是最新）。核心 0.2.0 另外用 `scripts/artifacts.sh --verify core` 从发布提交重建了两次，字节一致。

### 动态帧率（0.6.0）

- 画面在变：10 fps；控制端有输入：30 fps，持续 2 s；静止（没发帧也没输入）3 s 后：2 fps。控制端的输入立刻唤醒抓屏循环并马上提高帧率，发完一帧后立即重新评估。
- 参数：`remote.Options.IdleFPS/IdleAfter`、`remote.Rates{Base,Active,Idle}`（`MaxActiveFPS` 60）；`nx-host -idle 2`；`go-nx -fps -fps-active -fps-idle`；设置项 `fps_mode`（`dynamic` / `fixed`）在每次会话开始时读取。
- 代码：`internal/remote/{host.go,rates.go}`、`internal/hostd/hostd.go`、`cmd/nx-host/main.go`；测试 `rates_test.go`、`dynamic_test.go`（真实 QUIC 回环）。`ACCEPTANCE.md` H30 是让你在自己的 PC 上用任务管理器看三种情形。

### 核心单独发布（`go-nx-core`）

- 应用 id `go-nx-core`，平台 `win-x64`，版本在 `CORE_VERSION`，与 `VERSION` 无关。
- 发布物是确定性的 zip（`tools/mkzip`：条目排序、mtime 取提交时间、固定权限）：`nx-host.exe`、`nx-probe.exe`、`SHA256SUMS`、`artifacts.json`、`docs/nx-host.md`、`docs/protocol.md`。
- 脚本：`release-core.sh`、`scripts/artifacts.sh [core] [--verify [core]]`、`scripts/core-changed.sh`（0 有变 / 1 没变 / 2 出错，用 `go list -deps` + `git diff`，从上一个 "Release core" 提交算起）、`scripts/ship.sh --core`（核心没变时拒绝发布）。CI 有"核心可复现"一步。
- 发完 `go-nx` 后 `ship.sh` 会提示核心有没有变，这次提示了，所以接着发了 0.2.0。

### 新界面（0.7.0）

- **结构**：`internal/ui/{theme,i18n,text,kit,gfx}` 是工具箱；`internal/app` 是可移植的整个应用（页面、对话框、会话、连接流程，对外只依赖 `services.go` 里的接口）；`cmd/go-nx` 只剩 Windows 适配（主机服务、剪贴板 / 注册表主题 / 系统语言 / 深色标题栏、托盘、更新器）。旧 `internal/ui/*.go`、`cmd/go-nx/{tabs,viewer,keys}_windows.go` 已删除。
- **语言**：简体中文 / English / 한국어，跟随系统，可手动切换；一张字符串表（每个键三种语言），具名占位符、复数、测试禁止 printf 动词和韩文"은(는)"式猜测。
- **主题**：蓝色，深色 / 浅色，对比度有测试。
- **渲染**：按需渲染（`FPSModeVsyncOffMinimum` + `ScheduleFrame`），界面层缓存纹理，只在树版本 / 动画 / 唤醒变化时重绘。一个 SDF 着色器画填充、描边和阴影；31 个矢量图标。
- **会话视图（U4）**：悬浮工具条（把手、3.5 s 自动收起、三档宽度）、适应 / 100% 与平移（边缘自动平移）、全屏、发送文本（仅当主机有 `CapText`）、释放按键、한/영 与 한자 键、统计覆盖层、修饰键角标、基于时钟的按键重复（500 ms 后每 33 ms）、`Ctrl+Alt+Q/←/→/1-9/F/Z/R/T/I/B`、网络类错误后自动重连（等 1/2/4/8 s，最多 15 次，保留最后一帧）、新关闭码 8（被主机方踢出，不重连）。
- **0.7.0 里的最后一个小改动**（提交 `b2d1e29`）：已配对设备列表不再显示对方连接时用的端口（`app.ParsePaired`，有测试）；"见过的对端"表有上限；Windows 剪贴板 / 系统语言 / 主题的调用有只在 Windows 上编译的测试（`cmd/go-nx/platform_windows_test.go`，CI 的 Windows 作业里跑；日志里只看到该包 `ok`，**没有看到剪贴板那条测试是跑了还是因为会话里打不开剪贴板而跳过**）。

## 2. 怎么验证的

| 层 | 内容 | 在哪跑 |
|---|---|---|
| 布局 | 全部页面 × 四种语言（含伪语言 `xx`）× 四种窗口尺寸，不溢出、不重叠 | Linux 与 Windows CI |
| 行为 | 各流程用假的主机 / 连接 / 平台（会话结束变错误卡片、重连、更新、设置、撤销…），含 `-race` | Linux 与 Windows CI |
| GPU | 颜色与朝向、形状位置（每张图）、裁剪后形状不位移 | Xvfb（Linux）与 `windows-latest` |
| 真实程序 | 真实 `go-nx.exe` 在 Windows 运行器上启动到第一帧（阻塞的冒烟测试） | `windows-latest` |
| 截图 | `tools/uipreview` 渲染每个界面，artifact `ui-linux`、`ui-windows`（真实字体）；我看过 Windows 的图 | CI |
| 核心 | 协议兼容矩阵、`nx-probe` 端到端、zip 可复现 | Linux CI |

## 3. 跑真程序找到的真实 Windows 问题（单元测试发现不了）

1. **没有第一帧**：最小 FPS 模式下，窗口出现之前的 `ScheduleFrame` 会丢。修复：`kickFirstFrame` 每 25 ms 请求一次直到第一次 `Update`。冒烟测试原来是 `continue-on-error`，把它藏住了；现在是阻塞的，实测第一次 Update +240 ms、第一次 Draw +269 ms。
2. **裁剪区域里的形状位移**：DirectX 上子图作为目标会改变着色器里 `dst.xy` 的原点。修复：形状总是画在根图上，只与裁剪矩形求交，从不画在 SubImage 上；回归测试 `TestAClippedShapeIsNotShifted`。
3. **U0 漏掉的着色器原点错误**：着色器里 `dst.xy` 是纹理坐标，必须减去 `imageDstOrigin()`，否则只有一轮绘制里的第一张图上的形状位置对。已在 `ui-baseline.md` 里作为更正记录；回归测试 `TestShapesLandInTheRightPlaceOnEveryImage`。
4. 其他：`reconnectable` 把所有 quic 错误当成网络错误（现在先判 `ApplicationError` 的码，再判空闲 / 重置 / `*net.OpError`）；`pollTabs` 改变了标签状态却没让界面失效（会话结束后界面还是旧的，Linux CI 里的测试抓到了）；GPU 测试进程退出时 Mesa 崩溃（改成让主循环自己结束、在主线程退出）。

## 4. 用户的决定（本次）

- 开始实现 UI（预算：英文界面 ≤ 45 MB，中 / 韩 ≤ 80 MB，之前提出的，没有异议）。
- 核心文件作为单独应用发布（所以有了 `go-nx-core`）。
- 要动态帧率。
- 运行环境变化：release-publish 现在是 **podman 容器** `release-publish`（`~/dev/release-publish/deploy/container.sh`），systemd 单元不再用；`relhub` 不变；健康检查 `127.0.0.1:18790`。这不是我做的决定，是发布时发现的现状。

## 5. 没做

- U5 / U6 的其余部分：光标捕获、着色器做 BGRA→RGBA、高对比度、性能回归门槛进 CI、`Ctrl+K` 快速切换、设备在线状态、"停止共享"开关。
- 韩文翻译需要母语者校对（有测试挡住明显的助词猜测，但不等于译文自然）。
- 阶段 5 的 H6（光标闪烁 A/B）仍等用户。
- AAR 的完整构建仍没验证（本机没有 Android SDK / NDK）。

## 6. 没验证（需要用户在自己的 PC 上做）

`docs/ACCEPTANCE.md`：

- **H29** U0 在真实 GPU 与输入法上；
- **H30** 动态帧率：任务管理器里看 CPU；
- **H31** 新界面的基本使用；
- **H32** 输入法与韩文（预编辑文字、候选窗位置）；
- **H33** 125% / 150% / 200% 缩放与多显示器；
- **H34** 会话视图（工具条、适应 / 100%、全屏、发送文本、重连）。

这些全部未在用户的 PC 上执行过，没有一条可以写成"已验证"。更新到 0.7.0 后再做。

## 7. 怎么接着做

- 先读 `docs/context-recovery.md`，再读 `docs/ui-redesign.md` 第 13 节（待定问题）和 `ROADMAP.md`。
- 改 UI：`go test ./internal/ui/... ./internal/app`（GPU 测试在 Linux 上需要 `xvfb-run`）；`go run ./tools/uipreview -out DIR` 出截图；Windows 的行为用 CI（`windows-latest`）验证，不要指望 Wine 画 DirectX。
- 发布：提交并推送 → 工作树干净 → `scripts/ship.sh --bump minor|patch "说明"`（后台运行，**期间不要改仓库文件**）。行为改动才发；文档 / CI / 测试的提交不发。核心有变化时 `ship.sh` 末尾会提示，再跑 `scripts/ship.sh --core --bump ...`。
- 推送后先看 CI 再推下一个（CI 会取消被顶掉的运行）。
