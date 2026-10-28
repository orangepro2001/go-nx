# mobile：手机端查看器核心（gomobile）

包 `mobile` 把 go-nx 的**控制端**核心——QUIC、证书固定、配对、tile 解码、输入编码——暴露给 Android，通过 `gomobile bind` 生成 Java 接口。它不含任何 Android 专用代码：Activity、绘制表面、输入法、通知由应用负责。

Java 侧的公开 API 固定在 `mobile/api.golden`，CI 里 `scripts/build-aar.sh --check-api` 生成绑定并逐行比较；有意改 API 时运行 `scripts/build-aar.sh --update-golden` 并提交。

## 构建

```
scripts/build-aar.sh --check-api       # 只生成 Java 绑定并和 api.golden 比较；不需要 SDK/NDK（CI 跑这个）
scripts/build-aar.sh                   # 生成 build/go-nx-core.aar；需要 gomobile、ANDROID_HOME、ANDROID_NDK_HOME
```

脚本在**工作树的临时副本**里给 `go.mod` 加上 `golang.org/x/mobile`（版本由 `MOBILE_VERSION` 固定），所以本仓库的 `go.mod` 不变、主程序的依赖图不会因它多出东西。它不安装任何东西——`gomobile`/`gobind` 要事先在 PATH 里。

> **状态**：API 绑定检查在 Linux 上跑过并通过。**完整的 AAR 构建在这台机器上没有验证过**（没有 Android SDK/NDK）；第一次真正构建时请按脚本的提示准备环境，并把结果回填到 `ACCEPTANCE.md`。

## 像素约定

每个 tile 以紧密排列的 **B,G,R,X** 字节到达（Windows 主机抓屏的顺序）。**第 4 个字节没有定义，可能是 0**——不能当 alpha 用。要画 RGBA 位图的应用自己交换红蓝并把 alpha 置 255（参考实现 `view.BGRAToRGBA`，有字节级的测试）。`OnTile` 的 `pixels` **只在回调返回前有效**，不要保留。

一次更新是一次 `OnFrameBegin(width, height)` 加每个脏矩形一次 `OnTile`。第一次更新是整幅画面，之后只有变化的部分。尺寸可能在会话中变化（主机改分辨率或换显示器）：每次 `OnFrameBegin` 都带当前尺寸。

## 位置约定

主机收到的每个指针动作都带**位置**，而不是"在原来的位置"：

- `Move(x, y)` 记住最后位置（夹到画面内；画面尺寸未知前只夹到 32 位整数范围）。**被拒绝的移动不改变记住的位置**。
- `MouseDown`、`MouseUp`、`Wheel`、`WheelH` 都用最后一次 `Move` 的位置。**在第一次 `Move` 之前做这些动作返回错误**（没有位置可用；用角落代替就是"松开按钮时主机指针跳到左上角"的那个老 bug）。
- `ReleaseAll` 的鼠标抬起也带最后位置（不用 `NoMove`，因为 `NoMove` 之前的主机会把它当成坐标夹到角落）。
- 键盘事件没有位置。

## 释放与关闭

- `ReleaseAll()`：先放所有按住的鼠标键，再放所有按住的键，**各自后按的先放**（Ctrl+Shift+点击会被放成：点击、Shift、Ctrl）；顺序是确定的，不随 map 遍历。再调一次什么都不发。
- `Close()`：**立刻返回**。它先 `ReleaseAll`，然后在后台让这些释放到达主机再关连接——客户端把控制流写完、发 FIN，等主机读完并挂断（至多约 1.3 s），而不是写完就 `CloseWithError`（那会丢掉已写入但还没送达的数据）。完成时收到 `OnState("closed")`。主动关闭**不**产生 `OnError`。可重复调用。
- 查看器失去焦点时应用调 `ReleaseAll()`；应用被杀时主机的看门狗在约 20 s 后放开残留的键。

## 回调与线程

回调在 Go 的工作线程上被调用；应用必须把界面工作交给主线程。**回调里 panic 会被吞掉**，不会拖垮会话或进程。输入方法可以从任意线程并发调用（内部有互斥）。

状态：`connecting`（开始拨号）→ `streaming`（收到第一幅画面）→ `closed`。错误码（`OnError`）：`untrusted`、`bad-code`、`replaced`、`locked`、`revoked`、`busy`、`unverified`（主机拒绝或结束会话）、`host-identity`（主机证书和给定指纹不符——**配对码没有发出去**）、`identity`（本机身份无法加载）、`disconnected`（其他，`message` 有说明）。码是稳定的词，`message` 是给人看的。

## 键盘与文本

- `KeyDown/KeyUp(vk)`：Windows 虚拟键码；`0x100|vk` 表示扩展键（`0x100|0x2d` 是 Insert）。范围 1–0x1ff。
- `Text(s)`：按字面输入 UTF-8 文本，**与主机键盘布局无关**，至多 1024 字节，返回前不等主机；主机用 `OnReceipt(opID, status)` 回报，`opID` 从 1 递增。主机没有声明文本能力（`CanText()` 为假）时返回错误，而不是发出去让对方结束会话。
- v0（原始协议）的主机不能声明能力。若你**确知**它支持文本和横向滚轮（NightDesk 的 nx-host 就是），调 `AssumeLegacyExtensions()`；对不支持的主机这样做会在第一次发文本时结束会话。

## 与 NightDesk 快照里的 mobile 包的差异

接口名与签名保持兼容（`NewSession` 同样的五个参数），区别：

- 新增 `NewSessionWithOptions(..., wantCursor, callback)`（让主机把指针画进画面）、`HostName()`、`ProtocolVersion()`、`CanText()`、`CanWheelH()`、`AssumeLegacyExtensions()`。
- 第一次 `Move` 之前的按钮 / 滚轮动作是错误（原来悄悄用 0,0）。
- 位置被夹到画面内（原来原样发出，超出 32 位的整数还会回绕）。
- `ReleaseAll` 顺序确定（原来遍历 map）。
- `Close()` 保证释放先到达主机（原来入队后立刻关连接，在异步发送队列下会丢）。
- 没有声明文本能力的 v1 主机上 `Text` 返回错误；v0 主机上默认也返回错误，除非调用了 `AssumeLegacyExtensions()`（原来对 v0 主机总是直接发）。
- 固定指纹经 `remote.DialOptions.ExpectFingerprint`：写法宽松（大小写、冒号、空格），格式错立刻报错，握手时就拒绝不符的主机。
