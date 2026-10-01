package pool

import "sort"

// 第四批 C/D：机器可读的接口文档。
//
// 为什么要它：外部反代工具包接进来时，最省事的路径是**照着文档生成客户端**。
// 这份 OpenAPI 由注册表推导（已注册的 kind × 能力位 → 路由），所以只要适配器注册进来，
// 文档里就会自动出现它的可调用操作——不需要有人记得去更新文档。

// OpenAPIDocument 返回一份最小可用的 OpenAPI 3.0.3 文档（只覆盖 /api/v1/pool/*）。
//
// 「最小可用」的边界：paths + 参数 + 组件 schema 足以生成客户端；不生成完整响应细节
// （错误码语义写在每个操作的 description 里，映射规则见 poolErrorStatus）。
func OpenAPIDocument(version string) map[string]any {
	kinds := Kinds()
	kindNames := make([]string, 0, len(kinds))
	capabilitySet := map[Capability]bool{}
	for _, info := range kinds {
		kindNames = append(kindNames, info.Kind)
		for _, capability := range info.Capabilities {
			capabilitySet[capability] = true
		}
	}
	sort.Strings(kindNames)

	kindParam := map[string]any{
		"name": "kind", "in": "path", "required": true,
		"schema":      map[string]any{"type": "string", "enum": kindNames},
		"description": "号池后端标识（已注册的 kind）",
	}
	idParam := map[string]any{
		"name": "id", "in": "path", "required": true,
		"schema":      map[string]any{"type": "string"},
		"description": "条目 ID（官方账号池形如 <provider>:<account_id>）",
	}

	paths := map[string]any{
		"/api/v1/pool/kinds": map[string]any{
			"get": operation("列出号池后端与能力",
				"返回每个后端的 capabilities、字段说明（凭据字段标 secret）与 operations（由能力位推导的调用清单）。",
				nil, "#/components/schemas/AdapterInfoList"),
		},
		"/api/v1/pool/kinds/{kind}": map[string]any{
			"get": operation("取单个号池后端", "未注册的 kind 回 404。", []any{kindParam}, "#/components/schemas/AdapterInfo"),
		},
		"/api/v1/pool/entries": map[string]any{
			"get": operation("统一号池视图（支持过滤/排序/分页）",
				"单后端失败进 warnings[] 且仍 200；kind 未注册回 400。分页参数 limit/offset/sort/desc。",
				[]any{
					query("kind", "按后端过滤"),
					query("provider", "按服务商过滤"),
					query("status", "按状态过滤"),
					query("q", "名称或 ID 子串（大小写不敏感）"),
					query("enabled", "true/false"),
					query("healthy", "true/false"),
					query("expiring_within", "只留距今 N 秒内到期的条目"),
					query("has_expiry", "true=只留有过期时间的"),
					query("limit", "返回条数上限"),
					query("offset", "跳过条数"),
					query("sort", "kind/name/provider/status/enabled/expires"),
					query("desc", "true=倒序"),
				}, "#/components/schemas/EntryList"),
		},
		"/api/v1/pool/entries/{kind}/{id}": map[string]any{
			"get": operation("取单条号池条目", "条目不存在回 404。",
				[]any{kindParam, idParam}, "#/components/schemas/Entry"),
		},
		"/api/v1/pool/entries/batch": map[string]any{
			"post": map[string]any{
				"summary":     "批量动作（probe/refresh/enable/disable）",
				"description": "逐条独立：一条失败不影响其余；未声明能力的后端逐条回 capability not supported。需要 ids 或 filter+max，不允许隐式全量。",
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{"application/json": map[string]any{
						"schema": map[string]any{"$ref": "#/components/schemas/BatchRequest"},
					}},
				},
				"responses": responses("#/components/schemas/BatchResult"),
			},
		},
		"/api/v1/pool/stats": map[string]any{
			"get": operation("号池计数", "只数个数；要切分维度用 /summary。", nil, "#/components/schemas/Stats"),
		},
		"/api/v1/pool/summary": map[string]any{
			"get": operation("号池汇总视图",
				"启用/停用/健康/带错/临期/过期 + 分后端/分服务商/分状态 + kind_errors（让「空」与「坏」可区分）。",
				nil, "#/components/schemas/Summary"),
		},
		"/api/v1/pool/export": map[string]any{
			"get": operation("导出统一视图",
				"format=json（默认）或 csv。固定字段，不含任何凭据字段。",
				[]any{query("kind", "按后端过滤"), query("format", "json 或 csv")},
				"#/components/schemas/EntryList"),
		},
	}

	// 按能力位补上操作路由：注册了新的 kind/能力，文档自动跟着长。
	if capabilitySet[CapProbe] {
		paths["/api/v1/pool/entries/{kind}/{id}/probe"] = map[string]any{
			"post": actionOp("探活/读配额", CapProbe, []any{kindParam, idParam}),
		}
	}
	if capabilitySet[CapRefresh] {
		paths["/api/v1/pool/entries/{kind}/{id}/refresh"] = map[string]any{
			"post": actionOp("刷新凭据", CapRefresh, []any{kindParam, idParam}),
		}
	}
	if capabilitySet[CapSync] {
		paths["/api/v1/pool/kinds/{kind}/sync"] = map[string]any{
			"post": actionOp("物化到转发层", CapSync, []any{kindParam}),
		}
	}
	if capabilitySet[CapToggle] {
		paths["/api/v1/pool/entries/{kind}/{id}/enable"] = map[string]any{
			"post": actionOp("启用条目", CapToggle, []any{kindParam, idParam}),
		}
		paths["/api/v1/pool/entries/{kind}/{id}/disable"] = map[string]any{
			"post": actionOp("停用条目", CapToggle, []any{kindParam, idParam}),
		}
	}

	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":   "octopus 号池扩展层 API",
			"version": version,
			"description": "号池后端可插拔：每个后端（kind）自描述能力位与调用清单。" +
				"错误约定：未注册 kind / 条目不存在 → 404；后端未声明该能力 → 501；其余上游失败 → 502。" +
				"凭据永不回显：字段说明里的 secret=true 表示该字段只进不出。",
		},
		"servers": []any{map[string]any{"url": "/"}},
		"paths":   paths,
		"components": map[string]any{
			"schemas": schemas(),
		},
	}
}

func schemas() map[string]any {
	return map[string]any{
		"Entry": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind":       map[string]any{"type": "string"},
				"id":         map[string]any{"type": "string"},
				"name":       map[string]any{"type": "string"},
				"provider":   map[string]any{"type": "string"},
				"status":     map[string]any{"type": "string"},
				"enabled":    map[string]any{"type": "boolean"},
				"healthy":    map[string]any{"type": "boolean"},
				"plan_tier":  map[string]any{"type": "string"},
				"expires_at": map[string]any{"type": "string", "format": "date-time", "nullable": true},
				"last_error": map[string]any{"type": "string"},
				"labels":     map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				"detail":     map[string]any{"type": "object", "additionalProperties": true},
			},
		},
		"EntryList": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items":    map[string]any{"type": "array", "items": ref("#/components/schemas/Entry")},
				"total":    map[string]any{"type": "integer", "description": "过滤后条数"},
				"returned": map[string]any{"type": "integer", "description": "本次返回条数"},
				"scanned":  map[string]any{"type": "integer", "description": "过滤前条数"},
				"warnings": map[string]any{"type": "array", "items": ref("#/components/schemas/KindError")},
			},
		},
		"FieldSpec": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":     map[string]any{"type": "string"},
				"type":     map[string]any{"type": "string"},
				"label":    map[string]any{"type": "string"},
				"required": map[string]any{"type": "boolean"},
				"secret":   map[string]any{"type": "boolean", "description": "true=凭据字段，任何接口都不回显"},
			},
		},
		"Operation": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"capability":  map[string]any{"type": "string"},
				"method":      map[string]any{"type": "string"},
				"path":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
			},
		},
		"AdapterInfo": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind":         map[string]any{"type": "string"},
				"title":        map[string]any{"type": "string"},
				"capabilities": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"fields":       map[string]any{"type": "array", "items": ref("#/components/schemas/FieldSpec")},
				"operations":   map[string]any{"type": "array", "items": ref("#/components/schemas/Operation")},
				"builtin":      map[string]any{"type": "boolean"},
				"since":        map[string]any{"type": "string"},
			},
		},
		"AdapterInfoList": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items": map[string]any{"type": "array", "items": ref("#/components/schemas/AdapterInfo")},
				"total": map[string]any{"type": "integer"},
			},
		},
		"KindError": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind":  map[string]any{"type": "string"},
				"error": map[string]any{"type": "string"},
			},
		},
		"KindStat": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"entries": map[string]any{"type": "integer"},
				"enabled": map[string]any{"type": "integer"},
				"healthy": map[string]any{"type": "integer"},
			},
		},
		"Stats": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"total":   map[string]any{"type": "integer"},
				"enabled": map[string]any{"type": "integer"},
				"healthy": map[string]any{"type": "integer"},
				"by_kind": map[string]any{"type": "object", "additionalProperties": ref("#/components/schemas/KindStat")},
			},
		},
		"Summary": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"total":         map[string]any{"type": "integer"},
				"enabled":       map[string]any{"type": "integer"},
				"disabled":      map[string]any{"type": "integer"},
				"healthy":       map[string]any{"type": "integer"},
				"unhealthy":     map[string]any{"type": "integer"},
				"with_error":    map[string]any{"type": "integer"},
				"expiring_soon": map[string]any{"type": "integer", "description": "24 小时内到期"},
				"expired":       map[string]any{"type": "integer"},
				"by_kind":       map[string]any{"type": "object", "additionalProperties": ref("#/components/schemas/KindStat")},
				"by_provider":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer"}},
				"by_status":     map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer"}},
				"kind_errors":   map[string]any{"type": "array", "items": ref("#/components/schemas/KindError")},
			},
		},
		"BatchRequest": map[string]any{
			"type":     "object",
			"required": []any{"action"},
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "enum": []any{BatchProbe, BatchRefresh, BatchEnable, BatchDisable}},
				"kind":   map[string]any{"type": "string", "description": "配合 ids 时的后端标识"},
				"ids":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"filter": map[string]any{"type": "object", "additionalProperties": true},
				"max":    map[string]any{"type": "integer", "description": "默认 50，上限 200"},
			},
		},
		"BatchResult": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":  map[string]any{"type": "string"},
				"total":   map[string]any{"type": "integer"},
				"ok":      map[string]any{"type": "integer"},
				"failed":  map[string]any{"type": "integer"},
				"skipped": map[string]any{"type": "integer"},
				"items": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
					"kind":  map[string]any{"type": "string"},
					"id":    map[string]any{"type": "string"},
					"ok":    map[string]any{"type": "boolean"},
					"entry": ref("#/components/schemas/Entry"),
					"error": map[string]any{"type": "string"},
				}}},
			},
		},
	}
}

func ref(path string) map[string]any { return map[string]any{"$ref": path} }

func query(name, description string) map[string]any {
	return map[string]any{
		"name": name, "in": "query", "required": false,
		"schema": map[string]any{"type": "string"}, "description": description,
	}
}

func responses(schemaRef string) map[string]any {
	return map[string]any{
		"200": map[string]any{
			"description": "OK",
			"content": map[string]any{"application/json": map[string]any{
				"schema": ref(schemaRef),
			}},
		},
		"400": map[string]any{"description": "参数非法或 kind 未注册"},
		"404": map[string]any{"description": "条目不存在"},
		"501": map[string]any{"description": "该后端未声明该能力"},
		"502": map[string]any{"description": "上游或后端失败"},
	}
}

func operation(summary, description string, parameters []any, schemaRef string) map[string]any {
	op := map[string]any{
		"summary":     summary,
		"description": description,
		"responses":   responses(schemaRef),
		"security":    []any{map[string]any{"cookieAuth": []any{}}},
	}
	if parameters != nil {
		op["parameters"] = parameters
	}
	return op
}

// actionOp 生成一条"按能力位放行"的操作文档。
func actionOp(summary string, capability Capability, parameters []any) map[string]any {
	return operation(summary,
		"需要该后端声明 "+string(capability)+" 能力位；未声明回 501 并在消息里点名能力位。",
		parameters, "#/components/schemas/Entry")
}
