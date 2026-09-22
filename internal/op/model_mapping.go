package op

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/charmbracelet/log"
)

// 模型名智能重写（吸收上游 lingyuins/octopus 的 exact/wildcard/regex 三态设计）。
//
// 解决的问题：客户端（Claude Code / Codex / Cherry Studio 等）常写死带版本后缀的模型名
// （claude-3-5-sonnet-20241022、gpt-4o-2024-11-20），而本地分组名是简名（claude-sonnet、gpt-4o），
// 客户端因此拿到 model not found —— 用户只能为每个版本后缀再建一个分组。
//
// 做法：在「按模型名找分组」之前先过一层重写规则，命中即改写为目标分组名。
// 语义只有一条，不做多义：**未命中任何规则时逐字返回原名**，
// 这是本机制与"必须配规则"的分水岭——没配规则的项目行为与改造前完全一致。
//
// 为什么用内存缓存：这条判断在每一条转发请求上都会发生，走库会给每条请求多一次查询；
// 规则规模是"人手写的几条到几十条"，缓存代价可忽略。匹配全在内存完成：
// exact 走 EqualFold、wildcard 走贪婪回溯、regex 在刷新时预编译。
var modelMappingCache = struct {
	sync.RWMutex
	items []model.ModelMapping
	regex map[int]*regexp.Regexp
}{}

// ModelMappingRefresh 从库刷新模型映射缓存（启动与每次写操作后调用）。
//
// 两条容忍纪律（与 ManualSubscriptionRefresh 一致）：
//   - **表不存在时按"没有规则"处理并告警**，而不是让整次缓存初始化失败。部分建表的测试环境、
//     尚未跑到 AutoMigrate 的极端老库都可能缺这张表；把渠道/分组缓存一起拖挂会让实例起不来，
//     而"没有规则"恰好就是安全的默认行为（直通，零行为变化）。
//   - **解析失败的正则规则跳过而不是让整次刷新失败**：一条手误的正则不该让实例起不来，
//     但也不能静默吞掉——该规则在 List 结果里照常呈现（面板上看得见）。
func ModelMappingRefresh(ctx context.Context) error {
	conn := db.GetDB().WithContext(ctx)
	if !conn.Migrator().HasTable(&model.ModelMapping{}) {
		log.Warnf("model mappings: table missing, treating as empty (no rewrite rules)")
		modelMappingCache.Lock()
		modelMappingCache.items = nil
		modelMappingCache.regex = map[int]*regexp.Regexp{}
		modelMappingCache.Unlock()
		return nil
	}

	var items []model.ModelMapping
	if err := conn.Order("priority DESC, id ASC").Find(&items).Error; err != nil {
		return fmt.Errorf("load model mappings: %w", err)
	}

	compiled := make(map[int]*regexp.Regexp, len(items))
	for i := range items {
		item := items[i]
		if item.MatchType != model.ModelMatchRegex {
			continue
		}
		re, err := regexp.Compile(item.Pattern)
		if err != nil {
			log.Warnf("model mapping %d: invalid regex %q skipped: %v", item.ID, item.Pattern, err)
			continue
		}
		compiled[item.ID] = re
	}

	modelMappingCache.Lock()
	modelMappingCache.items = items
	modelMappingCache.regex = compiled
	modelMappingCache.Unlock()
	return nil
}

// ModelMappingList 返回全部规则（按优先级倒序，与匹配顺序一致）。
func ModelMappingList() []model.ModelMapping {
	modelMappingCache.RLock()
	defer modelMappingCache.RUnlock()
	out := make([]model.ModelMapping, len(modelMappingCache.items))
	copy(out, modelMappingCache.items)
	return out
}

// ModelMappingCount 返回缓存的规则条数（供状态面板使用）。
func ModelMappingCount() int {
	modelMappingCache.RLock()
	defer modelMappingCache.RUnlock()
	return len(modelMappingCache.items)
}

// ModelMappingResolve 按优先级顺序匹配并返回重写后的模型名，以及命中的规则。
//
// 未命中任何规则时**逐字返回原模型名**且规则为 nil。
func ModelMappingResolve(requestModel string) (string, *model.ModelMapping) {
	if requestModel == "" {
		return requestModel, nil
	}

	modelMappingCache.RLock()
	defer modelMappingCache.RUnlock()

	for i := range modelMappingCache.items {
		item := &modelMappingCache.items[i]
		if !item.Enabled {
			continue
		}
		if modelMappingMatch(item, modelMappingCache.regex[item.ID], requestModel) {
			return item.TargetModel, item
		}
	}
	return requestModel, nil
}

// ModelMappingResolveByName 是转发链路用的入口：报告"要不要改写"以及改成什么。
//
// 单独提供它的原因：转发链路里"原名能查到分组"是常见路径，此时调用方**不需要**拿规则对象，
// 只想知道改没改。matched 为 false 时调用方必须原样使用传入的名字。
func ModelMappingResolveByName(requestModel string) (string, bool) {
	resolved, rule := ModelMappingResolve(requestModel)
	return resolved, rule != nil
}

// ModelMappingDryRun 试跑一条规则（不落库），用于面板里的"测试"按钮与新建前的校验。
// 它按给定模式直接匹配、不看缓存，因此可以在保存前就告诉用户"这条规则会不会误伤"。
func ModelMappingDryRun(matchType model.ModelMatchType, pattern, modelName string) (bool, error) {
	switch matchType {
	case model.ModelMatchExact:
		return strings.EqualFold(modelName, pattern), nil
	case model.ModelMatchWildcard:
		return matchWildcard(pattern, modelName), nil
	case model.ModelMatchRegex:
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false, fmt.Errorf("invalid regex pattern: %w", err)
		}
		return re.MatchString(modelName), nil
	default:
		return false, fmt.Errorf("invalid match type: %s", matchType)
	}
}

// validateRegex 校验正则表达式是否可编译（写路径与更新路径共用）。
func validateRegex(pattern string) error {
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("invalid regex pattern: %w", err)
	}
	return nil
}

// ModelMappingTest 用当前生效的规则集去重写一个模型名，返回结果与命中的规则。
// 与 Resolve 的区别：它明确报告"命中了哪条"，供面板展示。
func ModelMappingTest(requestModel string) model.ModelMappingTestResponse {
	target, matchedRule := ModelMappingResolve(requestModel)
	resp := model.ModelMappingTestResponse{
		OriginalModel: requestModel,
		TargetModel:   target,
		Matched:       matchedRule != nil,
	}
	if matchedRule != nil {
		copied := *matchedRule
		resp.MatchedRule = &copied
	}
	return resp
}

// modelMappingMatch 判断一条规则是否命中。re 为该规则预编译的正则（非 regex 类型时为 nil）。
func modelMappingMatch(item *model.ModelMapping, re *regexp.Regexp, requestModel string) bool {
	switch item.MatchType {
	case model.ModelMatchExact:
		return strings.EqualFold(requestModel, item.Pattern)
	case model.ModelMatchWildcard:
		return matchWildcard(item.Pattern, requestModel)
	case model.ModelMatchRegex:
		if re == nil {
			return false
		}
		return re.MatchString(requestModel)
	default:
		return false
	}
}

// matchWildcard 是 glob 风格匹配：* 匹配任意长度（含空），? 匹配单个字符。
// 大小写不敏感：模型名的大小写在不同客户端之间并不统一。
//
// 用经典的"双指针 + 回溯星号位置"算法，最坏 O(n·m)、通常近线性；
// 不翻译成正则，是为了避免用户在 pattern 里写正则元字符（.、+、()）时被静默赋予特殊含义。
func matchWildcard(pattern, s string) bool {
	pattern = strings.ToLower(pattern)
	s = strings.ToLower(s)

	pIdx, sIdx := 0, 0
	starIdx, match := -1, 0

	for sIdx < len(s) {
		if pIdx < len(pattern) && (pattern[pIdx] == '?' || pattern[pIdx] == s[sIdx]) {
			pIdx++
			sIdx++
			continue
		}
		if pIdx < len(pattern) && pattern[pIdx] == '*' {
			starIdx = pIdx
			match = sIdx
			pIdx++
			continue
		}
		if starIdx != -1 {
			pIdx = starIdx + 1
			match++
			sIdx = match
			continue
		}
		return false
	}

	for pIdx < len(pattern) && pattern[pIdx] == '*' {
		pIdx++
	}
	return pIdx == len(pattern)
}
