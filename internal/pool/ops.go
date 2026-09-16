package pool

import (
	"context"
	"fmt"
)

// ============================ 可选能力接口（第二批） ============================
//
// 设计取向：能力位（Info().Capabilities）是**对外声明**，这些接口是**对内契约**，两者必须同时满足才放行。
//
// 为什么两层都要：只查能力位会让"声明了但没实现"变成运行期 panic；只查接口会让外部工具在调用前
// 无法知道某个后端到底支不支持这项操作（于是它只能试错）。声明 + 实现都过，才回答"支持"。
type (
	// Getter 支持按 ID 取单条。
	Getter interface {
		Get(ctx context.Context, id string) (Entry, error)
	}
	// Prober 支持主动探活/读配额（成功与否都以 Entry 形式回一份当前状态）。
	Prober interface {
		Probe(ctx context.Context, id string) (Entry, error)
	}
	// Refresher 支持刷新单条条目的凭据。
	Refresher interface {
		Refresh(ctx context.Context, id string) (Entry, error)
	}
	// Syncer 支持把该后端的条目物化到转发层（kind 级）。
	Syncer interface {
		Sync(ctx context.Context) (SyncReport, error)
	}
	// Toggler 支持人工启停单条。
	Toggler interface {
		SetEnabled(ctx context.Context, id string, enabled bool) (Entry, error)
	}
)

// SyncReport 是同步的通用结论，与后端无关。
type SyncReport struct {
	Kind    string   `json:"kind"`
	Entries int      `json:"entries"` // 同步后该后端可参与转发的条目数
	Notes   []string `json:"notes,omitempty"`
}

// 包级操作错误：接口层按它们映射 HTTP 状态，不靠字符串匹配。
var (
	// ErrUnsupported 表示该后端没声明/没实现这项能力。
	ErrUnsupported = fmt.Errorf("capability not supported by this pool kind")
	// ErrEntryNotFound 表示条目不存在。
	ErrEntryNotFound = fmt.Errorf("pool entry not found")
	// ErrConflict 表示条目存在、但当前状态不允许这个操作（例如账号还没有物化出凭据，无从启停）：
	// 与"不存在"分开，外部工具才不会把"等它同步完再来"误判成"这条是错的"。
	ErrConflict = fmt.Errorf("pool entry state does not allow this operation")
)

// Get 取单条：先查能力位，再查实现。
func Get(ctx context.Context, kind, id string) (Entry, error) {
	adapter, ok := lookup(kind)
	if !ok {
		return Entry{}, fmt.Errorf("%w: %s", ErrUnknownKind, kind)
	}
	if !declares(adapter, CapGet) {
		return Entry{}, fmt.Errorf("%w: %s", ErrUnsupported, CapGet)
	}
	getter, ok := adapter.(Getter)
	if !ok {
		return Entry{}, fmt.Errorf("%w: %s (declared but not implemented)", ErrUnsupported, CapGet)
	}
	entry, err := safeCall("get", func() (Entry, error) { return getter.Get(ctx, id) })
	if err != nil {
		return Entry{}, err
	}
	return normalizeEntry(kind, entry), nil
}

// Probe 探活/读配额。
//
// 语义：**失败也回条目**——探活的价值就在于告诉你"这条现在是什么状态"，
// 因此返回的 Entry 可能同时带着错误返回（调用方按 error 判断结论，按 Entry 展示状态）。
func Probe(ctx context.Context, kind, id string) (Entry, error) {
	adapter, ok := lookup(kind)
	if !ok {
		return Entry{}, fmt.Errorf("%w: %s", ErrUnknownKind, kind)
	}
	if !declares(adapter, CapProbe) {
		return Entry{}, fmt.Errorf("%w: %s", ErrUnsupported, CapProbe)
	}
	prober, ok := adapter.(Prober)
	if !ok {
		return Entry{}, fmt.Errorf("%w: %s (declared but not implemented)", ErrUnsupported, CapProbe)
	}
	return safeCall("probe", func() (Entry, error) { return prober.Probe(ctx, id) })
}

// Refresh 刷新单条凭据。
func Refresh(ctx context.Context, kind, id string) (Entry, error) {
	adapter, ok := lookup(kind)
	if !ok {
		return Entry{}, fmt.Errorf("%w: %s", ErrUnknownKind, kind)
	}
	if !declares(adapter, CapRefresh) {
		return Entry{}, fmt.Errorf("%w: %s", ErrUnsupported, CapRefresh)
	}
	refresher, ok := adapter.(Refresher)
	if !ok {
		return Entry{}, fmt.Errorf("%w: %s (declared but not implemented)", ErrUnsupported, CapRefresh)
	}
	return safeCall("refresh", func() (Entry, error) { return refresher.Refresh(ctx, id) })
}

// Sync 把该后端的条目物化到转发层。
func Sync(ctx context.Context, kind string) (SyncReport, error) {
	adapter, ok := lookup(kind)
	if !ok {
		return SyncReport{}, fmt.Errorf("%w: %s", ErrUnknownKind, kind)
	}
	if !declares(adapter, CapSync) {
		return SyncReport{}, fmt.Errorf("%w: %s", ErrUnsupported, CapSync)
	}
	syncer, ok := adapter.(Syncer)
	if !ok {
		return SyncReport{}, fmt.Errorf("%w: %s (declared but not implemented)", ErrUnsupported, CapSync)
	}
	report, err := safeSync(syncer, ctx)
	if err != nil {
		return SyncReport{}, err
	}
	if report.Kind == "" {
		report.Kind = kind
	}
	return report, nil
}

// SetEnabled 人工启停单条。
func SetEnabled(ctx context.Context, kind, id string, enabled bool) (Entry, error) {
	adapter, ok := lookup(kind)
	if !ok {
		return Entry{}, fmt.Errorf("%w: %s", ErrUnknownKind, kind)
	}
	if !declares(adapter, CapToggle) {
		return Entry{}, fmt.Errorf("%w: %s", ErrUnsupported, CapToggle)
	}
	toggler, ok := adapter.(Toggler)
	if !ok {
		return Entry{}, fmt.Errorf("%w: %s (declared but not implemented)", ErrUnsupported, CapToggle)
	}
	entry, err := safeCall("toggle", func() (Entry, error) { return toggler.SetEnabled(ctx, id, enabled) })
	if err != nil {
		return Entry{}, err
	}
	return normalizeEntry(kind, entry), nil
}

func declares(adapter Adapter, capability Capability) bool {
	for _, declared := range adapter.Info().Capabilities {
		if declared == capability {
			return true
		}
	}
	return false
}

// safeCall 把适配器实现的 panic 兜成普通错误（与 safeEntries 同一考虑：适配器是别人写的扩展点）。
func safeCall(op string, call func() (Entry, error)) (entry Entry, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			entry, err = Entry{}, fmt.Errorf("adapter %s panicked: %v", op, recovered)
		}
	}()
	return call()
}

func safeSync(syncer Syncer, ctx context.Context) (report SyncReport, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			report, err = SyncReport{}, fmt.Errorf("adapter sync panicked: %v", recovered)
		}
	}()
	return syncer.Sync(ctx)
}

// normalizeEntry 是 normalize 的单条版本。
func normalizeEntry(kind string, entry Entry) Entry {
	if entry.Kind == "" {
		entry.Kind = kind
	}
	if entry.Name == "" {
		entry.Name = entry.ID
	}
	return entry
}
