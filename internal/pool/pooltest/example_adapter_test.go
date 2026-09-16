package pooltest_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/pool"
	"github.com/bestruirui/octopus/internal/pool/pooltest"
)

// 这个文件是**接入方视角**的最小可用适配器示例（开发指南里的代码就是它）。
//
// 它刻意用公开 API 写：`pool.Adapter` + 可选能力接口 + `pooltest.Run`。
// 放在这里而不是只写在文档里，是为了让示例代码被编译和运行——
// 文档里的代码一旦失修，接入方照抄就会踩坑。

// inventoryAdapter 把一个"别的工具包里的账号清单"接进号池：
// 只读列表 + 按 ID 取详情 + 探活，不实现写操作（所以不声明 refresh/toggle/sync）。
type inventoryAdapter struct {
	baseURL string
	// 凭据刻意不放在 Entry 里：标了 secret 的字段出现一次就是泄漏。
	token string
}

// Info 是自描述：外部工具靠它发现"这个后端有什么、能做什么、字段长什么样"。
func (a *inventoryAdapter) Info() pool.AdapterInfo {
	return pool.AdapterInfo{
		Kind:         "example-inventory",
		Title:        "示例：外部工具包账号清单",
		Capabilities: []pool.Capability{pool.CapList, pool.CapGet, pool.CapProbe},
		Fields: []pool.FieldSpec{
			{Name: "base_url", Type: "string", Label: "站点地址", Required: true},
			{Name: "access_token", Type: "string", Label: "访问令牌", Required: true, Secret: true},
		},
	}
}

// Entries 是唯一的必需方法：把后端的状态投影成统一视图的行。
func (a *inventoryAdapter) Entries(ctx context.Context) ([]pool.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []pool.Entry{{
		Kind:     "example-inventory",
		ID:       "acc-1",
		Name:     "示例账号",
		Provider: "example",
		Status:   "active",
		Enabled:  true,
		Healthy:  true,
		Labels:   map[string]string{"plan": "pro"}, // 只放非凭据信息
		Detail:   map[string]any{"mapped_channel": "官方账号池-example"},
	}}, nil
}

func (a *inventoryAdapter) Get(_ context.Context, id string) (pool.Entry, error) {
	entries, err := a.Entries(context.Background())
	if err != nil {
		return pool.Entry{}, err
	}
	for _, entry := range entries {
		if entry.ID == id {
			return entry, nil
		}
	}
	return pool.Entry{}, pool.ErrEntryNotFound
}

// Probe 主动探活：这里用"有没有配地址"当探活结果（真实适配器在这里发一次轻量请求）。
func (a *inventoryAdapter) Probe(ctx context.Context, id string) (pool.Entry, error) {
	entry, err := a.Get(ctx, id)
	if err != nil {
		return pool.Entry{}, err
	}
	if a.baseURL == "" {
		entry.Healthy = false
		entry.LastError = "base_url 未配置"
	}
	return entry, nil
}

// TestExampleAdapterContract 是接入方的标准动作：一行契约自检。
func TestExampleAdapterContract(t *testing.T) {
	pooltest.Run(t, &inventoryAdapter{baseURL: "https://example.invalid", token: "secret-token"})
}

// 说明"secret 字段不会漏出去"在示例里的具体含义：Entry 里放的是凭据的**状态**，不是凭据本身。
// 这条断言同时守住示例本身不跑偏。
func TestExampleAdapterDoesNotLeakToken(t *testing.T) {
	adapter := &inventoryAdapter{baseURL: "https://example.invalid", token: "secret-token"}
	entries, err := adapter.Entries(context.Background())
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(encoded), adapter.token) {
		t.Fatalf("条目里不该出现凭据内容: %s", encoded)
	}
}
