package middleware

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
)

// jumpTokenPathPrefix 的末段是一次性跳转令牌的明文，按 R-sec-001 红线（日志/输出
// 绝不出现凭据）不落任何日志。格式与 gin 默认 formatter 一致，仅多这一步掩码。
const jumpTokenPathPrefix = "/api/v1/account/jump/go/"

func redactLogPath(path string) string {
	if idx := strings.Index(path, jumpTokenPathPrefix); idx >= 0 {
		return path[:idx+len(jumpTokenPathPrefix)] + "[REDACTED]"
	}
	return path
}

func Logger() gin.HandlerFunc {
	return gin.LoggerWithConfig(gin.LoggerConfig{
		Formatter: func(param gin.LogFormatterParams) string {
			// 字段与 gin 默认 formatter 对齐（method/path 双引号），仅多一步掩码，
			// 避免审计噪音之外的行为漂移。
			return fmt.Sprintf("[GIN] %s | %3d | %13v | %15s | %-7q %q\n",
				param.TimeStamp.Format("2006/01/02 - 15:04:05"),
				param.StatusCode,
				param.Latency,
				param.ClientIP,
				param.Method,
				redactLogPath(param.Path),
			)
		},
	})
}
