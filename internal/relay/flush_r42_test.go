package relay

import (
	"os"
	"strings"
	"testing"
)

// R42-F1: 源码守卫测试：断言 handler.go 在非流式写入客户端成功后执行 Flush，杜绝缓冲区残留
func TestNonStreamResponseFlushes(t *testing.T) {
	bytes, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	content := string(bytes)
	idxWrite := strings.Index(content, `c.Writer.Write(result.body)`)
	if idxWrite == -1 {
		t.Fatalf("未找到 Write(result.body)")
	}
	sub := content[idxWrite : idxWrite+150]
	if !strings.Contains(sub, `c.Writer.Flush()`) {
		t.Fatalf("非流式写出未在 Write 成功后调用 Flush(): %s", sub)
	}
}
