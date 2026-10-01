package op

// 分组快速建立（需求1）：选一个渠道 + 模型（可再指定某条凭据），一键建组。
//
// # 为什么需要这个入口
//
// 常规建组要在编辑器里逐个挑授权，而最常见的诉求恰恰是最机械的那一种：
// 「把 53HK-L 这个渠道的 claude-fable-5 拉成一个分组」。备份里已有 202 个
// `渠道/模型` 格式的分组全是手工建的 —— 这个入口就是把它变成一次点击。
//
// # 命名与折叠
//
// 组名固定 `<渠道名>/<模型名>`。前端按 `/` 前的前缀折叠，只显示渠道名 ——
// 一个渠道有几十个模型时，列表才不会被同名渠道撑爆。
//
// # 成员怎么选
//
// 授权是 (渠道模型, 凭据) 的唯一对。给定 (渠道, 模型) 后：
//   - 不指定凭据 → 该模型下**所有可用凭据**都进组（天然具备故障转移能力）
//   - 指定凭据   → 只用那一条
// 一条都取不到就报错并说清原因，不建空分组（空分组调不通，且用户看不出为什么）。

import (
	"context"
	"fmt"
	"strings"

	"github.com/t-yang-only/OctoNexus/internal/db"
	"github.com/t-yang-only/OctoNexus/internal/model"
)

// QuickGroupRequest 是一次快速建组的请求。
type QuickGroupRequest struct {
	ChannelID int    `json:"channel_id" binding:"required"`
	ModelName string `json:"model_name" binding:"required"`
	// KeyID 可空：空 = 该模型下所有可用凭据都进组。
	KeyID int `json:"key_id"`
	// Mode 可空：空 = failover（多凭据时这才有意义；单凭据时与 manual 等价）。
	Mode model.GroupMode `json:"mode" binding:"omitempty,oneof=manual failover lowest_cost quality_first lowest_latency least_busy lowest_tpm_rpm weighted smart allocate"`
}

// QuickGroupResult 回带建好的分组与入选凭据，让界面能说明"这个组里有谁"。
type QuickGroupResult struct {
	Group     *model.Group `json:"group"`
	GrantIDs  []int        `json:"grant_ids"`
	KeyNames  []string     `json:"key_names"`
	ChannelID int          `json:"channel_id"`
	Reused    bool         `json:"reused"` // true = 分组已存在，直接复用
}

// GroupQuickCreate 按 (渠道, 模型[, 凭据]) 一键建组。
//
// 幂等：同名分组已存在且成员一致时直接复用（Reused=true），不报错——
// 用户重复点同一个模型不该看到一片红色。
func GroupQuickCreate(ctx context.Context, req *QuickGroupRequest) (*QuickGroupResult, error) {
	req.ModelName = strings.TrimSpace(req.ModelName)
	if req.ModelName == "" {
		return nil, fmt.Errorf("model name is required")
	}
	channel, err := ChannelGet(req.ChannelID)
	if err != nil {
		return nil, fmt.Errorf("lookup channel: %w", err)
	}
	if channel.Name == "" {
		return nil, fmt.Errorf("channel %d has no name", req.ChannelID)
	}

	grants, keyNames, err := quickGroupGrants(req.ChannelID, req.ModelName, req.KeyID)
	if err != nil {
		return nil, err
	}
	if len(grants) == 0 {
		if req.KeyID > 0 {
			return nil, fmt.Errorf("渠道 %s 下模型 %s 没有可用凭据（指定的凭据可能已禁用或不覆盖该模型）", channel.Name, req.ModelName)
		}
		return nil, fmt.Errorf("渠道 %s 下模型 %s 没有可用凭据（可能未探测到模型，或凭据全部禁用）", channel.Name, req.ModelName)
	}

	name := channel.Name + "/" + req.ModelName
	items := make([]model.GroupItemInput, 0, len(grants))
	for _, id := range grants {
		items = append(items, model.GroupItemInput{ChannelGrantID: id})
	}

	// 已存在同名分组：核对成员是否一致，一致则复用。
	if existing, err := findGroupByName(ctx, name); err == nil && existing != nil {
		if sameGrantSet(existing, grants) {
			return &QuickGroupResult{Group: existing, GrantIDs: grants, KeyNames: keyNames,
				ChannelID: req.ChannelID, Reused: true}, nil
		}
		return nil, fmt.Errorf("分组 %s 已存在但成员不同：请到分组详情里调整，或先删掉它", name)
	}

	mode := req.Mode
	if mode == "" {
		mode = model.GroupModeFailover
	}
	group, err := GroupCreate(&model.GroupCreateRequest{
		Name:  name,
		Mode:  mode,
		Items: items,
	}, ctx)
	if err != nil {
		return nil, err
	}
	return &QuickGroupResult{Group: group, GrantIDs: grants, KeyNames: keyNames,
		ChannelID: req.ChannelID}, nil
}

// quickGroupGrants 找出 (渠道, 模型) 下可用的授权 ID 与凭据名。
//
// 可用 = 渠道启用 + 凭据启用 + 模型存在。这三条与 ChannelGrantCandidate.Available
// 同源，所以直接复用它，不另写一套判定（两套判定必然分叉）。
//
// keyID > 0 时只取该凭据的授权，按 **ID** 比对而不是名字（凭据名在渠道内唯一，
// 但跨渠道会重名，按名字筛会把别的渠道的授权卷进来）。
func quickGroupGrants(channelID int, modelName string, keyID int) ([]int, []string, error) {
	var grants []int
	var names []string
	seen := make(map[int]struct{})
	for _, cand := range ChannelGrantCandidates() {
		if cand.ChannelID != channelID || cand.ModelName != modelName || !cand.Available {
			continue
		}
		if keyID > 0 && cand.KeyID != keyID {
			continue
		}
		if _, dup := seen[cand.ID]; dup {
			continue
		}
		seen[cand.ID] = struct{}{}
		grants = append(grants, cand.ID)
		names = append(names, cand.KeyName)
	}
	return grants, names, nil
}

// findGroupByName 按名字找分组（大小写敏感，与建组时的命名保持一致）。
//
// **必须 Preload Items**：幂等判断要拿现有成员与新算出的授权集合比对，
// 不预加载时 group.Items 恒为空，比对必然失败 —— 表现为"重复点同一个模型
// 报成员不同"，把幂等彻底废掉（本轮实测踩过）。
func findGroupByName(ctx context.Context, name string) (*model.Group, error) {
	var group model.Group
	err := db.GetDB().WithContext(ctx).
		Preload("Items").
		Where("name = ?", name).First(&group).Error
	if err != nil {
		return nil, err
	}
	return &group, nil
}

// sameGrantSet 判断现有分组的成员是否正好是这批授权（顺序无关）。
func sameGrantSet(group *model.Group, grants []int) bool {
	if group == nil || len(group.Items) != len(grants) {
		return false
	}
	want := make(map[int]struct{}, len(grants))
	for _, id := range grants {
		want[id] = struct{}{}
	}
	for _, item := range group.Items {
		if item.ChannelGrantID == nil {
			return false
		}
		if _, ok := want[*item.ChannelGrantID]; !ok {
			return false
		}
	}
	return true
}
