package handlers

import (
	"net/http"

	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/gin-gonic/gin"
)

// T-usability-012 「资源不存在」的状态码映射。
//
// ## 为什么需要
//
// 审计发现删除不存在的资源时返回 **500**：
//
//	DELETE /api/v1/apikey/delete/999999   → 500 "API key not found"
//	DELETE /api/v1/channel/delete/999999  → 500 "channel not found"
//	DELETE /api/v1/group/delete/999999    → 500 "group not found"
//
// 语义错了：500 表示"服务端故障、可重试"，而"这个资源不存在"是确定的、
// 不该重试的结论。调用方看到 500 会去重试或报"服务器错误"，
// 把明确的"东西没了"误报成故障。
//
// ## 为什么只用在写路径
//
// **不改读取路径**：读一个不存在的资源返回空是常态（列表为空、详情未填），
// 一律 404 会让"正常空态"看起来像错误。
// 所以这个函数只用于 delete/update 这类**要求目标必须存在**的操作。
//
// ## 用法
//
//	if err := op.XxxDelete(id); err != nil {
//	    writeOpError(c, err)   // 自动回 404 或 500
//	    return
//	}
func writeOpError(c *gin.Context, err error) {
	if op.IsNotFound(err) {
		// 透传错误原文而不是换成通用文案：
		// op 层的消息已带了"什么没找到"（如 "API key 999999 not found"），
		// 换成通用的 "not found" 反而丢了可执行信息。
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Error(c, http.StatusInternalServerError, err.Error())
}
