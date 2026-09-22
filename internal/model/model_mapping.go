package model

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

// ModelMatchType 定义模型名匹配规则类型。
type ModelMatchType string

const (
	ModelMatchExact    ModelMatchType = "exact"    // 精确匹配（忽略大小写）
	ModelMatchWildcard ModelMatchType = "wildcard" // 通配符匹配（支持 * 与 ?）
	ModelMatchRegex    ModelMatchType = "regex"    // 正则表达式匹配
)

// IsValid 检查匹配类型是否合法。
func (m ModelMatchType) IsValid() bool {
	switch m {
	case ModelMatchExact, ModelMatchWildcard, ModelMatchRegex:
		return true
	}
	return false
}

// ModelMapping 描述一条「客户端模型名 → 本地分组名」的重写规则。
//
// 解决的问题：客户端（Claude Code / Codex / Cherry Studio 等）常写死带版本后缀的模型名
// （claude-3-5-sonnet-20241022、gpt-4o-2024-11-20），而本地分组名是简名（claude-sonnet、gpt-4o），
// 客户端因此拿到 model not found，用户只能为每个版本后缀各建一个分组。
//
// 语义只有一条，不做多义：规则命中客户端的请求模型名时，把它改写成目标分组名，
// 再按改写后的名字去找分组。**未命中任何规则时逐字保持原名**——没配规则的项目行为与改造前完全一致。
//
// 刻意不做「按分组作用域生效」：那属于另一件事（改写发往上游的模型名），
// 与本表「定位本地分组」的职责混在一起会让两条链路都难排查，需要时另建字段。
type ModelMapping struct {
	ID          int            `json:"id" gorm:"primaryKey;autoIncrement"`
	Name        string         `json:"name" gorm:"size:255;not null"`         // 规则备注名称
	Pattern     string         `json:"pattern" gorm:"size:512;not null"`      // 匹配表达式
	MatchType   ModelMatchType `json:"match_type" gorm:"size:20;not null"`    // 匹配模式：exact / wildcard / regex
	TargetModel string         `json:"target_model" gorm:"size:255;not null"` // 重写后的目标分组名
	Priority    int            `json:"priority" gorm:"not null;default:0"`    // 优先级（数字越大越先匹配）
	Enabled     bool           `json:"enabled" gorm:"not null"`               // 是否启用
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// ModelMappingCreateRequest 是创建模型映射规则的载荷。
type ModelMappingCreateRequest struct {
	Name        string         `json:"name" binding:"required"`
	Pattern     string         `json:"pattern" binding:"required"`
	MatchType   ModelMatchType `json:"match_type" binding:"required,oneof=exact wildcard regex"`
	TargetModel string         `json:"target_model" binding:"required"`
	Priority    int            `json:"priority"`
	Enabled     *bool          `json:"enabled"` // 指针区分传了 false 还是没传（默认 true）
}

func (ModelMappingCreateRequest) TableName() string { return "-" }

// Validate 校验创建参数。
func (req *ModelMappingCreateRequest) Validate() error {
	req.Name = strings.TrimSpace(req.Name)
	req.Pattern = strings.TrimSpace(req.Pattern)
	req.TargetModel = strings.TrimSpace(req.TargetModel)
	if req.Name == "" {
		return errors.New("name is required")
	}
	if req.Pattern == "" {
		return errors.New("pattern is required")
	}
	if req.TargetModel == "" {
		return errors.New("target_model is required")
	}
	if !req.MatchType.IsValid() {
		return errors.New("invalid match_type: must be exact, wildcard or regex")
	}
	if req.MatchType == ModelMatchRegex {
		if _, err := regexp.Compile(req.Pattern); err != nil {
			return errors.New("invalid regex pattern: " + err.Error())
		}
	}
	return nil
}

// ModelMappingUpdateRequest 是更新模型映射规则的载荷。
type ModelMappingUpdateRequest struct {
	Name        *string         `json:"name"`
	Pattern     *string         `json:"pattern"`
	MatchType   *ModelMatchType `json:"match_type" binding:"omitempty,oneof=exact wildcard regex"`
	TargetModel *string         `json:"target_model"`
	Priority    *int            `json:"priority"`
	Enabled     *bool           `json:"enabled"`
}

func (ModelMappingUpdateRequest) TableName() string { return "-" }

// ModelMappingTestRequest 用于在控制面板或 API 侧直接测试当前模型名重写结果。
type ModelMappingTestRequest struct {
	ModelName string `json:"model_name" binding:"required"`
}

// ModelMappingTestResponse 报告测试重写的结果。
type ModelMappingTestResponse struct {
	OriginalModel string        `json:"original_model"`
	TargetModel   string        `json:"target_model"`
	Matched       bool          `json:"matched"`
	MatchedRule   *ModelMapping `json:"matched_rule,omitempty"`
}
