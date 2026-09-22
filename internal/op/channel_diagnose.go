package op

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// T-usability-001 渠道可用性诊断。
//
// ## 解决什么问题
//
// 一个渠道上的模型要能被客户端调用，必须**三层齐全**：
//
//	channel_models（模型配上了）
//	  → channel_grants（某个凭据被授权用这个模型）
//	    → groups（生成了分组，客户端用分组名调用）
//
// 任何一层断掉，结果都是「配了但用不上」，而**界面上完全看不出是哪一层断的**：
// 渠道详情里模型列得好好的，客户端却报 model not found。
//
// 实测（2026-09-23 生产库）：三个渠道共 91 个模型处于这种状态
// （pipixia 27、senseaudio 43、openagents 21），全部是「有模型、无授权」——
// 授权只在显式提交 grants 时创建，而这些模型没有走过那条路径。
//
// 诊断的价值在于把「为什么用不上」变成可读的一句话，而不是让用户去猜
// 是分组名错了、渠道没启用、还是模型名打错了。
type ChannelModelGap struct {
	ModelName string `json:"model_name"`
	// GrantKeys 是被授权使用该模型的凭据名；为空即「没有任何凭据被授权」。
	GrantKeys []string `json:"grant_keys"`
	// GroupName 是对应的自动分组名；空串表示分组不存在。
	GroupName string `json:"group_name"`
	// Usable 是最终结论：这个模型现在能不能被客户端调用。
	Usable bool `json:"usable"`
	// Reason 在不可用时给出**可执行**的原因（不是「不可用」三个字）。
	Reason string `json:"reason,omitempty"`
}

// ChannelDiagnose 是一个渠道的可用性诊断结果。
type ChannelDiagnose struct {
	ChannelID   int    `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Enabled     bool   `json:"enabled"`
	// KeyCount 是启用的凭据数。凭据全禁用时所有模型都不可用，且原因与「没授权」不同。
	KeyCount int `json:"key_count"`
	// UsableCount / TotalCount 给出这个渠道的**可用率**，让用户一眼看出问题规模。
	UsableCount int               `json:"usable_count"`
	TotalCount  int               `json:"total_count"`
	Models      []ChannelModelGap `json:"models"`
	// Broken 只列出不可用的，供面板直接渲染「待处理清单」。
	Broken []ChannelModelGap `json:"broken"`
}

// ChannelDiagnoseAll 诊断全部渠道（或指定渠道）。
func ChannelDiagnoseAll(ctx context.Context, channelID int) ([]ChannelDiagnose, error) {
	conn := db.GetDB()

	var channels []model.Channel
	query := conn.WithContext(ctx).Order("id")
	if channelID > 0 {
		query = query.Where("id = ?", channelID)
	}
	if err := query.Find(&channels).Error; err != nil {
		return nil, fmt.Errorf("load channels: %w", err)
	}

	out := make([]ChannelDiagnose, 0, len(channels))
	for _, channel := range channels {
		result, err := diagnoseChannel(ctx, channel)
		if err != nil {
			return nil, err
		}
		out = append(out, result)
	}
	return out, nil
}

func diagnoseChannel(ctx context.Context, channel model.Channel) (ChannelDiagnose, error) {
	conn := db.GetDB()
	result := ChannelDiagnose{
		ChannelID:   channel.ID,
		ChannelName: channel.Name,
		Enabled:     channel.Enabled,
	}

	// 启用的凭据：被禁用的凭据不算「可用的授权来源」。
	var keys []model.ChannelKey
	if err := conn.WithContext(ctx).Where("channel_id = ?", channel.ID).Find(&keys).Error; err != nil {
		return result, fmt.Errorf("load channel keys: %w", err)
	}
	keyNameByID := make(map[int]string, len(keys))
	enabledKeyIDs := make(map[int]bool, len(keys))
	for _, key := range keys {
		keyNameByID[key.ID] = key.Name
		if key.Enabled {
			enabledKeyIDs[key.ID] = true
		}
	}
	result.KeyCount = len(enabledKeyIDs)

	var models []model.ChannelModel
	if err := conn.WithContext(ctx).Where("channel_id = ?", channel.ID).Order("name").Find(&models).Error; err != nil {
		return result, fmt.Errorf("load channel models: %w", err)
	}
	if len(models) == 0 {
		return result, nil
	}

	modelIDs := make([]int, 0, len(models))
	modelByID := make(map[int]model.ChannelModel, len(models))
	for _, m := range models {
		modelIDs = append(modelIDs, m.ID)
		modelByID[m.ID] = m
	}

	var grants []model.ChannelGrant
	if err := conn.WithContext(ctx).Where("channel_model_id IN ?", modelIDs).Find(&grants).Error; err != nil {
		return result, fmt.Errorf("load channel grants: %w", err)
	}
	keyNamesByModel := make(map[int][]string, len(models))
	for _, grant := range grants {
		// 只认启用凭据上的授权：凭据禁用后那个授权等于不存在，
		// 把它算进来会让诊断给出「有授权」的错误结论。
		if !enabledKeyIDs[grant.ChannelKeyID] {
			continue
		}
		name := keyNameByID[grant.ChannelKeyID]
		if name == "" {
			name = fmt.Sprintf("#%d", grant.ChannelKeyID)
		}
		keyNamesByModel[grant.ChannelModelID] = append(keyNamesByModel[grant.ChannelModelID], name)
	}

	// 分组名 → 是否存在。一次查全，避免逐模型查库。
	groupNames := make(map[string]bool)
	var groups []model.Group
	if err := conn.WithContext(ctx).Where("name LIKE ?", channel.Name+"/%").Find(&groups).Error; err == nil {
		for _, g := range groups {
			groupNames[g.Name] = true
		}
	}

	result.TotalCount = len(models)
	for _, m := range models {
		gap := ChannelModelGap{ModelName: m.Name}
		names := keyNamesByModel[m.ID]
		sort.Strings(names)
		gap.GrantKeys = names
		groupName, err := AutoGroupName(channel.Name, m.Name)
		if err == nil {
			if groupNames[groupName] {
				gap.GroupName = groupName
			}
		}

		switch {
		case !channel.Enabled:
			gap.Usable = false
			gap.Reason = "渠道已停用：启用后才会参与选路"
		case result.KeyCount == 0:
			gap.Usable = false
			gap.Reason = "没有启用的凭据：该渠道下所有凭据都被禁用"
		case len(names) == 0:
			// 这是实测最常见的一种：模型配上了，但没有任何凭据被授权用它。
			gap.Usable = false
			gap.Reason = "没有凭据被授权使用该模型：需要在渠道编辑里为它勾选凭据，否则不会生成分组"
		case gap.GroupName == "":
			gap.Usable = false
			gap.Reason = "授权已存在但没有对应分组：保存渠道时会自动补建，或手动触发一次保存"
		default:
			gap.Usable = true
		}

		result.Models = append(result.Models, gap)
		if gap.Usable {
			result.UsableCount++
		} else {
			result.Broken = append(result.Broken, gap)
		}
	}
	return result, nil
}

// ChannelDiagnoseSummary 是跨渠道的汇总，回答「整个实例有多少模型用不上」。
type ChannelDiagnoseSummary struct {
	Channels        int `json:"channels"`
	TotalModels     int `json:"total_models"`
	UsableModels    int `json:"usable_models"`
	BrokenModels    int `json:"broken_models"`
	ChannelsWithGap int `json:"channels_with_gap"`
}

// SummarizeChannelDiagnose 汇总诊断结果，并给出可用率。
func SummarizeChannelDiagnose(items []ChannelDiagnose) ChannelDiagnoseSummary {
	summary := ChannelDiagnoseSummary{Channels: len(items)}
	for _, item := range items {
		summary.TotalModels += item.TotalCount
		summary.UsableModels += item.UsableCount
		summary.BrokenModels += len(item.Broken)
		if len(item.Broken) > 0 {
			summary.ChannelsWithGap++
		}
	}
	return summary
}

// DiagnoseReasonCounts 按原因归类不可用模型，让用户知道该优先修哪一类。
func DiagnoseReasonCounts(items []ChannelDiagnose) map[string]int {
	counts := make(map[string]int)
	for _, item := range items {
		for _, gap := range item.Broken {
			reason := gap.Reason
			if idx := strings.Index(reason, "："); idx > 0 {
				reason = reason[:idx]
			}
			counts[reason]++
		}
	}
	return counts
}
