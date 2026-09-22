package op

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// 模型名智能重写的判别性用例（吸收上游 lingyuins/octopus 的 exact/wildcard/regex 三态设计）。
//
// 这套用例守住的核心不变量是**向后兼容**：没有任何规则命中时，重写必须逐字返回原模型名，
// 否则所有现存部署（谁都没配规则）会在升级后集体 model not found。

// setupModelMappingDB 准备一个可用的库。
//
// 用包级 once + 固定临时文件而不是 t.TempDir()：db.InitDB 是全局单例，
// 每个用例各起一个库会不断替换全局 db 并泄漏上一次的连接（Windows 上表现为
// "TempDir RemoveAll cleanup: file is being used by another process"）。
// 这里只初始化一次，用例之间靠 resetMappings 清规则来隔离。
var modelMappingDBOnce sync.Once

func setupModelMappingDB(t *testing.T) {
	t.Helper()
	modelMappingDBOnce.Do(func() {
		dsn := filepath.Join(os.TempDir(), "octopus-model-mapping-test.db")
		_ = os.Remove(dsn)
		if err := db.InitDB("sqlite", dsn, false); err != nil {
			t.Fatalf("init db: %v", err)
		}
	})
	if db.GetDB() == nil {
		t.Fatalf("db 未初始化")
	}
	if err := db.GetDB().AutoMigrate(&model.ModelMapping{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := ModelMappingRefresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
}

func resetMappings(t *testing.T) {
	t.Helper()
	if err := db.GetDB().Where("1 = 1").Delete(&model.ModelMapping{}).Error; err != nil {
		t.Fatalf("clear mappings: %v", err)
	}
	if err := ModelMappingRefresh(context.Background()); err != nil {
		t.Fatalf("refresh after clear: %v", err)
	}
}

func mustCreate(t *testing.T, req *model.ModelMappingCreateRequest) *model.ModelMapping {
	t.Helper()
	item, err := ModelMappingCreate(context.Background(), req)
	if err != nil {
		t.Fatalf("create %q: %v", req.Name, err)
	}
	return item
}

func intPtr(i int) *int { return &i }

// 未命中任何规则时必须逐字保持原模型名（升级安全性，本机制最重要的一条）。
func TestModelMappingResolvePassthroughWhenNoRule(t *testing.T) {
	setupModelMappingDB(t)
	resetMappings(t)

	got, rule := ModelMappingResolve("claude-3-5-sonnet-20241022")
	if got != "claude-3-5-sonnet-20241022" {
		t.Fatalf("未命中规则时不该改写: got %q", got)
	}
	if rule != nil {
		t.Fatalf("未命中规则时不该返回规则: got %+v", rule)
	}
}

// 空名（客户端没给 model 字段）也必须原样返回，不能 panic 或误命中。
func TestModelMappingResolveEmptyName(t *testing.T) {
	setupModelMappingDB(t)
	resetMappings(t)
	mustCreate(t, &model.ModelMappingCreateRequest{
		Name: "全匹配", Pattern: "*", MatchType: model.ModelMatchWildcard, TargetModel: "x",
	})
	if got, rule := ModelMappingResolve(""); got != "" || rule != nil {
		t.Fatalf("空模型名不该被匹配: got %q rule=%+v", got, rule)
	}
}

// exact 匹配忽略大小写；且不得命中前缀（exact 就是 exact）。
func TestModelMappingExactIsCaseInsensitiveAndExact(t *testing.T) {
	setupModelMappingDB(t)
	resetMappings(t)
	mustCreate(t, &model.ModelMappingCreateRequest{
		Name: "claude 官方全名", Pattern: "claude-3-5-sonnet-20241022",
		MatchType: model.ModelMatchExact, TargetModel: "claude-sonnet", Enabled: boolPtr(true),
	})

	if got, _ := ModelMappingResolve("CLAUDE-3-5-SONNET-20241022"); got != "claude-sonnet" {
		t.Fatalf("exact 应忽略大小写: got %q", got)
	}
	if got, _ := ModelMappingResolve("claude-3-5-sonnet"); got != "claude-3-5-sonnet" {
		t.Fatalf("exact 不该命中前缀: got %q", got)
	}
}

// wildcard 支持 * 与 ?，大小写不敏感；正则元字符在其中没有特殊含义。
func TestModelMappingWildcard(t *testing.T) {
	cases := []struct {
		pattern string
		input   string
		want    bool
	}{
		{"claude-*-2024*", "claude-3-5-sonnet-20241022", true},
		{"claude-*-2024*", "claude-3-5-sonnet", false},
		{"gpt-4o", "gpt-4o", true},
		{"gpt-4?", "gpt-4o", true},
		{"gpt-4?", "gpt-4oo", false},
		{"*sonnet*", "claude-3-5-sonnet-20241022", true},
		{"*", "anything", true},
		{"gpt-4o", "GPT-4O", true},
		// 点号在通配符里是普通字符，不是"任意字符"。
		{"gpt.4o", "gptX4o", false},
		{"gpt.4o", "gpt.4o", true},
		// 尾部星号要吃满剩余部分。
		{"abc*", "abcdef", true},
		{"abc*", "ab", false},
	}
	for _, c := range cases {
		got, err := ModelMappingDryRun(model.ModelMatchWildcard, c.pattern, c.input)
		if err != nil {
			t.Fatalf("dry run %q/%q: %v", c.pattern, c.input, err)
		}
		if got != c.want {
			t.Fatalf("wildcard %q vs %q = %v, want %v", c.pattern, c.input, got, c.want)
		}
	}
}

// 优先级：数字大的先匹配（先命中先返回），与兜底规则的先后关系必须可预期。
func TestModelMappingPriorityOrder(t *testing.T) {
	setupModelMappingDB(t)
	resetMappings(t)
	ctx := context.Background()

	// 低优先级：把所有 claude-* 都送去便宜分组。
	mustCreate(t, &model.ModelMappingCreateRequest{
		Name: "兜底", Pattern: "claude-*", MatchType: model.ModelMatchWildcard,
		TargetModel: "cheap-group", Priority: 1, Enabled: boolPtr(true),
	})
	// 高优先级：把最强的那个型号单独送去强分组。
	mustCreate(t, &model.ModelMappingCreateRequest{
		Name: "opus 专用", Pattern: "claude-opus-5", MatchType: model.ModelMatchExact,
		TargetModel: "strong-group", Priority: 100, Enabled: boolPtr(true),
	})

	if got, _ := ModelMappingResolve("claude-opus-5"); got != "strong-group" {
		t.Fatalf("高优先级应先生效: got %q", got)
	}
	if got, _ := ModelMappingResolve("claude-haiku-4-5"); got != "cheap-group" {
		t.Fatalf("未命中高优先级时应落到兜底: got %q", got)
	}

	// 把兜底提到最高：同一条输入应改由它命中（验证优先级是排序键而不是插入顺序）。
	list := ModelMappingList()
	if len(list) != 2 {
		t.Fatalf("应有 2 条规则: %d", len(list))
	}
	var lowID int
	for _, m := range list {
		if m.Name == "兜底" {
			lowID = m.ID
		}
	}
	if _, err := ModelMappingUpdate(ctx, lowID, &model.ModelMappingUpdateRequest{Priority: intPtr(999)}); err != nil {
		t.Fatalf("update priority: %v", err)
	}
	if got, _ := ModelMappingResolve("claude-opus-5"); got != "cheap-group" {
		t.Fatalf("提高优先级后应改由兜底命中: got %q", got)
	}
}

// 停用的规则不得参与匹配。
func TestModelMappingDisabledIsSkipped(t *testing.T) {
	setupModelMappingDB(t)
	resetMappings(t)
	mustCreate(t, &model.ModelMappingCreateRequest{
		Name: "停用", Pattern: "gpt-4o", MatchType: model.ModelMatchExact,
		TargetModel: "other", Enabled: boolPtr(false),
	})
	if got, _ := ModelMappingResolve("gpt-4o"); got != "gpt-4o" {
		t.Fatalf("停用规则不该生效: got %q", got)
	}
}

// 开关切换后立即生效（热生效，不重启）。
func TestModelMappingToggleTakesEffectImmediately(t *testing.T) {
	setupModelMappingDB(t)
	resetMappings(t)
	ctx := context.Background()

	item := mustCreate(t, &model.ModelMappingCreateRequest{
		Name: "开关", Pattern: "toggle-me", MatchType: model.ModelMatchExact,
		TargetModel: "toggled", Enabled: boolPtr(true),
	})
	if got, _ := ModelMappingResolve("toggle-me"); got != "toggled" {
		t.Fatalf("启用时应命中: got %q", got)
	}
	if _, err := ModelMappingToggle(ctx, item.ID, false); err != nil {
		t.Fatalf("toggle off: %v", err)
	}
	if got, _ := ModelMappingResolve("toggle-me"); got != "toggle-me" {
		t.Fatalf("停用后不该命中: got %q", got)
	}
	if _, err := ModelMappingToggle(ctx, item.ID, true); err != nil {
		t.Fatalf("toggle on: %v", err)
	}
	if got, _ := ModelMappingResolve("toggle-me"); got != "toggled" {
		t.Fatalf("重新启用后应命中: got %q", got)
	}
}

// 非法正则必须在写入口被拒绝（不能等到转发时才炸）。
func TestModelMappingRejectsInvalidRegex(t *testing.T) {
	setupModelMappingDB(t)
	resetMappings(t)

	_, err := ModelMappingCreate(context.Background(), &model.ModelMappingCreateRequest{
		Name: "坏正则", Pattern: "claude-([unclosed", MatchType: model.ModelMatchRegex,
		TargetModel: "x", Enabled: boolPtr(true),
	})
	if err == nil {
		t.Fatalf("非法正则应被拒绝")
	}
}

// 更新时改类型也要校验正则：改了 match_type 但没改 pattern 时，原表达式可能已经非法。
func TestModelMappingUpdateValidatesRegexOnFinalShape(t *testing.T) {
	setupModelMappingDB(t)
	resetMappings(t)
	ctx := context.Background()

	// 先建一条合法 wildcard，pattern 恰好是非法正则。
	item := mustCreate(t, &model.ModelMappingCreateRequest{
		Name: "wildcard 合法", Pattern: "claude-([unclosed", MatchType: model.ModelMatchWildcard,
		TargetModel: "x", Enabled: boolPtr(true),
	})
	// 只改类型为 regex，不传 pattern —— 必须在最终形态上校验并拒绝。
	mt := model.ModelMatchRegex
	if _, err := ModelMappingUpdate(ctx, item.ID, &model.ModelMappingUpdateRequest{MatchType: &mt}); err == nil {
		t.Fatalf("改成 regex 时应在最终形态上校验并拒绝非法表达式")
	}
}

// 正则规则命中。
func TestModelMappingRegex(t *testing.T) {
	setupModelMappingDB(t)
	resetMappings(t)
	mustCreate(t, &model.ModelMappingCreateRequest{
		Name: "sonnet 全版本", Pattern: `^claude-.*-sonnet-\d{8}$`, MatchType: model.ModelMatchRegex,
		TargetModel: "claude-sonnet", Enabled: boolPtr(true),
	})

	if got, _ := ModelMappingResolve("claude-3-5-sonnet-20241022"); got != "claude-sonnet" {
		t.Fatalf("正则应命中: got %q", got)
	}
	if got, _ := ModelMappingResolve("claude-3-5-sonnet"); got != "claude-3-5-sonnet" {
		t.Fatalf("正则不该命中缺日期后缀的: got %q", got)
	}
}

// 测试端点（面板用）应报告命中的是哪一条。
func TestModelMappingTestReportsMatchedRule(t *testing.T) {
	setupModelMappingDB(t)
	resetMappings(t)

	created := mustCreate(t, &model.ModelMappingCreateRequest{
		Name: "命中我", Pattern: "gpt-4o-*", MatchType: model.ModelMatchWildcard,
		TargetModel: "gpt-4o", Enabled: boolPtr(true),
	})

	resp := ModelMappingTest("gpt-4o-2024-11-20")
	if !resp.Matched || resp.TargetModel != "gpt-4o" {
		t.Fatalf("应命中并改写: %+v", resp)
	}
	if resp.MatchedRule == nil || resp.MatchedRule.ID != created.ID {
		t.Fatalf("应报告命中的规则: %+v", resp.MatchedRule)
	}

	miss := ModelMappingTest("unrelated-model")
	if miss.Matched || miss.TargetModel != "unrelated-model" {
		t.Fatalf("未命中应原样返回: %+v", miss)
	}
}

// 删除后缓存立即刷新（不重启即生效）。
func TestModelMappingDeleteRefreshesCache(t *testing.T) {
	setupModelMappingDB(t)
	resetMappings(t)
	ctx := context.Background()

	item := mustCreate(t, &model.ModelMappingCreateRequest{
		Name: "待删", Pattern: "to-delete", MatchType: model.ModelMatchExact,
		TargetModel: "gone", Enabled: boolPtr(true),
	})
	if got, _ := ModelMappingResolve("to-delete"); got != "gone" {
		t.Fatalf("删除前应命中: got %q", got)
	}
	if err := ModelMappingDelete(ctx, item.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, _ := ModelMappingResolve("to-delete"); got != "to-delete" {
		t.Fatalf("删除后不该命中: got %q", got)
	}
	if _, err := ModelMappingGet(ctx, item.ID); err == nil {
		t.Fatalf("删除后按主键取应报错")
	}
}

// ResolveByName 是转发链路入口：命中时 matched=true，未命中时调用方必须原样使用传入名。
func TestModelMappingResolveByNameContract(t *testing.T) {
	setupModelMappingDB(t)
	resetMappings(t)
	mustCreate(t, &model.ModelMappingCreateRequest{
		Name: "链路入口", Pattern: "legacy-name", MatchType: model.ModelMatchExact,
		TargetModel: "new-name", Enabled: boolPtr(true),
	})

	if got, matched := ModelMappingResolveByName("legacy-name"); !matched || got != "new-name" {
		t.Fatalf("命中时应返回目标名且 matched=true: got %q matched=%v", got, matched)
	}
	if got, matched := ModelMappingResolveByName("untouched"); matched || got != "untouched" {
		t.Fatalf("未命中时应原样返回且 matched=false: got %q matched=%v", got, matched)
	}
}

// 校验：必填项与非法匹配类型都要被挡在写入口。
func TestModelMappingCreateValidation(t *testing.T) {
	base := func() *model.ModelMappingCreateRequest {
		return &model.ModelMappingCreateRequest{
			Name: "n", Pattern: "p", MatchType: model.ModelMatchExact, TargetModel: "t",
		}
	}
	cases := []struct {
		name   string
		mutate func(*model.ModelMappingCreateRequest)
	}{
		{"空名称", func(r *model.ModelMappingCreateRequest) { r.Name = "  " }},
		{"空表达式", func(r *model.ModelMappingCreateRequest) { r.Pattern = "" }},
		{"空目标", func(r *model.ModelMappingCreateRequest) { r.TargetModel = " " }},
		{"非法匹配类型", func(r *model.ModelMappingCreateRequest) { r.MatchType = "fuzzy" }},
	}
	for _, c := range cases {
		req := base()
		c.mutate(req)
		if err := req.Validate(); err == nil {
			t.Fatalf("%s: 应校验失败", c.name)
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("合法请求不该失败: %v", err)
	}
}

// 保存前后空白必须被裁掉：用户从别处复制模型名常带尾随空格，不裁会静默不命中。
func TestModelMappingTrimsWhitespace(t *testing.T) {
	setupModelMappingDB(t)
	resetMappings(t)

	item := mustCreate(t, &model.ModelMappingCreateRequest{
		Name: "  带空格  ", Pattern: "  spaced-model  ", MatchType: model.ModelMatchExact,
		TargetModel: "  target-group  ", Enabled: boolPtr(true),
	})
	if item.Pattern != "spaced-model" || item.TargetModel != "target-group" || item.Name != "带空格" {
		t.Fatalf("空白未被裁剪: %+v", item)
	}
	if got, _ := ModelMappingResolve("spaced-model"); got != "target-group" {
		t.Fatalf("裁剪后应能命中: got %q", got)
	}
}

// DryRun 对非法匹配类型必须报错，而不是静默返回 false（否则面板会把配置错误显示成"不匹配"）。
func TestModelMappingDryRunReportsBadType(t *testing.T) {
	if _, err := ModelMappingDryRun("weird", "x", "y"); err == nil {
		t.Fatal("非法匹配类型应报错")
	}
}
