package hostd

import (
	"os"

	"github.com/orangepro2001/go-nx/internal/proto"
)

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }

func moveTo(x, y int32) proto.Input { return proto.Input{Kind: proto.KindMouseMove, X: x, Y: y} }
