package pooltest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/pool"
)

// 这一组用例检查的是**契约自检工具包本身**：好适配器要放行，坏适配器要被抓出来。
//
// 为什么要给检查器写测试：检查器一旦漏报，接入方会以为自己的适配器没问题；
// 一旦误报，接入方会去改本来正确的代码。两头都贵，所以每条规则都要有一个反例。

// goodAdapter 是一个满足契约的最小适配器（含所有它声明的能力）。
type goodAdapter struct {
	entries []pool.Entry
	fields  []pool.FieldSpec
	caps    []pool.Capability
}

func (g *goodAdapter) Info() pool.AdapterInfo {
	caps := g.caps
	if caps == nil {
		caps = []pool.Capability{pool.CapList, pool.CapGet, pool.CapProbe}
	}
	return pool.AdapterInfo{Kind: "good", Title: "正常后端", Capabilities: caps, Fields: g.fields}
}

func (g *goodAdapter) Entries(context.Context) ([]pool.Entry, error) {
	if g.entries == nil {
		return []pool.Entry{{ID: "1", Name: "一号"}}, nil
	}
	return g.entries, nil
}

func (g *goodAdapter) Get(_ context.Context, id string) (pool.Entry, error) {
	for _, entry := range g.entries {
		if entry.ID == id {
			return entry, nil
		}
	}
	return pool.Entry{}, pool.ErrEntryNotFound
}

func (g *goodAdapter) Probe(ctx context.Context, id string) (pool.Entry, error) {
	return g.Get(ctx, id)
}

func TestGoodAdapterPasses(t *testing.T) {
	adapter := &goodAdapter{
		entries: []pool.Entry{{ID: "1", Name: "一号", Kind: "good"}},
		fields:  []pool.FieldSpec{{Name: "access_token", Type: "string", Secret: true}, {Name: "plan", Type: "string"}},
	}
	if problems := Check(context.Background(), adapter); len(problems) != 0 {
		t.Fatalf("正常适配器不该报问题: %v", problems)
	}
}

// 空 kind / 缺 list 能力位 —— 外部工具没法选它。
func TestDetectsBadIdentity(t *testing.T) {
	problems := Check(context.Background(), &goodAdapter{caps: []pool.Capability{pool.CapGet}})
	if !hasProblem(problems, "必须声明 list") {
		t.Fatalf("应报缺 list: %v", problems)
	}

	noKind := &kindlessAdapter{}
	problems = Check(context.Background(), noKind)
	if !hasProblem(problems, "Kind 不能为空") || !hasProblem(problems, "必须声明 list") {
		t.Fatalf("应报空 kind + 缺 list: %v", problems)
	}
}

type kindlessAdapter struct{}

func (k *kindlessAdapter) Info() pool.AdapterInfo                        { return pool.AdapterInfo{} }
func (k *kindlessAdapter) Entries(context.Context) ([]pool.Entry, error) { return nil, nil }

// 未知能力位 / 重复声明。
func TestDetectsBadCapabilities(t *testing.T) {
	problems := Check(context.Background(), &goodAdapter{
		caps: []pool.Capability{pool.CapList, "teleport", pool.CapList},
	})
	if !hasProblem(problems, "不认识") || !hasProblem(problems, "重复声明") {
		t.Fatalf("应报未知能力位 + 重复: %v", problems)
	}
}

// 声明了能力位却没实现 —— 运行时容忍（回 501），但接入期的自检必须指出来。
func TestDetectsDeclaredButMissing(t *testing.T) {
	problems := Check(context.Background(), &declaredOnly{})
	if !hasProblem(problems, "没有实现对应接口") {
		t.Fatalf("应报声明了没实现: %v", problems)
	}
}

type declaredOnly struct{}

func (d *declaredOnly) Info() pool.AdapterInfo {
	// probe/toggle/sync 都声明了，但一个都没实现。
	return pool.AdapterInfo{Kind: "declared", Capabilities: []pool.Capability{
		pool.CapList, pool.CapProbe, pool.CapToggle, pool.CapSync,
	}}
}
func (d *declaredOnly) Entries(context.Context) ([]pool.Entry, error) { return nil, nil }

// 条目身份问题：空 ID、重复 ID、kind 回填错。
func TestDetectsBadEntries(t *testing.T) {
	problems := Check(context.Background(), &goodAdapter{entries: []pool.Entry{
		{ID: "", Name: "没有身份"},
		{ID: "dup", Name: "a"},
		{ID: "dup", Name: "b"},
		{ID: "3", Name: "c", Kind: "别的后端"},
	}})
	if !hasProblem(problems, "没有 ID") {
		t.Fatalf("应报空 ID: %v", problems)
	}
	if !hasProblem(problems, "ID 重复") {
		t.Fatalf("应报重复 ID: %v", problems)
	}
	if !hasProblem(problems, "不一致") {
		t.Fatalf("应报 kind 不一致: %v", problems)
	}
}

// 凭据从条目里漏出去 —— 这是最要紧的一条：标了 secret 就等于对外承诺"不回显"。
func TestDetectsSecretLeak(t *testing.T) {
	adapter := &goodAdapter{
		fields: []pool.FieldSpec{{Name: "access_token", Secret: true}, {Name: "refresh_token", Secret: true}},
		entries: []pool.Entry{
			{ID: "1", Labels: map[string]string{"access_token": "应该是密文也不该在这里"}},
		},
	}
	problems := Check(context.Background(), adapter)
	if !hasProblem(problems, "凭据字段") {
		t.Fatalf("应报凭据泄漏: %v", problems)
	}

	// 嵌套一层的 Detail 也要查得到。
	nested := &goodAdapter{
		fields: []pool.FieldSpec{{Name: "refresh_token", Secret: true}},
		entries: []pool.Entry{
			{ID: "1", Detail: map[string]any{"keys": map[string]any{"refresh_token": "x"}}},
		},
	}
	if problems := Check(context.Background(), nested); !hasProblem(problems, "凭据字段") {
		t.Fatalf("嵌套结构里的凭据也要报: %v", problems)
	}

	// 同名但不是 secret 的字段不算泄漏（避免误报把正常字段也判死）。
	fine := &goodAdapter{
		fields:  []pool.FieldSpec{{Name: "plan", Type: "string"}},
		entries: []pool.Entry{{ID: "1", Labels: map[string]string{"plan": "pro"}}},
	}
	if problems := Check(context.Background(), fine); len(problems) != 0 {
		t.Fatalf("非 secret 字段不该报: %v", problems)
	}
}

// Get 与列表不一致：列表里有、Get 却失败；以及 Get 对不存在的 ID 不报错。
func TestDetectsBadGetter(t *testing.T) {
	broken := &inconsistentGetter{entries: []pool.Entry{{ID: "1"}, {ID: "2"}}}
	problems := Check(context.Background(), broken)
	if !hasProblem(problems, "但条目是列表里给的") {
		t.Fatalf("应报 Get 取不到列表给的条目: %v", problems)
	}

	swapped := &idSwapGetter{}
	problems = Check(context.Background(), swapped)
	if !hasProblem(problems, "列表与详情口径不一致") {
		t.Fatalf("应报 Get 回的 ID 与列表不一致: %v", problems)
	}

	silent := &silentGetter{}
	problems = Check(context.Background(), silent)
	if !hasProblem(problems, "对不存在的条目没报错") {
		t.Fatalf("应报 Get 不报错: %v", problems)
	}
}

type inconsistentGetter struct {
	entries []pool.Entry
}

func (b *inconsistentGetter) Info() pool.AdapterInfo {
	return pool.AdapterInfo{Kind: "inconsistent", Capabilities: []pool.Capability{pool.CapList, pool.CapGet}}
}
func (b *inconsistentGetter) Entries(context.Context) ([]pool.Entry, error) { return b.entries, nil }
func (b *inconsistentGetter) Get(_ context.Context, id string) (pool.Entry, error) {
	// 只有 "1" 能取到：列表给了 2，详情给不出 → 口径不一致。
	if id == "1" {
		return pool.Entry{ID: "1", Kind: "inconsistent"}, nil
	}
	return pool.Entry{}, errors.New("取不到")
}

// idSwapGetter 列表给 "1"、详情却回别的 ID —— 外部工具按 ID 操作会打错目标。
type idSwapGetter struct{}

func (s *idSwapGetter) Info() pool.AdapterInfo {
	return pool.AdapterInfo{Kind: "idswap", Capabilities: []pool.Capability{pool.CapList, pool.CapGet}}
}
func (s *idSwapGetter) Entries(context.Context) ([]pool.Entry, error) {
	return []pool.Entry{{ID: "1"}}, nil
}
func (s *idSwapGetter) Get(context.Context, string) (pool.Entry, error) {
	return pool.Entry{ID: "别的", Kind: "idswap"}, nil
}

type silentGetter struct{}

func (s *silentGetter) Info() pool.AdapterInfo {
	return pool.AdapterInfo{Kind: "silent", Capabilities: []pool.Capability{pool.CapList, pool.CapGet}}
}
func (s *silentGetter) Entries(context.Context) ([]pool.Entry, error) {
	return []pool.Entry{{ID: "1"}}, nil
}
func (s *silentGetter) Get(context.Context, string) (pool.Entry, error) {
	// 不存在的 ID 也回空条目、不报错 —— 上层会把"没了"显示成"活着"。
	return pool.Entry{}, nil
}

// Entries 炸了不能带走测试进程，要变成一条可读问题。
func TestDetectsPanic(t *testing.T) {
	problems := Check(context.Background(), &panicAdapter{})
	if !hasProblem(problems, "panic") {
		t.Fatalf("应把 panic 兜成问题: %v", problems)
	}
}

type panicAdapter struct{}

func (p *panicAdapter) Info() pool.AdapterInfo {
	return pool.AdapterInfo{Kind: "panic", Capabilities: []pool.Capability{pool.CapList}}
}
func (p *panicAdapter) Entries(context.Context) ([]pool.Entry, error) { panic("后端炸了") }

// nil 适配器：外部工具传空进来时给一条明确问题，而不是崩。
func TestNilAdapter(t *testing.T) {
	if problems := Check(context.Background(), nil); len(problems) != 1 {
		t.Fatalf("nil 适配器应回一条问题: %v", problems)
	}
}

// Run 把问题变成测试失败（这里用子测试确认它真的会失败，避免"检查器永远绿灯"）。
func TestRunFailsBrokenAdapter(t *testing.T) {
	fake := &testing.T{}
	Run(fake, &declaredOnly{})
	if !fake.Failed() {
		t.Fatal("Run 应当让坏适配器的测试失败")
	}
}

func hasProblem(problems []string, needle string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, needle) {
			return true
		}
	}
	return false
}
