package health

import "context"

// probeTestContext 返回测试用背景上下文（探测器签名统一 context.Context）。
func probeTestContext() context.Context {
	return context.Background()
}
