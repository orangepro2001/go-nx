# internal/relhub

release-publish 的客户端 SDK 副本，用于 go-nx 自更新（`internal/update`）。

- 来源：`/home/tony/dev/release-publish/pkg/relproto`、`pkg/relclient`（该目录不是 git 仓库，没有提交号）
- 复制日期：2026-10-28
- 复制范围：仅非测试文件（`*_test.go` 依赖服务端 `internal/` 包，无法在这里编译）
- 唯一改动：import 路径 `relhub/pkg/...` → `github.com/orangepro2001/go-nx/internal/relhub/...`；`relproto/manifest.go` 里的 `signaturePrefix = "relhub-release-v1"` 是协议字符串，**不要改**
- 只依赖标准库，所以 `go.mod` 不需要变化

同步方法：重新复制两个目录、重做 import 替换，然后跑 `go test ./internal/update/` 和 `scripts/e2e-update.sh`。
