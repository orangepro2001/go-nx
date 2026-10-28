# U0：界面改造前的技术验证（基线）

- 日期：2026-10-28。工具：`tools/u0`（`go run ./tools/u0 all`）；CI 的 linux 和 windows 两个 job 都跑它并保留 JSON 报告和截图（artifact `u0-linux`、`u0-windows`）。
- **这是对 `docs/ui-redesign.md` 里假设的核对，不是界面实现**——界面仍然只有文档。每一项有数字或明确结论；"通过"指"与计划假设一致"，"推翻"指"计划要改"。
- 测量环境：
  - **Windows**：GitHub `windows-latest`，4 个逻辑 CPU，**DirectX 11、软件渲染**（runner 没有 GPU），交互式桌面，Go 1.27.3，Ebiten v2.10.5。
  - **Linux**：GitHub `ubuntu-latest` 和本机（i7-8700）上的 Xvfb + Mesa 软件 GL（OpenGL）。
  - **没有**用户 PC 上的真实 GPU 数据；那一项在 `ACCEPTANCE.md` H29。软件渲染让"每帧都画"比真实 GPU 更贵，所以空闲 CPU 的绝对值不能照搬；**相对关系和 API 行为**是这里要得到的。

## 结论一览

| # | 验证项 | 结论 |
|---|---|---|
| ① | 空闲 CPU、首帧、内存、tile 上传 | 只有 `FPSModeVsyncOffMinimum` + `ScheduleFrame` 稳定为 0%（两次运行都是 0.000%）；"跳过绘制"在 Windows runner 上是 0–2%（计时精度和噪声内），在 Linux 软件 GL 上 2.5%；**一个空的 Ebiten 窗口在 Windows 上就占 34 MB 私有内存 / 44 MB 工作集**；tile 上传 ≈ 8 µs，远低于 200 µs 预算 |
| ② | `ScheduleFrame` / `FPSModeVsyncOffMinimum` 在 v2.10.5 上是否按预期 | **通过**（Windows/DirectX 和 Linux/OpenGL）：空闲 0 帧 0% CPU；每次 `ScheduleFrame()` 恰好一帧，延迟 1–2 ms。已弃用但可用 |
| ③ | 字体存在性、大小、内存、三语渲染 | 三种字体在 runner 上都在，三语渲染正确；**用 `*os.File` 加载是对的**（从内存字节解析留下的更多：YaHei 94 MB 对 37 MB，Malgun 41 对 28，Segoe 3.7 对 2.6），但 **CJK 字体的常驻内存仍是文件大小的 1.5–2.2 倍**（YaHei 37 MB、Malgun 28 MB），60 MB 的内存预算在中 / 韩界面下**放不进去**，要改 |
| ④ | `exp/textinput` | **部分**：无输入法时注入的按键、字符和 Unicode 文本都原样到达（`hello한中`）；有输入法时的预编辑和候选窗**需要用户在装有中 / 韩输入法的机器上做**（H29） |
| ⑤ | VK_HANGUL / VK_HANJA | **证实**：Ebiten 没有这两个键，按下它们没有任何键事件也没有字符；韩文用户用工具条按钮发送 VK 的方案成立；经现有注入路径能否切换远端输入法**需要装有韩文输入法的远端**（H29） |
| ⑥ | SDF 着色器在 DirectX 后端 | **通过**：与 Go 里同一公式逐像素比较，最大差 1/255（WARP 和 OpenGL 一致）；编译 0.5 ms |
| ⑦ | Linux CI 截图 | **可行**：Xvfb + Mesa 下能离屏渲染、读回像素、写 PNG；已作为 CI artifact |

## ① 空闲、内存、上传

**每种画法在窗口空闲时的开销**（`u0 idle`，每种测 3 s，之前稳定 1 s；窗口 320×200，画一个蓝色圆角块）：

| 做法 | Windows runner（DirectX，软件渲染） | Linux runner（OpenGL，Mesa 软件） |
|---|---|---|
| 默认：垂直同步、60 TPS、每帧重画 | 17–20% 个核心（两次运行），60 次 Update、60 次 Draw / 秒 | 30.0% |
| 跳过绘制：`SetScreenClearedEveryFrame(false)`，`Draw` 提前返回，60 TPS | 0.0–0.5%，60 / 60 | 2.5% |
| 同上，`SetTPS(10)` | 0–2%（两次运行 0.00% 和 2.02%），10 次 Update、60 次 Draw / 秒 | 2.6% |
| `FPSModeVsyncOffMinimum`，不请求就不画 | **0 帧 / 秒，0.000%（两次都是）** | 0 帧 / 秒，0.009% |

要点：

- **CPU 计时精度**：Windows 的进程 CPU 时间走 15.6 ms 一格，3 s 的区间里分辨率约 0.5%，同一做法两次运行相差 0–2%，所以只有"一直是 0.000%"的最小模式是可靠的"零"。计划里的 0.3% 预算要在用户 PC 上用 60 s 才能判定（H29）。
- "跳过绘制"只省 GPU，**主循环仍按 60 帧 / 秒转**（Draw 照样被调用，只是提前返回）；降低 TPS 只降 `Update` 的频率，**不降 Draw 的频率**。在 Windows 软件渲染上这已经很低，在 Linux 软件 GL 上仍有 2.5%。
- **最省的是 `FPSModeVsyncOffMinimum` + `ScheduleFrame()`**：什么都不请求时一帧都不跑。它在 v2.5 起被标为弃用，但在 v2.10.5 的两个平台上都按文档工作（见 ②）。

**一个空的 Ebiten 窗口的内存**（Windows runner）：私有内存 **34 MB**，工作集 **44 MB**（Linux/Mesa 另说：私有 57 MB，常驻 137 MB，因为软件 GL 的缓冲）。这还没有任何界面层和字体。**计划的"空闲常驻 ≤ 60 MB"**：英文界面（Segoe UI 堆里 2.6 MB）≈ 37 MB 可达；韩文界面加 Malgun（堆里 28 MB）≈ 62 MB，刚好超；中文界面加 YaHei（堆里 37 MB）≈ 71 MB，超。真实 GPU 的驱动映射会让这个基底更大或更小，**必须在用户 PC 上量**（H29）；预算因此改了（见 ③ 末尾）。

**tile 与整幅画面的上传**（`u0 tiles`，Windows runner 的 Go 时钟一格 0.3 ms，所以用整段循环的平均值）：

| | Windows（DirectX） | Linux（OpenGL） | 计划预算 |
|---|---|---|---|
| 一个 64×64 tile 的 `WritePixels`（调用本身） | 7.8 µs | 6.8 µs | ≤ 200 µs ✔ |
| 同上，每 100 个后读回一个像素确认 GPU 收到 | 6.5 µs | 6.6 µs | |
| 整幅 1080p 的 `WritePixels`（含确认） | 0.56 ms | 0.63 ms | |
| 整幅 4K | 1.6 ms | 1.7 ms | |
| 把 1080p 画面 `DrawImage` 一次（调用本身） | 1.0 µs | 0.6 µs | |

软件渲染下这些是 CPU 拷贝的成本，真实 GPU 还要加上总线传输（一个 tile 16 KB，不会成为问题），所以**这一项通过**。

**首帧**：Windows runner 上 `render` 进程从启动到第一次 `Draw` 是 327 ms，其中包含打开 5 个字体（约 110 ms）；纯 Ebiten 初始化 + DirectX 软件适配器大约 200 ms。Linux CI 的软件 GL 要 3.2 s（不具代表性）。计划的 ≤ 300 ms 要在用户 PC 上量；**启动路径不读取 CJK 字体**这一条仍然必须。

## ② `ScheduleFrame`

`u0 idle` 的最后一步：切到 `FPSModeVsyncOffMinimum`，先看 4–6 s 里什么都不请求时跑多少帧，再请求 8 次 `ScheduleFrame()`，每次等到下一次 `Draw` 并数它之后又额外画了几帧。

| | Windows（DirectX） | Linux（OpenGL） |
|---|---|---|
| 空闲时每秒帧数 | 0 | 0 |
| 空闲时 CPU | 0.000% | 0.009% |
| 一次 `ScheduleFrame()` 引出的帧数 | 恰好 1.0 | 恰好 1.0 |
| 请求到 `Draw` 的延迟（中位 / 最大） | 1.2 / 2.0 ms | 1.2 / 1.4 ms |

**结论：计划的"按需渲染"可以直接用 `FPSModeVsyncOffMinimum` + `ScheduleFrame()`，包在一个地方**，降级方案（低 TPS + 跳过 `Draw`）已验证也可用，只是空闲时仍有 60 次 / 秒的 Draw 调用。注意在最小模式下"新的输入"也会触发帧（文档所述），所以界面不需要为鼠标 / 键盘手动调度，只在**没有输入但画面要变**时调用（连接状态变化、tile 到达、动画）。

## ③ 字体

Windows runner 上（`u0 fonts`；文件缓存已预热，所以只比较加载器怎么做；"留下的堆"是加载并保留结果之后的 `HeapAlloc` 增量）：

| 字体 | 文件 | 面数 | 从文件句柄加载（计划规定的） | 先读整个文件再从字节解析 | 覆盖样例 |
|---|---|---|---|---|---|
| Segoe UI `segoeui.ttf` | 0.9 MB | 1 | 5 ms，**+2.6 MB** | 5 ms，+3.7 MB | 是 |
| Segoe UI Semibold `seguisb.ttf` | 0.9 MB | 1 | 4 ms，+2.8 MB | 5 ms，+3.7 MB | 是 |
| Microsoft YaHei `msyh.ttc` | 18.8 MB | 2（YaHei、YaHei UI） | 100 ms，**+37.4 MB** | 84 ms，**+93.5 MB** | 是 |
| Malgun Gothic `malgun.ttf` | 12.8 MB | 1 | 34 ms，**+27.9 MB** | 31 ms，**+40.7 MB** | 是 |

三种字体都在 `windows-latest` 上（`C:\Windows\Fonts`），按文件名能找到；样例（简体中文 / 한국어 / 拉丁）每个字符都有字形。三语截图在 CI artifact `u0-windows` 里：拉丁用 Segoe UI，汉字用 YaHei UI，谚文用 Malgun，经 `text.MultiFace` 回退链，圆角块是 SDF 着色器画的。

**结论：**

- **计划规定的"用 `*os.File` 加载，不读整个文件"是对的。** go-text 的加载器按需从文件读表；先把整个文件读进内存再解析，会留下那份字节**加上**每个面各自复制的表，YaHei（2 个面）要 93.5 MB，比文件句柄的 37.4 MB 多 2.5 倍；单面的 Malgun 是 40.7 MB 对 27.9 MB；本机的 Noto 集合（10 个面）是 216 MB 对 20 MB——面越多差得越大。时间两者相同（YaHei 100 ms 对 84 ms，Malgun 34 对 31）。
- **但文件句柄加载也不便宜**：留在堆里的是文件大小的 **1.5–2.2 倍**（YaHei UI 18.8 → 37 MB，Malgun 12.8 → 28 MB，Segoe 0.9 → 2.6 MB），而不是计划"推断"的"MB 级"。一个 CJK 字体就是几十 MB。
- **`NewGoTextFaceSource` 对 `.ttc` 返回 `collections not allowed`**；集合要用 `NewGoTextFaceSourcesFromCollection`，它**把集合里每个面都解析一遍**，面越多越贵（`msyh.ttc` 2 个面，本机的 Noto Sans CJK 10 个面）。没有办法只解析一个面。
- **内存预算因此放不进去**：一个空的 Windows Ebiten 窗口已经是 34 MB 私有内存（①）；英文界面 + Segoe UI ≈ 37 MB；韩文界面 + Malgun ≈ 62 MB；中文界面 + YaHei ≈ 71 MB；两个 CJK 字体同时加载 ≈ 100 MB。计划的"空闲常驻 ≤ 60 MB"只有英文界面能满足。

**因此计划要改**（已写入 `ui-redesign.md`）：规则 `*os.File` 保留；**一次只常驻界面语言对应的那一个 CJK 字体**，其他语言的字体遇到它们独有的字符才加载，并在使用结束后可以释放；字体在**后台 goroutine** 里加载（YaHei 约 100 ms，超过原来的"首次使用 ≤ 50 ms"，但不在 UI 线程就不卡）；内存预算改为"英文界面 ≤ 45 MB，中 / 韩界面 ≤ 80 MB 私有内存"（**目标，待用户 PC 上用真实 GPU 驱动复测，H29**——软件渲染的基底 34 MB 在真实机器上可能更高或更低）。如果以后要压下这个数，唯一的办法是**制作子集字体**（只含常用字），但那要么内嵌（增加二进制）要么随程序安装，违背"单个静态 exe"，不在此处预设。

## ④⑤ 输入

`u0 input`（仅 Windows）：窗口拿到前台焦点后，用 `SendInput` 注入 `H E L L O`、F13、VK_HANGUL、VK_HANJA，再用 `KEYEVENTF_UNICODE` 注入 `한` 和 `中`。runner 没有输入法（US 键盘布局）。

- Ebiten 看到的按键：`H, E, L, L, O, F13`（还有焦点技巧用的 Alt）；`ebiten.AppendInputChars` 收到 `hello한中`；`exp/textinput` 的 `Field` 里也是 `hello한中`。**输入法关闭时按键原样到达，Unicode 文本也到达**——这是"会话中本地 IME 关闭时按键是否原样转发"的前半。
- VK_HANGUL（0x15）、VK_HANJA（0x19）**没有产生任何 Ebiten 按键事件，也没有字符**：Ebiten 的 `Key` 里没有这两个键（`ui-redesign.md` 之前的判断成立），韩文用户要靠工具条按钮发送这两个 VK。

**还没验证的**（需要装有中 / 韩输入法的机器，用户自行执行，见 `ACCEPTANCE.md` H29）：输入法开启时 `exp/textinput` 的预编辑文本、候选窗位置、提交；会话里本地输入法关闭后按键是否仍原样转发给远端；VK_HANGUL / VK_HANJA 经现有注入路径能否切换**远端**输入法。

## ⑥ SDF 着色器

`render`：一个 200×100 离屏图上用 Kage 着色器画圆角矩形（符号距离场，边缘 1 像素抗锯齿），读回像素，与 Go 里同一公式在每个像素中心的值逐通道比较。**两个平台最大差都是 1/255**（浮点舍入），所以 DirectX 的 WARP 和 OpenGL 的 Mesa 画出的是同一个形状。

> **更正（2026-10-28，实现 U1 时发现）：这个检查漏了一件事。** 着色器 `Fragment` 的第一个参数是**目标贴图（texture）上**的位置，而图片只是贴图的一部分；上面的比较只画了**一张**图，它恰好在贴图原点，所以通过了。实际界面一进程里有很多图，第二张起形状就画到别处（在 Xvfb/Mesa 上表现为"除第一张外整片透明"，正是写 `tools/uipreview` 时 90 张截图里一部分是空的才暴露）。修法是用 `imageDstOrigin()` 把原点减掉（`internal/ui/gfx/canvas.go`），回归测试 `TestShapesLandInTheRightPlaceOnEveryImage` 同时放着几张别的图来保证"不在原点"。**教训：对着色器的检查要覆盖"图片不在贴图原点"和"画在子图上"两种情形。**着色器编译 0.5 ms（Windows）/ 1.0 ms（Linux CI）。软件渲染上都能跑，**不需要"九宫格位图"降级**；真实老 GPU 和远程桌面会话下仍可能失败，降级方案保留为文档里的计划，待用户 PC 上验证。

## ⑦ Linux 上的截图

`xvfb-run -a u0 all -png scene.png`：Xvfb 提供 X 服务器，Mesa 的软件光栅（llvmpipe）提供 OpenGL，Ebiten 能创建窗口、渲染、`ReadPixels` 读回、编码成 PNG。CI 上额外装 `xvfb libgl1-mesa-dri libglx-mesa0 libegl1 libegl-mesa0`。**所以界面改造时可以在 Linux CI 里对每个界面状态截图并做像素比较**（U6 的回归测试）。局限：软件 GL 的像素与真实 GPU 可能有舍入差异，比较要用容差，不能逐字节相等；Linux 上没有 Windows 的字体，所以三语截图在 Linux CI 里只有拉丁字母真实可比，汉字和谚文只在 Windows job 的截图里正确。

## 对计划的影响（已同步到 `ui-redesign.md`）

1. **字体加载方式**：`*os.File` 的规则保留（实测证明它比读整个文件留下的更少，面越多差得越大）；只常驻界面语言那一个 CJK 字体；后台加载。
2. **预算**：首次用到某种 CJK 字体改为"≤ 100 ms，且不在 UI 线程"；"常驻内存"改为英文界面 ≤ 45 MB、中 / 韩界面 ≤ 80 MB 私有内存（一个空窗口在 Windows 上已是 34 MB，YaHei 加 37 MB，Malgun 加 28 MB），需在用户 PC 上用真实 GPU 驱动确认。
3. **按需渲染**：定为 `FPSModeVsyncOffMinimum` + `ScheduleFrame()`（封装在一处），降级为低 TPS + 跳过 `Draw`。
4. **三语渲染链、SDF 圆角、Linux 截图**：按原计划，已验证可行。

## 复现

```sh
go run ./tools/u0 fonts [-dir DIR | -file X.ttc -want "Family"]   # 不需要显示
xvfb-run -a -s "-screen 0 1920x1200x24" go run ./tools/u0 all -png scene.png -out report.json -check    # Linux
go run ./tools/u0 all -png scene.png -out report.json -check                                             # Windows（交互式桌面）
```

`-check` 只对**正确性**失败（某一部分没跑成、着色器和公式不符、有字体却画不出字、上传慢了几个数量级）返回非零；预算没达到不算失败，由人读摘要判断。
