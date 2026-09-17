package handlers

import (
	"errors"
	"net/http"

	"github.com/bestruirui/octopus/internal/pool"
	"github.com/bestruirui/octopus/internal/poolstore"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// R-pool-ext-001 第三批：**运行时声明式适配器**（用户对"四个边界"回复「我全都要」后落地）。
//
// 这四个边界不是文档里的承诺，而是这里的硬约束：
//
//	① 运行时声明式注册是开放的，但受域名白名单约束（pool_declarative_hosts，**留空即一律拒绝**）；
//	② 外部工具包读号池有独立的只读 APIKey 通道（见 pool_apikey.go 与 /v1/pool/*）；
//	③ "条目 → 渠道凭据"的同步方向固定为单向，由适配器的 sync 能力位声明；
//	④ 首批模板（new-api / one-api / sub2api）随这份文件一起给出，见 poolAdapterTemplates。
//
// 安全口径：请求体里的 secret 只进加密存储与内存，**任何响应都不回显**（脱敏在 poolstore.Redact 一处实现）。
func init() {
	router.NewGroupRouter("/api/v1/pool").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/adapters", http.MethodGet).
				Handle(poolAdaptersList),
		).
		AddRoute(
			router.NewRoute("/adapters", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(poolAdapterRegister),
		).
		AddRoute(
			router.NewRoute("/adapters/templates", http.MethodGet).
				Handle(poolAdapterTemplates),
		).
		AddRoute(
			router.NewRoute("/adapters/:kind", http.MethodDelete).
				Handle(poolAdapterRemove),
		)
}

// poolAdaptersList 回显已注册的声明式适配器与当前白名单（凭据字段一律为空）。
func poolAdaptersList(c *gin.Context) {
	items, err := poolstore.List()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	guards := pool.DeclarativeGuardsOf()
	resp.Success(c, gin.H{
		"items":                items,
		"total":                len(items),
		"hosts":                poolstore.Hosts(),
		"allowed_hosts":        guards.AllowedHosts,
		"read_only_capable":    true,
		"declarative_only_ops": []pool.Capability{pool.CapList, pool.CapGet},
	})
}

// poolAdapterRegister 注册（或按 kind 替换）一个声明式适配器。
//
// 两条错误码刻意分开：主机不在白名单是 **403**（这是被安全边界拒绝，不是格式写错），
// 其余形状问题（kind 前缀、能力位、URL、auth 字段缺失）是 400，运维照着改输入即可。
func poolAdapterRegister(c *gin.Context) {
	var spec pool.DeclarativeSpec
	if err := c.ShouldBindJSON(&spec); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	registered, err := poolstore.Register(spec)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, pool.ErrHostNotAllowed) {
			status = http.StatusForbidden
		}
		resp.Error(c, status, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": registered})
}

// poolAdapterRemove 移除一个声明式适配器（内置适配器会被拒绝）。
func poolAdapterRemove(c *gin.Context) {
	kind := c.Param("kind")
	if err := poolstore.Remove(kind); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, pool.ErrUnknownKind) {
			status = http.StatusNotFound
			if pool.BuiltinKind(kind) {
				status = http.StatusConflict
			}
		}
		resp.Error(c, status, err.Error())
		return
	}
	resp.Success(c, gin.H{"kind": kind, "removed": true})
}

// poolAdapterTemplates 返回首批三个常见形态的声明式模板（new-api / one-api / sub2api）。
//
// 三个形态共用同一套 New-API 系的只读端点（/api/user/self 与 /api/token/?p=0），差别只在默认路径与
// 字段名；模板里的 base_url 与 secret 留空由使用方填，因此模板本身不含任何凭据。
func poolAdapterTemplates(c *gin.Context) {
	resp.Success(c, gin.H{"items": poolAdapterTemplateList(), "total": len(poolAdapterTemplateList())})
}

// poolAdapterTemplate 是一个可直接改成 spec 的模板：只读、不含凭据。
type poolAdapterTemplate struct {
	Kind         string               `json:"kind"`
	Title        string               `json:"title"`
	Note         string               `json:"note"`
	Capabilities []pool.Capability    `json:"capabilities"`
	Spec         pool.DeclarativeSpec `json:"spec"`
}

// poolAdapterTemplateList 给出三套形态的模板。字段映射按各形态常见的命名给（New-API 系同族，
// 差异在 sub2api 的订阅字段），使用方按自己站点的实际字段改两行即可。
func poolAdapterTemplateList() []poolAdapterTemplate {
	fields := map[string]string{
		"id":         "id",
		"name":       "name",
		"status":     "status",
		"enabled":    "status",
		"expires_at": "expired_time",
	}
	readonly := []pool.Capability{pool.CapList, pool.CapGet}
	return []poolAdapterTemplate{
		{
			Kind:         "custom-new-api",
			Title:        "New API（令牌清单）",
			Note:         "先填 base_url 与凭据；items_path/fields 按自己站点的返回形状微调。能力位只有只读，写操作一律拒绝。",
			Capabilities: readonly,
			Spec: pool.DeclarativeSpec{
				Kind:         "custom-new-api",
				Title:        "New API（令牌清单）",
				Capabilities: readonly,
				Auth: pool.DeclarativeAuth{
					Type:      "login",
					LoginURL:  "/api/user/login",
					LoginBody: map[string]string{"username": "{{username}}", "password": "{{password}}"},
					TokenPath: "data.access_token",
				},
				List: pool.DeclarativeList{
					Path:      "/api/token/?p=0&size=100",
					ItemsPath: "data.items",
					Fields:    fields,
				},
			},
		},
		{
			Kind:         "custom-one-api",
			Title:        "One API（令牌清单）",
			Note:         "与 New API 同族：登录端点同为 /api/user/login，令牌清单在 data.items。",
			Capabilities: readonly,
			Spec: pool.DeclarativeSpec{
				Kind:         "custom-one-api",
				Title:        "One API（令牌清单）",
				Capabilities: readonly,
				Auth: pool.DeclarativeAuth{
					Type:      "login",
					LoginURL:  "/api/user/login",
					LoginBody: map[string]string{"username": "{{username}}", "password": "{{password}}"},
					TokenPath: "data.access_token",
				},
				List: pool.DeclarativeList{
					Path:      "/api/token/?p=0",
					ItemsPath: "data",
					Fields:    fields,
				},
			},
		},
		{
			Kind:         "custom-sub2api",
			Title:        "Sub2API（订阅/账号清单）",
			Note:         "订阅形态：账号页通常在 data.items，字段名可能是 sub_status/expires_at，按实际返回改 fields。",
			Capabilities: readonly,
			Spec: pool.DeclarativeSpec{
				Kind:         "custom-sub2api",
				Title:        "Sub2API（订阅/账号清单）",
				Capabilities: readonly,
				Auth: pool.DeclarativeAuth{
					Type:      "login",
					LoginURL:  "/api/auth/login",
					LoginBody: map[string]string{"email": "{{username}}", "password": "{{password}}"},
					TokenPath: "data.token",
				},
				List: pool.DeclarativeList{
					Path:      "/api/accounts",
					ItemsPath: "data.items",
					Fields:    fields,
				},
			},
		},
	}
}
