// Package poolstore 是号池声明式适配器的装配与持久化层（R-pool-ext-001 第三批）。
//
// 为什么单独一层：`internal/pool` 依赖 `internal/op`（内置官方账号适配器投影 op 的数据），
// 所以 op 不能反向依赖 pool；而"读设置 → 解密 → 校验 → 注册适配器"这段编排两边都放不下。
// 这一层只做编排，不含 HTTP 与界面逻辑，因此可以单测，也能被启动流程与接口层共用。
package poolstore

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/pool"
)

// 存储口径（刻意选轻的）：
//   - **不进新表**：规格数量是个位数，直接存进设置表一份 JSON；新增表要迁移、要备份口径、要面板，
//     而这一层是"接第三方工具包"的扩展点，落库形态越轻越好。
//   - **整份加密**：spec 里带 token/密码，明文落库等于把凭据抄一遍（复用 op 的 AES-256-GCM）。
//   - **接口只回脱敏副本**：Redact 是唯一的脱敏实现点，任何出口都得从它过。
var mu sync.Mutex // 规格写入是低频操作，串行化即可，也避免并发注册同名 kind 打架。

// specs 读出全部已注册的声明式规格（含凭据，只在进程内流转）。
func specs() ([]pool.DeclarativeSpec, error) {
	encoded, err := op.SettingGetString(model.SettingKeyPoolDeclarativeAdapters)
	if err != nil || strings.TrimSpace(encoded) == "" {
		return nil, nil
	}
	plain, err := op.PoolSecretOpen(encoded)
	if err != nil {
		return nil, err
	}
	var parsed []pool.DeclarativeSpec
	if err := json.Unmarshal(plain, &parsed); err != nil {
		return nil, fmt.Errorf("pool declarative specs: %w", err)
	}
	return parsed, nil
}

// save 写回规格列表（整份加密）。
func save(list []pool.DeclarativeSpec) error {
	if len(list) == 0 {
		return op.SettingSetString(model.SettingKeyPoolDeclarativeAdapters, "")
	}
	sorted := append([]pool.DeclarativeSpec(nil), list...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Kind < sorted[j].Kind })
	plain, err := json.Marshal(sorted)
	if err != nil {
		return err
	}
	encoded, err := op.PoolSecretSeal(plain)
	if err != nil {
		return err
	}
	return op.SettingSetString(model.SettingKeyPoolDeclarativeAdapters, encoded)
}

// Redact 是唯一的脱敏实现点：任何出口都从这里过，凭据一律清空。
func Redact(spec pool.DeclarativeSpec) pool.DeclarativeSpec {
	spec.Secret = pool.DeclarativeSecret{}
	return spec
}

// List 返回已注册的声明式适配器（已脱敏）。
func List() ([]pool.DeclarativeSpec, error) {
	stored, err := specs()
	if err != nil {
		return nil, err
	}
	redacted := make([]pool.DeclarativeSpec, 0, len(stored))
	for _, spec := range stored {
		redacted = append(redacted, Redact(spec))
	}
	return redacted, nil
}

// Register 注册（或按 kind 替换）一个声明式适配器，并落库。
//
// 顺序刻意是"先构造校验 → 再落库 → 最后注册运行时"：校验不过就不该留下半份配置；
// 注册失败（例如内置 kind 被占）要把刚落库的那份回滚，避免重启后又冒出来。
func Register(spec pool.DeclarativeSpec) (pool.DeclarativeSpec, error) {
	mu.Lock()
	defer mu.Unlock()

	adapter, err := pool.NewDeclarativeAdapter(spec) // 内含白名单/能力位/URL 校验。
	if err != nil {
		return pool.DeclarativeSpec{}, err
	}
	if pool.BuiltinKind(spec.Kind) {
		return pool.DeclarativeSpec{}, fmt.Errorf("%w: %q 是内置适配器", pool.ErrDuplicateKind, spec.Kind)
	}
	stored, err := specs()
	if err != nil {
		return pool.DeclarativeSpec{}, err
	}
	replaced := false
	for i := range stored {
		if stored[i].Kind == spec.Kind {
			stored[i] = spec
			replaced = true
			break
		}
	}
	if !replaced {
		stored = append(stored, spec)
	}
	if err := save(stored); err != nil {
		return pool.DeclarativeSpec{}, err
	}
	if err := pool.RegisterRuntime(adapter); err != nil {
		if rollbackErr := save(drop(stored, spec.Kind)); rollbackErr != nil {
			return pool.DeclarativeSpec{}, fmt.Errorf("%w（回滚失败: %v）", err, rollbackErr)
		}
		return pool.DeclarativeSpec{}, err
	}
	return Redact(spec), nil
}

// Remove 移除一个声明式适配器（内置适配器不可移除）。
func Remove(kind string) error {
	mu.Lock()
	defer mu.Unlock()

	kind = strings.TrimSpace(kind)
	if kind == "" {
		return fmt.Errorf("%w: 空 kind", pool.ErrUnknownKind)
	}
	stored, err := specs()
	if err != nil {
		return err
	}
	if err := pool.UnregisterRuntime(kind); err != nil {
		return err
	}
	return save(drop(stored, kind))
}

// drop 返回去掉某 kind 的新列表（不修改入参）。
func drop(list []pool.DeclarativeSpec, kind string) []pool.DeclarativeSpec {
	kept := make([]pool.DeclarativeSpec, 0, len(list))
	for _, spec := range list {
		if spec.Kind != kind {
			kept = append(kept, spec)
		}
	}
	return kept
}

// LoadAll 在启动时把已存的声明式适配器装回注册表，并同步白名单守卫。
//
// 单个 spec 失效（白名单被收紧、目标站点改了路径）不影响其它 spec，也不阻塞启动：
// 返回成功装载的个数与逐条错误，调用方记日志即可。
func LoadAll() (int, []error) {
	mu.Lock()
	defer mu.Unlock()

	ApplyGuards()
	stored, err := specs()
	if err != nil {
		return 0, []error{err}
	}
	loaded := 0
	failures := make([]error, 0)
	for _, spec := range stored {
		adapter, err := pool.NewDeclarativeAdapter(spec)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", spec.Kind, err))
			continue
		}
		if err := pool.RegisterRuntime(adapter); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", spec.Kind, err))
			continue
		}
		loaded++
	}
	return loaded, failures
}

// ApplyGuards 把白名单设置同步进 pool 层守卫（启动与设置变更时调用）。
func ApplyGuards() {
	raw, err := op.SettingGetString(model.SettingKeyPoolDeclarativeHosts)
	if err != nil {
		raw = ""
	}
	pool.SetDeclarativeGuards(pool.DeclarativeGuards{AllowedHosts: pool.ParseDeclarativeHosts(raw)})
}

// Hosts 返回当前白名单原文（供接口回显"现在允许哪些域名"）。
func Hosts() string {
	raw, err := op.SettingGetString(model.SettingKeyPoolDeclarativeHosts)
	if err != nil {
		return ""
	}
	return raw
}
