package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// T-acct-001 官方账号授权接入：authorize/callback/读取三接口 + 列表，
// 全挂 Admin 端口 + Auth 权限门（口径沿 jump）。
// 凭据明文与密文都不出响应：模型侧 json:"-" 双保险，这里只回显账号元数据。

func init() {
	router.NewGroupRouter("/api/v1/account/official").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/authorize", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(officialAccountAuthorize),
		).
		AddRoute(
			router.NewRoute("/callback", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(officialAccountCallback),
		).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(officialAccountList),
		).
		AddRoute(
			router.NewRoute("/usage/:id", http.MethodPost).
				Handle(officialAccountUsage),
		)
}

func officialAccountAuthorize(c *gin.Context) {
	var req model.OfficialAccountCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	account, authorizeURL, state, err := op.OfficialAccountAuthorize(nil, req.Provider)
	if err != nil {
		switch {
		case errors.Is(err, op.ErrOfficialKeyMissing):
			resp.Error(c, http.StatusServiceUnavailable, "official credential cipher key not configured (set OCTOPUS_OFFICIAL_KEY)")
		default:
			resp.Error(c, http.StatusBadRequest, err.Error())
		}
		return
	}
	resp.Success(c, gin.H{
		"account":       account,
		"authorize_url": authorizeURL,
		"state":         state,
		"expires_in":    int(op.OfficialStateTTL().Seconds()),
	})
}

func officialAccountCallback(c *gin.Context) {
	var req model.OfficialAccountCallbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	account, err := op.OfficialAccountCallback(nil, req.AccountID, req.OneTimeCode, req.State)
	if err != nil {
		switch {
		case errors.Is(err, op.ErrOfficialStateUnknown):
			resp.Error(c, http.StatusBadRequest, "oauth state invalid")
		case errors.Is(err, op.ErrOfficialStateExpired):
			resp.Error(c, http.StatusGone, "oauth state expired, restart authorization")
		case errors.Is(err, op.ErrOfficialAccountNotFound):
			resp.Error(c, http.StatusNotFound, "account not found")
		default:
			resp.Error(c, http.StatusBadGateway, err.Error())
		}
		return
	}
	resp.Success(c, account)
}

func officialAccountList(c *gin.Context) {
	accounts, err := op.OfficialAccountList(nil)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		return
	}
	resp.Success(c, gin.H{"items": accounts, "total": len(accounts)})
}

func officialAccountUsage(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid account id")
		return
	}
	account, err := op.OfficialAccountReadUsage(nil, id)
	if err != nil {
		switch {
		case errors.Is(err, op.ErrOfficialAccountNotFound):
			resp.Error(c, http.StatusNotFound, "account not found")
		default:
			resp.Error(c, http.StatusBadGateway, err.Error())
		}
		return
	}
	resp.Success(c, account)
}
