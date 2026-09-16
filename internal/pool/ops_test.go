package pool

import (
	"context"
	"errors"
	"testing"
)

// opsAdapter 是第二批能力的单测替身：一个适配器可以只实现其中若干项，
// 用来验证"声明了但没实现"与"压根没声明"这两种缺能力的形态。
type opsAdapter struct {
	kind    string
	caps    []Capability
	entries []Entry

	getErr    error
	probeErr  error
	refreshFn func(id string) (Entry, error)
	synced    bool
	panicOn   string // "get" / "probe" / "sync" / "toggle" 之一：对应方法直接 panic
}

func (o *opsAdapter) Info() AdapterInfo {
	caps := o.caps
	if caps == nil {
		caps = []Capability{CapList}
	}
	return AdapterInfo{Kind: o.kind, Title: "ops 替身 " + o.kind, Capabilities: caps}
}

func (o *opsAdapter) Entries(context.Context) ([]Entry, error) { return o.entries, nil }

func (o *opsAdapter) Get(_ context.Context, id string) (Entry, error) {
	if o.panicOn == "get" {
		panic("get 炸了")
	}
	if o.getErr != nil {
		return Entry{}, o.getErr
	}
	for _, entry := range o.entries {
		if entry.ID == id {
			return entry, nil
		}
	}
	return Entry{}, ErrEntryNotFound
}

func (o *opsAdapter) Probe(_ context.Context, id string) (Entry, error) {
	if o.panicOn == "probe" {
		panic("probe 炸了")
	}
	entry, err := o.Get(context.Background(), id)
	if o.probeErr != nil {
		entry.LastError = o.probeErr.Error()
		return entry, o.probeErr
	}
	return entry, err
}

func (o *opsAdapter) Refresh(_ context.Context, id string) (Entry, error) {
	if o.refreshFn != nil {
		return o.refreshFn(id)
	}
	return Entry{ID: id, Name: id, Status: "refreshed"}, nil
}

func (o *opsAdapter) Sync(context.Context) (SyncReport, error) {
	if o.panicOn == "sync" {
		panic("sync 炸了")
	}
	o.synced = true
	return SyncReport{Entries: len(o.entries)}, nil
}

func (o *opsAdapter) SetEnabled(_ context.Context, id string, enabled bool) (Entry, error) {
	if o.panicOn == "toggle" {
		panic("toggle 炸了")
	}
	for _, entry := range o.entries {
		if entry.ID == id {
			entry.Enabled = enabled
			return entry, nil
		}
	}
	return Entry{}, ErrEntryNotFound
}

// TestOpsRejectUnknownKind 未知 kind 一律 ErrUnknownKind（接口层映射 404）。
func TestOpsRejectUnknownKind(t *testing.T) {
	ctx := context.Background()
	if _, err := Get(ctx, "nope", "x"); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("Get err = %v", err)
	}
	if _, err := Probe(ctx, "nope", "x"); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("Probe err = %v", err)
	}
	if _, err := Refresh(ctx, "nope", "x"); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("Refresh err = %v", err)
	}
	if _, err := Sync(ctx, "nope"); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("Sync err = %v", err)
	}
	if _, err := SetEnabled(ctx, "nope", "x", true); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("SetEnabled err = %v", err)
	}
}

// TestOpsRequireDeclaredAndImplemented 两层校验：没声明能力位 → ErrUnsupported；
// 声明了但没实现（类型断言失败）→ 同样是 ErrUnsupported，但消息里写明 declared but not implemented。
func TestOpsRequireDeclaredAndImplemented(t *testing.T) {
	ctx := context.Background()
	// 只声明 list 的适配器：任何写操作都应被拒。
	withFake(t, &opsAdapter{kind: "readonly", entries: []Entry{{ID: "a"}}})
	for name, call := range map[string]func() error{
		"get":     func() error { _, err := Get(ctx, "readonly", "a"); return err },
		"probe":   func() error { _, err := Probe(ctx, "readonly", "a"); return err },
		"refresh": func() error { _, err := Refresh(ctx, "readonly", "a"); return err },
		"sync":    func() error { _, err := Sync(ctx, "readonly"); return err },
		"toggle":  func() error { _, err := SetEnabled(ctx, "readonly", "a", false); return err },
	} {
		if err := call(); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
		}
	}

	// 声明了能力位但不实现接口：必须报"声明了却没实现"，而不是 panic。
	declared := &declaredOnlyAdapter{kind: "liar"}
	withFake(t, declared)
	if _, err := Get(ctx, "liar", "a"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("declared-but-not-implemented: err = %v", err)
	} else if want := "declared but not implemented"; !contains(err.Error(), want) {
		t.Errorf("错误信息应说明声明了却没实现: %v", err)
	}
}

// declaredOnlyAdapter 只声明能力位、不实现任何可选接口。
type declaredOnlyAdapter struct{ kind string }

func (d *declaredOnlyAdapter) Info() AdapterInfo {
	return AdapterInfo{Kind: d.kind, Title: "只会声明", Capabilities: []Capability{CapList, CapGet, CapProbe}}
}

func (d *declaredOnlyAdapter) Entries(context.Context) ([]Entry, error) { return nil, nil }

// TestOpsHappyPath 声明且实现的能力正常工作。
func TestOpsHappyPath(t *testing.T) {
	ctx := context.Background()
	adapter := &opsAdapter{
		kind:    "full",
		caps:    []Capability{CapList, CapGet, CapProbe, CapRefresh, CapToggle, CapSync},
		entries: []Entry{{ID: "a", Name: "甲", Enabled: true}, {ID: "b", Name: "乙"}},
	}
	withFake(t, adapter)

	entry, err := Get(ctx, "full", "a")
	if err != nil || entry.Name != "甲" || entry.Kind != "full" {
		t.Fatalf("Get = %+v / %v", entry, err)
	}
	if _, err := Get(ctx, "full", "missing"); !errors.Is(err, ErrEntryNotFound) {
		t.Fatalf("Get(missing) err = %v", err)
	}

	// 探活失败也要回条目（接口层据此展示"现在是什么状态"）。
	adapter.probeErr = errors.New("上游 401")
	probed, err := Probe(ctx, "full", "a")
	if err == nil || !contains(err.Error(), "上游 401") {
		t.Fatalf("Probe err = %v", err)
	}
	if probed.ID != "a" || probed.LastError == "" {
		t.Fatalf("探活失败也应回带错误的条目: %+v", probed)
	}
	adapter.probeErr = nil
	if probed, err = Probe(ctx, "full", "a"); err != nil || probed.ID != "a" {
		t.Fatalf("Probe = %+v / %v", probed, err)
	}

	if entry, err = Refresh(ctx, "full", "b"); err != nil || entry.Status != "refreshed" {
		t.Fatalf("Refresh = %+v / %v", entry, err)
	}

	report, err := Sync(ctx, "full")
	if err != nil || report.Kind != "full" || report.Entries != 2 || !adapter.synced {
		t.Fatalf("Sync = %+v / %v", report, err)
	}

	if entry, err = SetEnabled(ctx, "full", "b", true); err != nil || !entry.Enabled {
		t.Fatalf("SetEnabled = %+v / %v", entry, err)
	}
}

// TestOpsPanicBecomesError 可选能力的实现 panic 也不能打死请求：兜成错误。
func TestOpsPanicBecomesError(t *testing.T) {
	ctx := context.Background()
	caps := []Capability{CapList, CapGet, CapProbe, CapRefresh, CapToggle, CapSync}
	for _, which := range []string{"get", "probe", "toggle", "sync"} {
		adapter := &opsAdapter{kind: "boom-" + which, caps: caps, entries: []Entry{{ID: "a"}}, panicOn: which}
		withFake(t, adapter)

		var err error
		switch which {
		case "get":
			_, err = Get(ctx, adapter.kind, "a")
		case "probe":
			_, err = Probe(ctx, adapter.kind, "a")
		case "toggle":
			_, err = SetEnabled(ctx, adapter.kind, "a", true)
		case "sync":
			_, err = Sync(ctx, adapter.kind)
		}
		if err == nil || !contains(err.Error(), "panicked") {
			t.Errorf("%s panic 应兜成错误, got %v", which, err)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
