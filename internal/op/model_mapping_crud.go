package op

import (
	"context"
	"fmt"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// 模型映射的写路径：每次写操作后都刷新缓存，让面板上改完立即对转发生效
// （与设置项的"热生效"一致——用户不该为了加一条规则去重启实例）。

// ModelMappingCreate 新建一条规则。
func ModelMappingCreate(ctx context.Context, req *model.ModelMappingCreateRequest) (*model.ModelMapping, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	item := &model.ModelMapping{
		Name:        req.Name,
		Pattern:     req.Pattern,
		MatchType:   req.MatchType,
		TargetModel: req.TargetModel,
		Priority:    req.Priority,
		Enabled:     enabled,
	}
	if err := db.GetDB().WithContext(ctx).Create(item).Error; err != nil {
		return nil, fmt.Errorf("create model mapping: %w", err)
	}
	if err := ModelMappingRefresh(ctx); err != nil {
		return nil, err
	}
	return item, nil
}

// ModelMappingUpdate 按主键更新规则（只改传了的字段）。
func ModelMappingUpdate(ctx context.Context, id int, req *model.ModelMappingUpdateRequest) (*model.ModelMapping, error) {
	var item model.ModelMapping
	if err := db.GetDB().WithContext(ctx).First(&item, id).Error; err != nil {
		return nil, fmt.Errorf("find model mapping %d: %w", id, err)
	}

	updates := map[string]any{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, fmt.Errorf("name is required")
		}
		updates["name"] = name
	}
	matchType := item.MatchType
	if req.MatchType != nil {
		if !req.MatchType.IsValid() {
			return nil, fmt.Errorf("invalid match_type: must be exact, wildcard or regex")
		}
		matchType = *req.MatchType
		updates["match_type"] = matchType
	}
	pattern := item.Pattern
	if req.Pattern != nil {
		pattern = strings.TrimSpace(*req.Pattern)
		if pattern == "" {
			return nil, fmt.Errorf("pattern is required")
		}
		updates["pattern"] = pattern
	}
	// 正则合法性要在"最终形态"上校验：改了类型但没改表达式时也要校验（换类型可能让原表达式失效）。
	if matchType == model.ModelMatchRegex {
		if err := validateRegex(pattern); err != nil {
			return nil, err
		}
	}
	if req.TargetModel != nil {
		target := strings.TrimSpace(*req.TargetModel)
		if target == "" {
			return nil, fmt.Errorf("target_model is required")
		}
		updates["target_model"] = target
	}
	if req.Priority != nil {
		updates["priority"] = *req.Priority
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}

	if len(updates) > 0 {
		if err := db.GetDB().WithContext(ctx).Model(&item).Updates(updates).Error; err != nil {
			return nil, fmt.Errorf("update model mapping %d: %w", id, err)
		}
	}

	if err := ModelMappingRefresh(ctx); err != nil {
		return nil, err
	}
	if err := db.GetDB().WithContext(ctx).First(&item, id).Error; err != nil {
		return nil, fmt.Errorf("reload model mapping %d: %w", id, err)
	}
	return &item, nil
}

// ModelMappingDelete 按主键删除规则。
func ModelMappingDelete(ctx context.Context, id int) error {
	if err := db.GetDB().WithContext(ctx).Delete(&model.ModelMapping{}, id).Error; err != nil {
		return fmt.Errorf("delete model mapping %d: %w", id, err)
	}
	return ModelMappingRefresh(ctx)
}

// ModelMappingGet 按主键取规则。
func ModelMappingGet(ctx context.Context, id int) (*model.ModelMapping, error) {
	var item model.ModelMapping
	if err := db.GetDB().WithContext(ctx).First(&item, id).Error; err != nil {
		return nil, fmt.Errorf("get model mapping %d: %w", id, err)
	}
	return &item, nil
}

// ModelMappingToggle 切换启用状态（面板上的开关）。
func ModelMappingToggle(ctx context.Context, id int, enabled bool) (*model.ModelMapping, error) {
	if err := db.GetDB().WithContext(ctx).Model(&model.ModelMapping{}).
		Where("id = ?", id).Update("enabled", enabled).Error; err != nil {
		return nil, fmt.Errorf("toggle model mapping %d: %w", id, err)
	}
	if err := ModelMappingRefresh(ctx); err != nil {
		return nil, err
	}
	return ModelMappingGet(ctx, id)
}
