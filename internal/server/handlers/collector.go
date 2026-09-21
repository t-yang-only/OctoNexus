package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/bestruirui/octopus/internal/collector"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// R-collector-001 账号级采集凭据：把"这家站点怎么读余额"变成一份可安装的东西。
//
// 两条路径都支持（用户明确要求"两个功能一起实现"）：
//   - 交互登录：octopus 把站点登录页反代成临时地址，用户在里面输账号/验证码，服务端捕获会话；
//   - 采集包：用户自己逆向出的登录+查账描述（JSON），装进 octopus 长期自动跑。
//
// 安全口径：全部挂 middleware.Auth()（管理面）；凭据/会话/采集包三类密文分开 AAD 加密落库；
// 接口出参永不回显密码与会话内容，只给 has_credentials / has_session 与脱敏步骤轨迹。
func init() {
	router.NewGroupRouter("/api/v1/collector").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/sources", http.MethodGet).Handle(collectorSourceList),
		).
		AddRoute(
			router.NewRoute("/sources", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(collectorSourceCreate),
		).
		AddRoute(
			router.NewRoute("/sources/:id", http.MethodGet).Handle(collectorSourceGet),
		).
		AddRoute(
			router.NewRoute("/sources/:id", http.MethodPut).
				Use(middleware.RequireJSON()).
				Handle(collectorSourceUpdate),
		).
		AddRoute(
			router.NewRoute("/sources/:id", http.MethodDelete).Handle(collectorSourceDelete),
		).
		AddRoute(
			router.NewRoute("/sources/:id/run", http.MethodPost).Handle(collectorSourceRun),
		).
		AddRoute(
			router.NewRoute("/sources/:id/login", http.MethodPost).Handle(collectorSourceLoginStart),
		).
		AddRoute(
			// 导出扩展包：只给结构，不含账号密码，可发给别人用（别人填自己的凭据即可）。
			router.NewRoute("/sources/:id/export", http.MethodGet).Handle(collectorSourceExport),
		).
		AddRoute(
			// 导入扩展包：拿一份别人给的包 + 自己的站点与凭据，直接建一条可用凭据。
			router.NewRoute("/sources/import", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(collectorSourceImport),
		).
		AddRoute(
			router.NewRoute("/sources/:id/login/finish", http.MethodPost).Handle(collectorSourceLoginFinish),
		).
		AddRoute(
			router.NewRoute("/sources/:id/session", http.MethodDelete).Handle(collectorSourceSessionClear),
		).
		AddRoute(
			router.NewRoute("/login/:id/*path", http.MethodGet).Handle(collectorLoginProxy),
		).
		AddRoute(
			router.NewRoute("/login/:id/*path", http.MethodPost).Handle(collectorLoginProxy),
		).
		AddRoute(
			router.NewRoute("/templates", http.MethodGet).Handle(collectorTemplateList),
		).
		AddRoute(
			router.NewRoute("/templates/render", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(collectorTemplateRender),
		)
}

// collectorTemplateList 列出内置站点类型（new-api / one-api / sub2api / litellm），
// 每类给 form 与 interactive 两版：前者直接 POST 登录，后者走反代登录页（验证码场景）。
func collectorTemplateList(c *gin.Context) {
	items := collector.Templates()
	resp.Success(c, gin.H{"items": items, "total": len(items), "kinds": collector.TemplateKinds})
}

// collectorTemplateRender 按站点类型 + 用户填的网址生成一份采集包正文（生成即过装载门禁）。
// 它只把"业界惯例的登录/查账端点"填好，主机白名单来自用户填的网址 —— 不预置域名。
func collectorTemplateRender(c *gin.Context) {
	var payload struct {
		Kind    string `json:"kind"`
		BaseURL string `json:"base_url"`
		Mode    string `json:"mode"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		resp.Error(c, http.StatusBadRequest, "请求体格式错误："+err.Error())
		return
	}
	pack, err := collector.BuildTemplate(payload.Kind, payload.BaseURL, payload.Mode)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	raw, err := json.MarshalIndent(pack, "", "  ")
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"pack": string(raw), "kind": payload.Kind, "mode": pack.Login.Mode, "units": pack.Units})
}

func collectorSourceList(c *gin.Context) {
	items, err := op.CredentialSourceList(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": items, "total": len(items)})
}

func collectorSourceGet(c *gin.Context) {
	id, ok := collectorID(c)
	if !ok {
		return
	}
	item, err := op.CredentialSourceGet(c.Request.Context(), id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": item})
}

func collectorSourceCreate(c *gin.Context) {
	var in model.CredentialSourceInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	item, err := op.CredentialSourceSave(c.Request.Context(), 0, in)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": item})
}

func collectorSourceUpdate(c *gin.Context) {
	id, ok := collectorID(c)
	if !ok {
		return
	}
	var in model.CredentialSourceInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	item, err := op.CredentialSourceSave(c.Request.Context(), id, in)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": item})
}

func collectorSourceDelete(c *gin.Context) {
	id, ok := collectorID(c)
	if !ok {
		return
	}
	if err := op.CredentialSourceDelete(c.Request.Context(), id); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{})
}

// collectorSourceRun 立即采集一次（面板上的"读一下"按钮）。
func collectorSourceRun(c *gin.Context) {
	id, ok := collectorID(c)
	if !ok {
		return
	}
	reading, result, err := op.CredentialSourceRunNow(c.Request.Context(), id)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	item, _ := op.CredentialSourceGet(c.Request.Context(), id)
	resp.Success(c, gin.H{
		"reading": reading,
		"item":    item,
		"ok":      result.OK(),
		"reason":  result.ReasonKey,
		"message": result.ReasonText,
		"steps":   result.Steps,
	})
}

// collectorSourceExport 导出扩展包（结构 + 建议的站点/名称/单位，不含凭据）。
func collectorSourceExport(c *gin.Context) {
	id, ok := collectorID(c)
	if !ok {
		return
	}
	item, pack, err := op.CredentialSourceExportPack(c.Request.Context(), id)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{
		"name":     item.Name,
		"site":     item.Site,
		"units":    item.Units,
		"hosts":    item.Hosts,
		"kind":     item.Kind,
		"pack":     pack,
		"withheld": "账号密码不在包里：导入方需另填自己的凭据",
	})
}

// collectorSourceImport 导入扩展包：包 + 站点 + 凭据 → 一条可直接跑的采集凭据。
func collectorSourceImport(c *gin.Context) {
	var payload struct {
		Name        string `json:"name"`
		Site        string `json:"site"`
		Pack        string `json:"pack"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		ProxyNodeID *int   `json:"proxy_node_id"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if strings.TrimSpace(payload.Pack) == "" {
		resp.Error(c, http.StatusBadRequest, "缺少 pack：请提供一份扩展包")
		return
	}
	kind := model.CredentialKindPack
	if payload.Site != "" {
		kind = model.CredentialKindLogin
	}
	item, err := op.CredentialSourceSave(c.Request.Context(), 0, model.CredentialSourceInput{
		Name: payload.Name, Kind: kind, Site: payload.Site, Pack: payload.Pack,
		Username: payload.Username, Password: payload.Password, ProxyNodeID: payload.ProxyNodeID,
	})
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": item})
}

// collectorSourceLoginStart 给出交互登录的临时地址。
func collectorSourceLoginStart(c *gin.Context) {
	id, ok := collectorID(c)
	if !ok {
		return
	}
	item, err := op.CredentialSourceStartLogin(c.Request.Context(), id)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": item, "login_url": item.LoginURL})
}

// collectorSourceLoginFinish 完成交互登录：保存捕获到的会话并试读一次。
func collectorSourceLoginFinish(c *gin.Context) {
	id, ok := collectorID(c)
	if !ok {
		return
	}
	item, err := op.CredentialSourceFinishLogin(c.Request.Context(), id)
	if err != nil {
		// 会话可能已经存下、只是试读失败：把最新状态一并带回，面板才能显示真实处境。
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": item})
}

func collectorSourceSessionClear(c *gin.Context) {
	id, ok := collectorID(c)
	if !ok {
		return
	}
	item, err := op.CredentialSourceClearSession(c.Request.Context(), id)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": item})
}

// collectorLoginPortal 是给用户打开的登录临时地址（带一次性令牌，无需面板会话）。
func collectorLoginPortal(c *gin.Context) {
	id, ok := collectorID(c)
	if !ok {
		return
	}
	token := c.Param("token")
	if !op.CollectorLoginTokenMatches(id, token) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusUnauthorized, `<html><head><meta charset="utf-8"><title>登录链接已失效</title></head>`+
			`<body style="font-family:system-ui;padding:32px"><h3>登录链接已失效</h3>`+
			`<p>这条登录地址是一次性的，过期或已完成后需要回面板重新生成：</p>`+
			`<p>扩展 → 采集凭据 → 该条目 → 「开始登录」，再复制新的临时地址。</p></body></html>`)
		return
	}
	proxy, err := op.CollectorLoginProxyWithPrefix(c.Request.Context(), id, op.CollectorPortalPrefix(id, token))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	proxy.Serve(c.Writer, c.Request, c.Param("path"))
}

// collectorLoginProxy 就是"被反向代理成工具/XXX 的临时地址"：用户在这里登录，会话由我们捕获。
func collectorLoginProxy(c *gin.Context) {
	id, ok := collectorID(c)
	if !ok {
		return
	}
	proxy, err := op.CollectorLoginProxy(c.Request.Context(), id)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	proxy.Serve(c.Writer, c.Request, c.Param("path"))
}

func collectorID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		resp.Error(c, http.StatusBadRequest, "采集凭据 id 非法")
		return 0, false
	}
	return id, true
}

// 登录通道的公开组：**故意不挂 middleware.Auth()**。
//
// 路径里的令牌是一次性、按源绑定的鉴权凭据（op.CollectorPortalPrefix/CollectorLoginTokenMatches），
// 再要求面板 cookie 会造成两个真问题：① 用户没登录面板时打开链接只看到 401 JSON（白屏）；
// ② 链接没法发给别人/在手机上用。其余 collector 接口仍然全部留在鉴权组里。
func init() {
	router.NewGroupRouter("/api/v1/collector").
		ServeOn(router.ServerAdmin).
		AddRoute(
			router.NewRoute("/portal/:id/:token/*path", http.MethodGet).Handle(collectorLoginPortal),
		).
		AddRoute(
			router.NewRoute("/portal/:id/:token/*path", http.MethodPost).Handle(collectorLoginPortal),
		)
}
