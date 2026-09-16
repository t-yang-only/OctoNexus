package pool

import (
	"context"
	"fmt"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// officialKind 是官方账号池的适配器标识（R-acct-001/T-pool-002 已有的那套后端）。
const officialKind = "official"

func init() {
	// 内置适配器在 init 注册：号池开箱就有官方账号这一种后端，外部适配器按同样方式追加。
	// 注册失败只能是我们自己写错了常量，直接 panic 让启动期暴露，不留一个静默少了一种号池后端的进程。
	if err := Register(officialAdapter{}); err != nil {
		panic(fmt.Sprintf("register pool adapter %q: %v", officialKind, err))
	}
}

// officialAdapter 把既有官方账号池投影成统一视图。
//
// 它只读：数据来自 op.OfficialPoolStatusList（账号 → 渠道凭据的既有映射），
// 因此本适配器不新增表、不改选路，纯粹是"既有实现的一种投影"。
type officialAdapter struct{}

func (officialAdapter) Info() AdapterInfo {
	return AdapterInfo{
		Kind:  officialKind,
		Title: "官方账号池",
		// 能力位只声明真的有的：探活是 op.OfficialAccountReadUsage、刷新与物化是 op.OfficialPoolSync、
		// 新建是 authorize/callback 流程。人工启停目前由同步逻辑按账号状态收敛，故不声明 toggle。
		Capabilities: []Capability{CapList, CapGet, CapProbe, CapRefresh, CapProvision, CapSync},
		Builtin:      true,
		Since:        "R-pool-ext-001",
		Fields: []FieldSpec{
			{Name: "provider", Type: "string", Label: "服务商", Required: true},
			{Name: "external_name", Type: "string", Label: "官方侧账号标识", Required: true},
			{Name: "access_token", Type: "string", Label: "访问凭据", Required: true, Secret: true},
			{Name: "refresh_token", Type: "string", Label: "刷新凭据", Secret: true},
			{Name: "plan_tier", Type: "string", Label: "套餐档位"},
		},
	}
}

func (officialAdapter) Entries(ctx context.Context) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	statuses, err := op.OfficialPoolStatusList(nil)
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, 8)
	for _, status := range statuses {
		for _, member := range status.Members {
			entries = append(entries, Entry{
				Kind:      officialKind,
				ID:        fmt.Sprintf("%s:%d", status.Provider, member.AccountID),
				Name:      member.ExternalName,
				Provider:  string(status.Provider),
				Status:    string(member.Status),
				Enabled:   member.KeyEnabled,
				Healthy:   member.Healthy,
				PlanTier:  member.PlanTier,
				ExpiresAt: member.ExpiresAt,
				LastError: member.LastError,
				Labels: map[string]string{
					"provider": string(status.Provider),
					"channel":  status.ChannelName,
				},
				Detail: map[string]any{
					"account_id":    member.AccountID,
					"channel_id":    status.ChannelID,
					"channel_name":  status.ChannelName,
					"key_name":      member.KeyName,
					"key_exists":    member.KeyExists,
					"key_enabled":   member.KeyEnabled,
					"window_5h":     member.Window5H,
					"window_7d":     member.Window7D,
					"models":        status.Models,
					"grants":        status.Grants,
					"is_active":     member.Status == model.OfficialAccountStatusActive,
					"account_total": status.Accounts,
					"active_keys":   status.ActiveKeys,
				},
			})
		}
	}
	return entries, nil
}
