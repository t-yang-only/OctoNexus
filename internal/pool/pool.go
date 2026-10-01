// Package pool 是号池的扩展层：把「谁在池子里、池子里的东西能干什么」从具体实现里抽出来。
//
// 背景（R-pool-ext-001）：号池此前只有一种形态——官方账号经 T-pool-002 物化成渠道凭据。
// 用户要求「号池加入高度扩展性，基于已有的开发大量 API 接口，后面好做各种反代工具包的扩展」，
// 于是把号池后端抽象成 Adapter（适配器）：
//
//   - 适配器自己声明能力（Capability）与条目字段，外部工具包/前端靠它发现"这个池子能做什么"，
//     不需要读 octopus 的源码，也不需要为每个新后端改前端；
//   - 统一视图（Entry）与后端无关：官方账号、中转站账号、自定义反代工具包都投影成同一张表；
//   - 注册表允许外部在启动时追加适配器，新增一种号池后端不必改选路、不必改协议转换。
//
// 本包的硬约束（安全口径，与既有号池一致）：**绝不回显凭据**。Entry 只带元数据与状态，
// 密文与明文都不出这一个包，也不出接口。
package pool

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Capability 是适配器对外声明的能力位。
//
// 为什么要显式声明而不是"都假设有"：外部工具包与前端按能力位决定展示哪些操作，
// 没实现的能力宁可说没有，也不要在调用时才发现后端报错。
type Capability string

const (
	CapList      Capability = "list"      // 能列出条目（所有适配器都应声明）。
	CapGet       Capability = "get"       // 能按 ID 取单条。
	CapProbe     Capability = "probe"     // 能主动探活/读配额。
	CapRefresh   Capability = "refresh"   // 能刷新凭据（换取新 token）。
	CapToggle    Capability = "toggle"    // 能人工启用/停用条目。
	CapProvision Capability = "provision" // 能新建条目（授权链接/扫码/登录）。
	CapRevoke    Capability = "revoke"    // 能删除/撤销条目。
	CapSync      Capability = "sync"      // 能物化到转发层（把条目落成渠道凭据）。
)

// allCapabilities 是能力位的合法集合，用于校验适配器自描述。
var allCapabilities = map[Capability]bool{
	CapList: true, CapGet: true, CapProbe: true, CapRefresh: true,
	CapToggle: true, CapProvision: true, CapRevoke: true, CapSync: true,
}

// KnownCapability 判断能力位是否是本层认识的值（适配器契约自检 / 外部工具校验用）。
func KnownCapability(capability Capability) bool { return allCapabilities[capability] }

// AllCapabilities 返回全部合法能力位（按固定顺序，供文档与界面枚举）。
func AllCapabilities() []Capability {
	return []Capability{CapList, CapGet, CapProbe, CapRefresh, CapToggle, CapProvision, CapRevoke, CapSync}
}

// FieldSpec 描述条目上的一个字段，供界面与外部工具渲染，不必硬编码字段名。
type FieldSpec struct {
	Name     string `json:"name"`
	Type     string `json:"type"`     // string / number / bool / time
	Label    string `json:"label"`    // 人类可读标签（中文，与面板一致）
	Required bool   `json:"required"` // 新建条目时是否必填
	Secret   bool   `json:"secret"`   // 是否凭据类字段；true 表示任何接口都不回显
}

// AdapterInfo 是适配器的自描述：外部工具靠它发现「有哪些号池后端、各自能做什么」。
type AdapterInfo struct {
	Kind         string       `json:"kind"`
	Title        string       `json:"title"`
	Capabilities []Capability `json:"capabilities"`
	Fields       []FieldSpec  `json:"fields,omitempty"`
	Builtin      bool         `json:"builtin"`
	Since        string       `json:"since,omitempty"` // 引入版本/阶段标记，便于外部工具做兼容判断
	// Operations 是该后端支持的具体 HTTP 调用（由能力位推导，见 query.go）。
	// 外部工具照这份清单渲染按钮即可，不必读 octopus 源码、也不必硬编码路径。
	Operations []Operation `json:"operations,omitempty"`
}

// Entry 是统一号池视图的一行，与具体后端无关。
//
// Detail 放后端特有的补充信息（如账号→凭据的映射状态）；凭据内容永远不在这里。
type Entry struct {
	Kind      string            `json:"kind"`
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Provider  string            `json:"provider,omitempty"`
	Status    string            `json:"status"`
	Enabled   bool              `json:"enabled"`
	Healthy   bool              `json:"healthy"`
	PlanTier  string            `json:"plan_tier,omitempty"`
	ExpiresAt *time.Time        `json:"expires_at,omitempty"`
	LastError string            `json:"last_error,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Detail    map[string]any    `json:"detail,omitempty"`
}

// Adapter 是一种号池后端的统一接口。
//
// 第一版只要求只读清单（Entries）：写操作（provision/refresh/probe/toggle/sync）由适配器按能力位
// 逐个实现，接口层按能力位拦截——这样"先接只读后端、后补写操作"不必改动已接好的后端。
type Adapter interface {
	Info() AdapterInfo
	Entries(ctx context.Context) ([]Entry, error)
}

// KindError 记录某个适配器在统一视图里出的错：一个后端坏了不该让整张表消失。
type KindError struct {
	Kind  string `json:"kind"`
	Error string `json:"error"`
}

// Stats 是号池的聚合计数，供面板与外部工具一眼看规模。
type Stats struct {
	Total   int                 `json:"total"`
	Enabled int                 `json:"enabled"`
	Healthy int                 `json:"healthy"`
	ByKind  map[string]KindStat `json:"by_kind"`
}

// KindStat 是单个适配器的计数。
type KindStat struct {
	Entries int `json:"entries"`
	Enabled int `json:"enabled"`
	Healthy int `json:"healthy"`
}

var (
	// ErrUnknownKind 表示请求的适配器没有注册。
	ErrUnknownKind = errors.New("unknown pool kind")
	// ErrDuplicateKind 表示同一 kind 被注册两次。
	ErrDuplicateKind = errors.New("duplicate pool kind")
	// ErrInvalidAdapter 表示适配器自描述不合法（空 kind、未声明 list 能力等）。
	ErrInvalidAdapter = errors.New("invalid pool adapter")
)

// registry 是包级注册表：内置适配器在 init 里注册，外部可在启动早期追加。
// 读多写少（注册只发生在启动期），用 RWMutex 保护即可。
var registry = struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
	order    []string
	builtin  map[string]bool // 内置适配器：运行时注册不可替换、不可移除。
	runtime  map[string]bool // 运行时（声明式）注册的适配器：可替换、可移除。
}{adapters: make(map[string]Adapter), builtin: make(map[string]bool), runtime: make(map[string]bool)}

// Register 注册一个号池适配器。
//
// 语义刻意选"报错"而不是"覆盖"：两个适配器抢同一个 kind 时沉默覆盖会让接口背后到底是哪个实现
// 变得不可知，宁可在启动期就炸出来。
func Register(adapter Adapter) error {
	if adapter == nil {
		return fmt.Errorf("%w: nil adapter", ErrInvalidAdapter)
	}
	info := adapter.Info()
	if info.Kind == "" {
		return fmt.Errorf("%w: empty kind", ErrInvalidAdapter)
	}
	if info.Title == "" {
		return fmt.Errorf("%w: kind %q has no title", ErrInvalidAdapter, info.Kind)
	}
	seen := false
	for _, capability := range info.Capabilities {
		if !allCapabilities[capability] {
			return fmt.Errorf("%w: kind %q declares unknown capability %q", ErrInvalidAdapter, info.Kind, capability)
		}
		if capability == CapList {
			seen = true
		}
	}
	if !seen {
		return fmt.Errorf("%w: kind %q must declare the %q capability", ErrInvalidAdapter, info.Kind, CapList)
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.adapters[info.Kind]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateKind, info.Kind)
	}
	registry.adapters[info.Kind] = adapter
	registry.order = append(registry.order, info.Kind)
	if info.Builtin {
		registry.builtin[info.Kind] = true
	}
	return nil
}

// validateAdapterInfo 是 Register 与 RegisterRuntime 共用的自描述校验。
func validateAdapterInfo(info AdapterInfo) error {
	if info.Kind == "" {
		return fmt.Errorf("%w: empty kind", ErrInvalidAdapter)
	}
	if info.Title == "" {
		return fmt.Errorf("%w: kind %q has no title", ErrInvalidAdapter, info.Kind)
	}
	seen := false
	for _, capability := range info.Capabilities {
		if !allCapabilities[capability] {
			return fmt.Errorf("%w: kind %q declares unknown capability %q", ErrInvalidAdapter, info.Kind, capability)
		}
		if capability == CapList {
			seen = true
		}
	}
	if !seen {
		return fmt.Errorf("%w: kind %q must declare the %q capability", ErrInvalidAdapter, info.Kind, CapList)
	}
	return nil
}

// RegisterRuntime 注册或替换一个**运行时**适配器（声明式注册走这里）。
//
// 与 Register 的两点差异，都是刻意的：
//   - 同名可替换：声明式适配器的 spec 是用户可改的，改完要能当场生效，不必重启；
//     但内置 kind 一律不可占用、也不可替换——否则一份 JSON 就能顶掉官方账号适配器。
//   - 单独记在 runtime 集合里：这样 UnregisterRuntime 只敢删运行时注册的那些。
func RegisterRuntime(adapter Adapter) error {
	if adapter == nil {
		return fmt.Errorf("%w: nil adapter", ErrInvalidAdapter)
	}
	info := adapter.Info()
	if err := validateAdapterInfo(info); err != nil {
		return err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.builtin[info.Kind] {
		return fmt.Errorf("%w: kind %q 是内置适配器，不能被运行时注册覆盖", ErrDuplicateKind, info.Kind)
	}
	if _, exists := registry.adapters[info.Kind]; exists && !registry.runtime[info.Kind] {
		return fmt.Errorf("%w: kind %q 已被非运行时适配器占用", ErrDuplicateKind, info.Kind)
	}
	if _, exists := registry.adapters[info.Kind]; !exists {
		registry.order = append(registry.order, info.Kind)
	}
	registry.adapters[info.Kind] = adapter
	registry.runtime[info.Kind] = true
	return nil
}

// UnregisterRuntime 移除一个运行时注册的适配器；内置适配器与启动期注册的适配器都不可移除。
func UnregisterRuntime(kind string) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.builtin[kind] {
		return fmt.Errorf("%w: kind %q 是内置适配器，不能移除", ErrUnknownKind, kind)
	}
	if !registry.runtime[kind] {
		return fmt.Errorf("%w: kind %q 不是运行时注册的适配器", ErrUnknownKind, kind)
	}
	delete(registry.adapters, kind)
	delete(registry.runtime, kind)
	for i, existing := range registry.order {
		if existing == kind {
			registry.order = append(registry.order[:i], registry.order[i+1:]...)
			break
		}
	}
	return nil
}

// BuiltinKind 报告某个 kind 是否是内置适配器（供接口层判断"这个能不能删/能不能覆盖"）。
func BuiltinKind(kind string) bool {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return registry.builtin[kind]
}

// unregister 仅供测试使用：注册表是包级状态，单测之间要能互相隔离。
func unregister(kind string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	delete(registry.adapters, kind)
	for i, existing := range registry.order {
		if existing == kind {
			registry.order = append(registry.order[:i], registry.order[i+1:]...)
			break
		}
	}
}

// Kinds 返回已注册适配器的自描述，按注册顺序稳定输出。
func Kinds() []AdapterInfo {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	infos := make([]AdapterInfo, 0, len(registry.order))
	for _, kind := range registry.order {
		info := registry.adapters[kind].Info()
		// 操作清单在这里统一回填：适配器只声明能力位，路由清单由能力位推导，二者不可能漂移。
		if info.Operations == nil {
			info.Operations = operationsOf(info.Capabilities)
		}
		infos = append(infos, info)
	}
	return infos
}

// Has 判断某个 kind 是否已注册。
func Has(kind string) bool {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	_, ok := registry.adapters[kind]
	return ok
}

// Lookup 按 kind 取回适配器本体。
//
// 与 Has 分开是因为有些调用方要的不是"在不在"，而是那个适配器本身：
// 契约自检工具包（internal/pool/pooltest）要拿它跑七类检查，而自检必须从外部测试包调用
// （pooltest 依赖 pool，内部测试包再引它就成了循环），所以这里需要一个对外可见的取用入口。
func Lookup(kind string) (Adapter, bool) {
	return lookup(kind)
}

// Entries 取统一视图：kind 为空表示所有已注册适配器。
//
// 单个适配器失败时把错误收集到 KindErrors 里返回，其余适配器的条目照常给出——
// 号池是排查现场的地方，一个坏后端把整张表变成 500 只会让人更难定位。
func Entries(ctx context.Context, kind string) ([]Entry, []KindError, error) {
	if kind != "" {
		adapter, ok := lookup(kind)
		if !ok {
			return nil, nil, fmt.Errorf("%w: %s", ErrUnknownKind, kind)
		}
		entries, err := safeEntries(adapter, ctx)
		if err != nil {
			return nil, []KindError{{Kind: kind, Error: err.Error()}}, nil
		}
		return normalize(kind, entries), nil, nil
	}

	kinds := Kinds()
	all := make([]Entry, 0, 16)
	failures := make([]KindError, 0)
	for _, info := range kinds {
		adapter, ok := lookup(info.Kind)
		if !ok {
			continue
		}
		entries, err := safeEntries(adapter, ctx)
		if err != nil {
			failures = append(failures, KindError{Kind: info.Kind, Error: err.Error()})
			continue
		}
		all = append(all, normalize(info.Kind, entries)...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Kind != all[j].Kind {
			return all[i].Kind < all[j].Kind
		}
		if all[i].Provider != all[j].Provider {
			return all[i].Provider < all[j].Provider
		}
		return all[i].Name < all[j].Name
	})
	return all, failures, nil
}

// Snapshot 汇总计数；取不到条目的适配器按 0 计（同样不因为一个后端出错而整体失败）。
// 名字不用 Stats 是因为包内已有同名类型（Go 里类型与方法不能同名）。
func Snapshot(ctx context.Context) Stats {
	stats := Stats{ByKind: make(map[string]KindStat)}
	for _, info := range Kinds() {
		adapter, ok := lookup(info.Kind)
		if !ok {
			continue
		}
		entries, err := safeEntries(adapter, ctx)
		if err != nil {
			continue
		}
		kindStat := KindStat{}
		for _, entry := range normalize(info.Kind, entries) {
			kindStat.Entries++
			if entry.Enabled {
				kindStat.Enabled++
			}
			if entry.Healthy {
				kindStat.Healthy++
			}
		}
		stats.ByKind[info.Kind] = kindStat
		stats.Total += kindStat.Entries
		stats.Enabled += kindStat.Enabled
		stats.Healthy += kindStat.Healthy
	}
	return stats
}

func lookup(kind string) (Adapter, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	adapter, ok := registry.adapters[kind]
	return adapter, ok
}

// normalize 兜底适配器漏填的字段：kind 由注册表回填，ID/Name 为空会让前端与工具无从下手。
func normalize(kind string, entries []Entry) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind == "" {
			entry.Kind = kind
		}
		if entry.Name == "" {
			entry.Name = entry.ID
		}
		out = append(out, entry)
	}
	return out
}

// safeEntries 调用适配器并把 panic 兜成普通错误。
//
// 为什么要兜：适配器是**扩展点**，将来会由外部反代工具包实现。别人写的代码在枚举号池时
// panic 一次，如果直接冒泡，整个 /pool/entries 请求就 500——号池恰恰是最需要能打开看一眼的地方。
// 兜住之后表现是"这一种后端带一条 warning，其余后端照常显示"。
func safeEntries(adapter Adapter, ctx context.Context) (entries []Entry, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			entries = nil
			err = fmt.Errorf("adapter %q panicked: %v", adapter.Info().Kind, recovered)
		}
	}()
	return adapter.Entries(ctx)
}
