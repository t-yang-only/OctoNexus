package relay

import "github.com/gin-gonic/gin"

// FinishKeyUsage 由转发终态调用, 把请求实际词元量交给鉴权中间件注册的记账回调。
// 回调未注册 (非转发路径/测试环境) 时静默跳过; 同一请求重复调用只记一次。
func FinishKeyUsage(c *gin.Context, promptTokens, completionTokens int64) {
	if c == nil {
		return
	}
	recorderAny, ok := c.Get("key_usage_recorder")
	if !ok {
		return
	}
	if recorder, ok := recorderAny.(func(int64)); ok {
		recorder(promptTokens + completionTokens)
	}
}
