package pool

import (
	"context"
	"errors"
	"testing"
)

// fakeAdapter 是注册表单测用的适配器：能按需返回条目或错误。
type fakeAdapter struct {
	kind    string
	entries []Entry
	err     error
	caps    []Capability
}

func (f fakeAdapter) Info() AdapterInfo {
	caps := f.caps
	if caps == nil {
		caps = []Capability{CapList}
	}
	return AdapterInfo{Kind: f.kind, Title: "假适配器 " + f.kind, Capabilities: caps}
}

func (f fakeAdapter) Entries(context.Context) ([]Entry, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.entries, nil
}

func withFake(t *testing.T, adapter Adapter) {
	t.Helper()
	kind := adapter.Info().Kind
	if err := Register(adapter); err != nil {
		t.Fatalf("register %s: %v", kind, err)
	}
	t.Cleanup(func() { unregister(kind) })
}

// withoutBuiltin 在"枚举全部 kind"的用例里临时摘掉内置适配器。
//
// 内置的官方账号池适配器要读数据库：单测环境没有初始化 DB, 直接枚举会走到 op 层并 panic
// （生产环境有默认 DB, 所以只有单测需要隔离）。摘掉再装回, 别的用例不受影响。
func withoutBuiltin(t *testing.T) {
	t.Helper()
	if Has(officialKind) {
		unregister(officialKind)
		t.Cleanup(func() {
			if err := Register(officialAdapter{}); err != nil {
				t.Fatalf("恢复内置适配器失败: %v", err)
			}
		})
	}
}

// TestRegisterRejectsBadAdapters 注册期就把坏适配器挡住：空 kind、缺 list 能力、未知能力位、重复注册。
func TestRegisterRejectsBadAdapters(t *testing.T) {
	cases := []struct {
		name    string
		adapter Adapter
		want    error
	}{
		{"nil", nil, ErrInvalidAdapter},
		{"empty kind", fakeAdapter{}, ErrInvalidAdapter},
		{"no list capability", fakeAdapter{kind: "no-list", caps: []Capability{CapProbe}}, ErrInvalidAdapter},
		{"unknown capability", fakeAdapter{kind: "weird", caps: []Capability{CapList, "teleport"}}, ErrInvalidAdapter},
	}
	for _, tc := range cases {
		if err := Register(tc.adapter); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	withFake(t, fakeAdapter{kind: "dup"})
	if err := Register(fakeAdapter{kind: "dup"}); !errors.Is(err, ErrDuplicateKind) {
		t.Errorf("duplicate register err = %v, want ErrDuplicateKind", err)
	}
}

// TestEntriesFiltersByKindAndRejectsUnknown kind 为空取全部；指定 kind 时只取那一种；未知 kind 报错。
func TestEntriesFiltersByKindAndRejectsUnknown(t *testing.T) {
	withoutBuiltin(t)
	withFake(t, fakeAdapter{kind: "alpha", entries: []Entry{{ID: "a1", Name: "甲"}, {ID: "a2"}}})
	withFake(t, fakeAdapter{kind: "beta", entries: []Entry{{ID: "b1", Name: "乙", Enabled: true, Healthy: true}}})

	all, failures, err := Entries(context.Background(), "")
	if err != nil {
		t.Fatalf("entries(all): %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %+v", failures)
	}
	if len(all) != 3 {
		t.Fatalf("all entries = %d, want 3", len(all))
	}
	// 全量视图按 kind 排序稳定, 且 kind 由注册表回填; 同 kind 内按 provider 再按 name。
	if all[0].Kind != "alpha" || all[2].Kind != "beta" {
		t.Fatalf("排序应按 kind 聚合: %+v", []string{all[0].Kind, all[1].Kind, all[2].Kind})
	}
	byID := map[string]Entry{}
	for _, entry := range all {
		byID[entry.ID] = entry
	}
	for _, want := range []string{"a1", "a2", "b1"} {
		if _, ok := byID[want]; !ok {
			t.Fatalf("缺少条目 %s: %+v", want, all)
		}
	}
	// 适配器漏填名称时要回填 ID, 否则前端与外部工具无从显示。
	if byID["a2"].Name != "a2" {
		t.Fatalf("名称为空的条目应回填 ID, got %+v", byID["a2"])
	}
	// 适配器漏填 kind 时由注册表回填。
	if byID["a2"].Kind != "alpha" {
		t.Fatalf("kind 应回填为注册时的 kind, got %+v", byID["a2"])
	}

	only, _, err := Entries(context.Background(), "beta")
	if err != nil {
		t.Fatalf("entries(beta): %v", err)
	}
	if len(only) != 1 || only[0].Kind != "beta" || !only[0].Enabled {
		t.Fatalf("beta entries = %+v", only)
	}

	if _, _, err := Entries(context.Background(), "nope"); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("unknown kind err = %v, want ErrUnknownKind", err)
	}
}

// TestEntriesKeepsOtherKindsWhenOneFails 一个后端坏了不该让整张表消失：错误进 KindErrors, 其余照回。
func TestEntriesKeepsOtherKindsWhenOneFails(t *testing.T) {
	withoutBuiltin(t)
	withFake(t, fakeAdapter{kind: "good", entries: []Entry{{ID: "ok"}}})
	withFake(t, fakeAdapter{kind: "bad", err: errors.New("上游挂了")})

	entries, failures, err := Entries(context.Background(), "")
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != "good" {
		t.Fatalf("entries = %+v, want only the good kind", entries)
	}
	if len(failures) != 1 || failures[0].Kind != "bad" {
		t.Fatalf("failures = %+v", failures)
	}

	// 指定坏 kind 时条目为空但错误照样透出（而不是整请求 500）。
	entries, failures, err = Entries(context.Background(), "bad")
	if err != nil {
		t.Fatalf("entries(bad): %v", err)
	}
	if len(entries) != 0 || len(failures) != 1 {
		t.Fatalf("bad kind = %+v / %+v", entries, failures)
	}
}

// TestStatsCountsEnabledAndHealthy 聚合计数与条目一致, 且坏后端计 0 而不是让统计失败。
func TestStatsCountsEnabledAndHealthy(t *testing.T) {
	withoutBuiltin(t)
	withFake(t, fakeAdapter{kind: "counted", entries: []Entry{
		{ID: "1", Enabled: true, Healthy: true},
		{ID: "2", Enabled: true},
		{ID: "3"},
	}})
	withFake(t, fakeAdapter{kind: "broken", err: errors.New("boom")})

	stats := Snapshot(context.Background())
	kind := stats.ByKind["counted"]
	if kind.Entries != 3 || kind.Enabled != 2 || kind.Healthy != 1 {
		t.Fatalf("counted stat = %+v", kind)
	}
	if stats.ByKind["broken"].Entries != 0 {
		t.Fatalf("坏后端应计 0, got %+v", stats.ByKind["broken"])
	}
	if stats.Total < 3 || stats.Enabled < 2 {
		t.Fatalf("total stats = %+v", stats)
	}
}

// TestBuiltinOfficialAdapterIsRegistered 官方账号池这一种后端必须开箱可用（内置适配器注册成功）。
func TestBuiltinOfficialAdapterIsRegistered(t *testing.T) {
	if !Has(officialKind) {
		t.Fatalf("内置适配器 %q 未注册", officialKind)
	}
	var info AdapterInfo
	for _, candidate := range Kinds() {
		if candidate.Kind == officialKind {
			info = candidate
			break
		}
	}
	if info.Title == "" || !info.Builtin {
		t.Fatalf("内置适配器自描述不完整: %+v", info)
	}
	has := map[Capability]bool{}
	for _, capability := range info.Capabilities {
		has[capability] = true
	}
	for _, want := range []Capability{CapList, CapGet, CapProbe, CapRefresh, CapProvision, CapSync} {
		if !has[want] {
			t.Errorf("官方账号池适配器缺能力位 %q", want)
		}
	}
	// 凭据类字段必须标成 secret: 接口层按它决定"永不回显"。
	secret := false
	for _, field := range info.Fields {
		if field.Name == "access_token" && field.Secret {
			secret = true
		}
	}
	if !secret {
		t.Error("access_token 字段必须标记 secret")
	}
}

// panicAdapter 模拟外部适配器实现里的 panic。
type panicAdapter struct{ kind string }

func (p panicAdapter) Info() AdapterInfo {
	return AdapterInfo{Kind: p.kind, Title: "会炸的适配器", Capabilities: []Capability{CapList}}
}

func (p panicAdapter) Entries(context.Context) ([]Entry, error) { panic("外部适配器炸了") }

// TestAdapterPanicBecomesWarning 适配器 panic 不能把整张号池表打没：兜成 warning, 其余照回。
func TestAdapterPanicBecomesWarning(t *testing.T) {
	withoutBuiltin(t)
	withFake(t, fakeAdapter{kind: "fine", entries: []Entry{{ID: "ok", Name: "正常"}}})
	withFake(t, panicAdapter{kind: "boom"})

	entries, failures, err := Entries(context.Background(), "")
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != "fine" {
		t.Fatalf("entries = %+v, want only the healthy kind", entries)
	}
	if len(failures) != 1 || failures[0].Kind != "boom" {
		t.Fatalf("failures = %+v, want one entry for the panicking kind", failures)
	}

	// 指定会炸的 kind: 也不 panic, 只回 warning。
	entries, failures, err = Entries(context.Background(), "boom")
	if err != nil {
		t.Fatalf("entries(boom): %v", err)
	}
	if len(entries) != 0 || len(failures) != 1 {
		t.Fatalf("boom kind = %+v / %+v", entries, failures)
	}

	// 统计同样不被带崩。
	if stats := Snapshot(context.Background()); stats.ByKind["boom"].Entries != 0 {
		t.Fatalf("统计应忽略会炸的适配器: %+v", stats.ByKind["boom"])
	}
}
