package handlers

import (
	"errors"
	"html/template"
	"net/http"
	"strconv"

	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// T-acct-005 手动登录一次性跳转页：管理员经权限门创建令牌（明文只回显一次），
// 浏览器携令牌打开跳转页；页面消费令牌后 meta-refresh 到目标站，
// 由用户在上游站内手动完成登录（含验证码），不经octopus代理凭据。

// jumpPageTemplate 目标 URL 经 html/template 自动转义，杜绝注入。
var jumpPageTemplate = template.Must(template.New("jump").Parse(`<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="referrer" content="no-referrer">
<meta http-equiv="refresh" content="0; url={{.TargetURL}}">
<title>正在跳转</title>
</head>
<body>
<p>正在跳转到 <a href="{{.TargetURL}}" rel="noreferrer noopener">{{.Kind}}</a> 登录页…</p>
<p>本跳转链接为一次性令牌，已即时失效。若页面未自动跳转，点击上方链接前请注意链接仅可使用一次。</p>
</body>
</html>
`))

func init() {
	// 创建/审计接口走 Admin 端口 + Auth 权限门。
	router.NewGroupRouter("/api/v1/account/jump").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(createJumpToken),
		).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listJumpTokens),
		)
	// 跳转页无状态凭 cookie：令牌本身即能力凭证，不挂 Auth。
	// 只挂 Admin 端口；一次性 + 120s TTL + 消费即失效兜底。
	router.NewGroupRouter("/api/v1/account/jump/go").
		ServeOn(router.ServerAdmin).
		AddRoute(
			router.NewRoute("/:token", http.MethodGet).
				Handle(goJumpToken),
		)
}

type createJumpTokenRequest struct {
	Kind      string `json:"kind"`
	TargetURL string `json:"target_url"`
}

func createJumpToken(c *gin.Context) {
	var req createJumpTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	// 权限门口径：octopus 管理员为单用户体系，JWT 校验已通过，操作人取当前账号名。
	actor := op.UserGet().Username
	plain, token, err := op.NewJumpToken(nil, req.Kind, req.TargetURL, actor)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{
		"token":      plain,
		"expires_at": token.ExpiresAt,
		"kind":       token.Kind,
	})
}

func listJumpTokens(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	tokens, total := op.ListJumpTokens(nil, limit, offset)
	resp.Success(c, gin.H{"items": tokens, "total": total})
}

func goJumpToken(c *gin.Context) {
	token, err := op.ConsumeJumpToken(nil, c.Param("token"))
	if err != nil {
		switch {
		case errors.Is(err, op.ErrJumpTokenNotFound):
			// 统一 404：不区分"不存在/已被消费"，避免向持伪令牌者回显状态。
			resp.Error(c, http.StatusNotFound, "jump link invalid")
		case errors.Is(err, op.ErrJumpTokenConsumed):
			resp.Error(c, http.StatusGone, "jump link already used")
		case errors.Is(err, op.ErrJumpTokenExpired):
			resp.Error(c, http.StatusGone, "jump link expired")
		default:
			resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		}
		return
	}
	c.Header("Cache-Control", "no-store")
	// no-referrer：跳转页 URL 含一次性令牌，meta-refresh 到目标站不得带 Referer，
	// 否则已消费的令牌连同 admin 端口地址一起泄露给上游。
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(http.StatusOK)
	if err := jumpPageTemplate.Execute(c.Writer, gin.H{
		"TargetURL": token.TargetURL,
		"Kind":      token.Kind,
	}); err != nil {
		return
	}
}
