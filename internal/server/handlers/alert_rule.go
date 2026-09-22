package handlers

import (
	"net/http"
	"strconv"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/internal/task"
	"github.com/gin-gonic/gin"
)

// 告警规则（吸收上游 lingyuins/octopus 的 Alerts，管理面）。
//
// 整组挂在 middleware.Auth() 之内：规则与触发历史会暴露"哪些渠道在坏、坏到什么程度"，
// 等于把上游拓扑与故障状况公开。
func init() {
	router.NewGroupRouter("/api/v1/alert-rule").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listAlertRule),
		).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Handle(createAlertRule),
		).
		AddRoute(
			router.NewRoute("/update/:id", http.MethodPost).
				Handle(updateAlertRule),
		).
		AddRoute(
			router.NewRoute("/delete/:id", http.MethodDelete).
				Handle(deleteAlertRule),
		).
		AddRoute(
			router.NewRoute("/evaluate", http.MethodPost).
				Handle(evaluateAlertRule),
		).
		AddRoute(
			router.NewRoute("/preview", http.MethodPost).
				Handle(previewAlertRules),
		).
		AddRoute(
			router.NewRoute("/run", http.MethodPost).
				Handle(runAlertRules),
		).
		AddRoute(
			router.NewRoute("/fires", http.MethodGet).
				Handle(listAlertFires),
		)
}

func listAlertRule(c *gin.Context) {
	resp.Success(c, op.AlertRuleList(c.Request.Context()))
}

func createAlertRule(c *gin.Context) {
	var req model.AlertRuleCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	item, err := op.AlertRuleCreate(c.Request.Context(), &req)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, item)
}

func updateAlertRule(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid id")
		return
	}
	var req model.AlertRuleUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	item, err := op.AlertRuleUpdate(c.Request.Context(), id, &req)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, item)
}

func deleteAlertRule(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid id")
		return
	}
	if err := op.AlertRuleDelete(c.Request.Context(), id); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, "deleted")
}

// evaluateAlertRule 试算一条规则（不发送、不记账）。
//
// 用 AlertRuleDue 而不是 AlertRuleEvaluate：面板上用户问的是"**现在**会不会报"，
// 所以必须把冷却也算进去——只算阈值会显示"会触发"而实际在冷却中不会发，
// 那正是用户最难自查的一类困惑。返回的 reason 会把未触发的原因说清楚。
func evaluateAlertRule(c *gin.Context) {
	var body struct {
		RuleID int `json:"rule_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	rule, err := op.AlertRuleGet(c.Request.Context(), body.RuleID)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	evals, err := op.AlertRuleDue(c.Request.Context(), *rule, timeNow())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, evals)
}

// previewAlertRules 预演一轮（算但不发）：回答"按现在这套规则，此刻会报几条、报哪些渠道"，
// 而不必先把手机刷一遍。判定口径与真发完全一致（同走 AlertRuleDue，含冷却）。
func previewAlertRules(c *gin.Context) {
	resp.Success(c, op.AlertRulePreviewAll(c.Request.Context(), timeNow()))
}

// runAlertRules 立刻跑一轮评估（面板上的"立即检查"），会真的发送。
func runAlertRules(c *gin.Context) {
	fired := task.AlertRuleEvaluateAll(c.Request.Context())
	resp.Success(c, gin.H{"fired": fired})
}

func listAlertFires(c *gin.Context) {
	resp.Success(c, op.AlertFireList(c.Request.Context(), 50))
}
