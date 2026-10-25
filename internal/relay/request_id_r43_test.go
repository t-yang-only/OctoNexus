package relay

import (
	"os"
	"strings"
	"testing"
)

// TestRequestIDHeaderInvariant 确保网关响应中严格包含 X-Octopus-Request-Id 头，用于端到端请求溯源
func TestRequestIDHeaderInvariant(t *testing.T) {
	bytes, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("读取 handler.go 失败: %v", err)
	}
	content := string(bytes)

	if !strings.Contains(content, `const requestIDHeaderName = "X-Octopus-Request-Id"`) {
		t.Fatalf("handler.go 缺少常量定义 requestIDHeaderName = \"X-Octopus-Request-Id\"")
	}

	occurrences := strings.Count(content, `c.Writer.Header().Set(requestIDHeaderName, strconv.FormatUint(request.ID, 10))`)
	if occurrences < 2 {
		t.Fatalf("handler.go 必须在 publishDecision 和 failRequest 中均包含 c.Writer.Header().Set(requestIDHeaderName, ...)，当前仅找到 %d 处", occurrences)
	}
}
