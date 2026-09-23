package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// T-usability-009 分组使用情况接口。
//
// 生产实测：418 个分组里只有 41 个被调用过（94% 从未使用），
// 而它们**全部**是客户端可直接调用的模型名 —— 用户在 AI 客户端里会看到 418 个条目。
//
// 本接口只陈述事实（谁在用、用多少次、最后一次何时），
// **不给"建议删除"的结论**：未被调用不等于该删（备用分组、待启用分组都合法地没有流量）。
// 本项目在 T-usability-006 上刚踩过这个坑 —— 把「清单未列出」当成「不可用」，
// 差点让用户删掉有效配置。结论性判断留给掌握上下文的人。
func groupUsage(c *gin.Context) {
	window := 5000
	if raw := strings.TrimSpace(c.Query("window")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
			return
		}
		window = parsed
	}
	stats, err := op.GroupUsageStats(c.Request.Context(), window)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, stats)
}

func init() {
	router.NewGroupRouter("/api/v1/group").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/usage", http.MethodGet).
				Handle(groupUsage),
		)
}
