package pooltest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/pool"
)

// pooltest 是号池适配器的**契约自检工具包**。
//
// 为什么需要它：号池扩展层的价值取决于"别人能照着接口写一个适配器"。
// 接口写对了没有、能力位声明和实现是否一致、凭据会不会从条目里漏出去——
// 这些如果只靠人工读代码，第三者接入的成本就会高到没人接。
//
// 用法（适配器作者写到自己的测试里）：
//
//	func TestMyAdapterContract(t *testing.T) {
//	    pooltest.Run(t, &MyAdapter{})
//	}
//
// 检查项的取向：**能在编译期/测试期发现的问题，不要留到运行期变成 501 或一条泄漏的条目**。
// 运行时为了"先接只读后端、后补写操作"会容忍声明了但没实现的能力位（回 501），
// 但那是给**渐进接入**留的口子，不是给"接完就忘"留的，所以这一套把它判为失败。

// Run 检查一个适配器是否满足号池适配器契约；有问题直接让测试失败（每个问题一条 t.Error）。
//
// 用 t.Error 而不是 t.Fatal：一次跑完所有检查，作者能一轮看到全部问题。
func Run(t testing.TB, adapter pool.Adapter) {
	t.Helper()
	for _, problem := range Check(context.Background(), adapter) {
		t.Error(problem)
	}
}

// Check 返回适配器的全部契约问题（空表示通过）。
//
// 独立于 testing 暴露出来，是为了让"适配器体检"能在测试之外复用
// （例如启动期自检、或者外部工具包自己的检查脚本）。
func Check(ctx context.Context, adapter pool.Adapter) []string {
	problems := []string{}
	if adapter == nil {
		return []string{"适配器是 nil"}
	}

	info := adapter.Info()
	if strings.TrimSpace(info.Kind) == "" {
		problems = append(problems, "AdapterInfo.Kind 不能为空（外部工具靠它选后端）")
	}
	problems = append(problems, checkCapabilities(info)...)
	// 声明了能力位却没实现：运行时为了渐进接入会容忍（回 501），
	// 但那是给"先接只读、后补写操作"留的口子，不是给"接完就忘"留的 —— 这里判为失败。
	for _, capability := range declaredButMissing(adapter, info) {
		problems = append(problems, fmt.Sprintf(
			"声明了能力位 %q 但没有实现对应接口（运行时会回 501；要么实现它，要么别声明）", capability))
	}
	problems = append(problems, checkFields(info)...)

	entries, err := safeEntries(ctx, adapter)
	if err != nil {
		// 列表失败本身不是契约问题（后端可能刚好不可用），但要看得到。
		problems = append(problems, fmt.Sprintf("Entries 返回错误（后端此刻不可用不算契约问题，但值得查）: %v", err))
	}
	problems = append(problems, checkEntries(info, entries)...)
	problems = append(problems, checkSecretLeak(info, entries)...)
	problems = append(problems, checkGetter(ctx, adapter, info, entries)...)
	return problems
}

// checkCapabilities 检查能力位：合法、不重复、必须含 list、声明了就要实现。
func checkCapabilities(info pool.AdapterInfo) []string {
	problems := []string{}
	seen := map[pool.Capability]bool{}
	hasList := false
	for _, capability := range info.Capabilities {
		if capability == pool.CapList {
			hasList = true
		}
		if !pool.KnownCapability(capability) {
			problems = append(problems, fmt.Sprintf("能力位 %q 不认识（合法值见 pool.AllCapabilities()）", capability))
			continue
		}
		if seen[capability] {
			problems = append(problems, fmt.Sprintf("能力位 %q 重复声明", capability))
		}
		seen[capability] = true
	}
	if !hasList {
		problems = append(problems, "必须声明 list 能力位（所有适配器都要能列出条目）")
	}
	return problems
}

// declaredButMissing 返回"声明了能力位、但没有对应实现"的能力位。
func declaredButMissing(adapter pool.Adapter, info pool.AdapterInfo) []pool.Capability {
	missing := []pool.Capability{}
	for _, capability := range info.Capabilities {
		switch capability {
		case pool.CapGet:
			if _, ok := adapter.(pool.Getter); !ok {
				missing = append(missing, capability)
			}
		case pool.CapProbe:
			if _, ok := adapter.(pool.Prober); !ok {
				missing = append(missing, capability)
			}
		case pool.CapRefresh:
			if _, ok := adapter.(pool.Refresher); !ok {
				missing = append(missing, capability)
			}
		case pool.CapToggle:
			if _, ok := adapter.(pool.Toggler); !ok {
				missing = append(missing, capability)
			}
		case pool.CapSync:
			if _, ok := adapter.(pool.Syncer); !ok {
				missing = append(missing, capability)
			}
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
	return missing
}

// checkFields 检查自描述字段：名字不能空、不能重复。
func checkFields(info pool.AdapterInfo) []string {
	problems := []string{}
	seen := map[string]bool{}
	for _, field := range info.Fields {
		if strings.TrimSpace(field.Name) == "" {
			problems = append(problems, "AdapterInfo.Fields 里有空名字的字段")
			continue
		}
		if seen[field.Name] {
			problems = append(problems, fmt.Sprintf("字段 %q 重复声明", field.Name))
		}
		seen[field.Name] = true
	}
	return problems
}

// checkEntries 检查条目身份：kind 必须回填成自己的 kind、ID 非空且不重复。
//
// ID 重复会让统一视图里两行看起来一样（外部工具按 ID 做操作会打错目标），
// 所以这一条是硬性要求。
func checkEntries(info pool.AdapterInfo, entries []pool.Entry) []string {
	problems := []string{}
	seen := map[string]bool{}
	for index, entry := range entries {
		if strings.TrimSpace(entry.ID) == "" {
			problems = append(problems, fmt.Sprintf("第 %d 条条目没有 ID", index))
			continue
		}
		if seen[entry.ID] {
			problems = append(problems, fmt.Sprintf("条目 ID 重复: %q（外部工具按 ID 操作会打错目标）", entry.ID))
		}
		seen[entry.ID] = true
		// Entries 可以直接返回条目，不走 registry 的 normalize；这里补上同一口径的检查。
		if entry.Kind != "" && entry.Kind != info.Kind {
			problems = append(problems, fmt.Sprintf("条目 %q 的 kind=%q 与适配器 kind=%q 不一致", entry.ID, entry.Kind, info.Kind))
		}
	}
	return problems
}

// checkSecretLeak 检查凭据字段有没有从 Entry 里漏出去。
//
// 做法：自描述里标了 secret 的字段名，不允许出现在 Labels/Detail 的键上
// （标了 secret 就等于对外承诺"任何接口都不回显"，这条承诺必须在条目层就守住）。
func checkSecretLeak(info pool.AdapterInfo, entries []pool.Entry) []string {
	problems := []string{}
	secrets := map[string]string{} // 小写字段名 → 原始字段名
	for _, field := range info.Fields {
		if field.Secret {
			secrets[strings.ToLower(field.Name)] = field.Name
		}
	}
	if len(secrets) == 0 {
		return problems
	}
	for _, entry := range entries {
		for _, key := range entryKeys(entry) {
			if original, hit := secrets[strings.ToLower(key)]; hit {
				problems = append(problems, fmt.Sprintf(
					"条目 %q 的 Labels/Detail 里出现了凭据字段 %q —— 标了 secret 的字段不能出现在任何响应里",
					entry.ID, original))
			}
		}
	}
	return problems
}

// entryKeys 列出条目上所有自定义信息的键（Detail 嵌套一层也算）。
func entryKeys(entry pool.Entry) []string {
	keys := make([]string, 0, len(entry.Labels)+len(entry.Detail))
	for key := range entry.Labels {
		keys = append(keys, key)
	}
	for key, value := range entry.Detail {
		keys = append(keys, key)
		if nested, ok := value.(map[string]string); ok {
			for inner := range nested {
				keys = append(keys, inner)
			}
		}
		if nested, ok := value.(map[string]any); ok {
			for inner := range nested {
				keys = append(keys, inner)
			}
		}
	}
	return keys
}

// checkGetter 检查列表与详情的一致性：Get 声明的 ID 必须取得到，且取不到的 ID 要报错。
//
// 第二条尤其重要：Get 对不存在的 ID 返回一个空条目（而不是错误）会让上层
// 把"没了"显示成"活着但什么都没有"。
func checkGetter(ctx context.Context, adapter pool.Adapter, info pool.AdapterInfo, entries []pool.Entry) []string {
	getter, ok := adapter.(pool.Getter)
	if !ok {
		return nil
	}
	problems := []string{}
	for _, entry := range entries {
		if entry.ID == "" {
			continue
		}
		got, err := getter.Get(ctx, entry.ID)
		if err != nil {
			problems = append(problems, fmt.Sprintf("Get(%q) 失败，但条目是列表里给的: %v", entry.ID, err))
			continue
		}
		if got.ID != entry.ID {
			problems = append(problems, fmt.Sprintf("Get(%q) 回的条目 ID 是 %q（列表与详情口径不一致）", entry.ID, got.ID))
		}
	}
	if len(entries) > 0 {
		missing := info.Kind + ":__pooltest_missing__"
		if _, err := getter.Get(ctx, missing); err == nil {
			problems = append(problems, fmt.Sprintf("Get(%q) 对不存在的条目没报错 —— 上层会把「没了」显示成「活着」", missing))
		}
	}
	return problems
}

// safeEntries 调 Entries 并把 panic 兜成错误（适配器炸了不该带走整个测试进程）。
func safeEntries(ctx context.Context, adapter pool.Adapter) (entries []pool.Entry, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("Entries panic: %v", recovered)
		}
	}()
	return adapter.Entries(ctx)
}
