package pluginmarket

// 插件市场：把"账号余额插件"与"号池插件"变成可安装、可分享的一份东西（需求1/2/3）。
//
// # 为什么是"市场"而不是两套独立表单
//
// 这两类插件在用户眼里是同一件事："我要给 octopus 加一个站点的读账能力"、
// "我要给 octopus 加一个号池适配器"。分成两个互不相干的表单，用户得先搞懂
// 内部模块怎么分；而且分享时也没法用同一种说法（"这是一个 octopus 插件"）。
//
// 本包只做**解析与分派**，不重复实现任何能力：
//   - 账号余额插件 → 转成采集包（collector.Pack），交给既有凭据源 CRUD 落库；
//   - 号池插件     → 转成声明式规格（pool.DeclarativeSpec），交给既有适配器注册落库。
// 这样"装载门禁、加密落库、域名白名单、能力位校验"全部沿用既有实现，
// 新开的只是一层统一格式，不新开一套安全面。
//
// # 统一格式
//
// 一份插件是一个 JSON 对象，最少包含 kind 与 spec 两个字段：
//
//	{
//	  "kind": "balance" | "pool",
//	  "name": "给人看的名字（可选，缺省用插件自带标题）",
//	  "spec": { ... }          // 按 kind 不同，形状见下面两个类型
//	}
//
// kind 是**必填且只能这两个值**：缺它就无法知道 spec 该按哪种形状解析，
// 而"猜一个"会把号池规格当采集包装（或反过来），报错信息还指不到真正的问题。
//
// # 安全口径
//
//   - 解析是纯函数：不碰网络、不碰数据库、不执行插件里的任何代码；
//   - 装载门禁在既有通路里（collector 的 hosts fail-closed、pool 的域名白名单）；
//   - 凭据（secret/username/password）属于**安装时另填**，插件文件里不该带 ——
//     带了也要在解析阶段拒掉，否则"分享插件"会顺便分享别人的账号。

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PluginKind 是插件的两类。
type PluginKind string

const (
	// KindBalance 是账号余额插件：把"某站点怎么登录、怎么读余额"打包成可安装的一份。
	KindBalance PluginKind = "balance"
	// KindPool 是号池插件：声明一个只读的号池适配器（列条目、取条目）。
	KindPool PluginKind = "pool"
)

// MaxPluginBytes 是插件文件的大小上限。
//
// 1MiB 对"登录/查账端点描述"绰绰有余（真正的采集包也就几 KB），
// 而对"上传一个几 MB 的东西进来"是明确的拒绝 —— 这类入口最该防的就是
// 有人把与插件无关的大文件塞进来。
const MaxPluginBytes = 1 << 20

// Plugin 是一份插件清单的通用形状。
//
// 用 json.RawMessage 承载 spec 而不是 interface{}：前者能把"按 kind 二次解析"
// 推迟到确定 kind 之后，且解析失败时报的是 spec 内部的错，不会把整个文件
// 的错混在一起。
type Plugin struct {
	Kind PluginKind      `json:"kind"`
	Name string          `json:"name"`
	Spec json.RawMessage `json:"spec"`
}

// BalanceSpec 是账号余额插件的载荷：它就是一份采集包。
//
// 字段与 collector.Pack 对齐，但这里只取"描述站点行为"的部分；
// 账号密码属于安装时另填，不在这形状里（见 Parse 的凭据拒止）。
type BalanceSpec struct {
	Hosts       []string       `json:"hosts"`
	Units       string         `json:"units"`
	Login       map[string]any `json:"login"`
	Read        []any          `json:"read"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
}

// PoolSpec 是号池插件的载荷：声明式适配器规格。
//
// 只保留"这个适配器是什么、连哪里、有哪些只读能力"，
// Secret（调用方凭据）由安装时另填。
type PoolSpec struct {
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	BaseURL      string   `json:"base_url"`
	Capabilities []string `json:"capabilities"`
}

// Parse 校验并解析一份插件清单。
//
// 错误分三类，措辞都指向"改哪里"：
//   - 不是合法 JSON / 超限 → 文件本身的问题；
//   - kind 缺失或不是那两个值 → 该补 kind；
//   - spec 与 kind 不匹配 / 带了凭据 → spec 的问题。
//
// 刻意**不**在这里做域名白名单与能力位校验：那些在既有通路里，
// 两处都做会出现"这边放过、那边拒掉"的分叉。
func Parse(raw []byte) (Plugin, error) {
	if len(raw) == 0 {
		return Plugin{}, fmt.Errorf("插件内容为空")
	}
	if len(raw) > MaxPluginBytes {
		return Plugin{}, fmt.Errorf("插件过大：%d 字节，上限 %d（这看起来不是插件描述文件）", len(raw), MaxPluginBytes)
	}
	var plugin Plugin
	if err := json.Unmarshal(raw, &plugin); err != nil {
		return Plugin{}, fmt.Errorf("不是合法 JSON：%w", err)
	}
	switch plugin.Kind {
	case KindBalance, KindPool:
	default:
		return Plugin{}, fmt.Errorf("kind 缺失或不被支持：%q（只接受 balance / pool）", string(plugin.Kind))
	}
	if len(plugin.Spec) == 0 {
		return Plugin{}, fmt.Errorf("缺少 spec：插件必须描述它要接入什么")
	}
	// spec 至少要能解析成一个对象：给个数组或字符串同样是"形状不对"。
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(plugin.Spec, &probe); err != nil {
		return Plugin{}, fmt.Errorf("spec 必须是 JSON 对象：%w", err)
	}
	if err := rejectEmbeddedCredentials(plugin.Spec); err != nil {
		return Plugin{}, err
	}
	return plugin, nil
}

// credentialKeys 是插件文件里**不允许出现**的键。
//
// 理由：插件的用途是分享"怎么接入一个站点"，凭据属于安装者自己。
// 文件里带了凭据，分享出去就等于把别人的账号一起分享了 —— 而这种泄露
// 在"我就发给朋友看看"的场景里几乎必然发生，所以必须在解析阶段挡掉。
var credentialKeys = []string{"secret", "password", "token", "api_key", "apikey", "access_token", "refresh_token", "cookie", "session"}

// credentialValueMarkers 是**值**里出现即判定为凭据的标记。
//
// 为什么值也要查：真实站点常用 `{"headers":{"Authorization":"Bearer sk-xxx"}}`
// 这种形状带凭据，键名是 Authorization（不在上面的清单里），值却分明是令牌。
// 只查键名会漏掉这一类 —— 而它恰恰是最常见的一种。
var credentialValueMarkers = []string{"bearer ", "sk-", "sess-", "eyj" /* JWT 头 */}

// rejectEmbeddedCredentials 递归扫描 spec，发现凭据键或凭据值即拒。
//
// 递归是必要的：凭据可能嵌在 login 或 read 的某一层里，只查顶层会漏掉。
func rejectEmbeddedCredentials(spec json.RawMessage) error {
	var walk func(any, string) error
	walk = func(node any, path string) error {
		switch value := node.(type) {
		case map[string]any:
			for key, child := range value {
				if containsFold(credentialKeys, key) {
					return fmt.Errorf("%s.%s 看起来是凭据字段：插件文件不应携带账号密码，"+
						"请在安装时填写（分享插件会连带分享凭据）", path, key)
				}
				if err := walk(child, path+"."+key); err != nil {
					return err
				}
			}
		case []any:
			for i, child := range value {
				if err := walk(child, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		case string:
			// 值里带凭据标记同样拒：键名查不出来的那一类靠这里兜住。
			lower := strings.ToLower(value)
			for _, marker := range credentialValueMarkers {
				if strings.Contains(lower, marker) {
					return fmt.Errorf("%s 的值看起来是凭据（含 %q）：插件文件不应携带账号密码，"+
						"请在安装时填写（分享插件会连带分享凭据）", path, marker)
				}
			}
		}
		return nil
	}
	var tree any
	if err := json.Unmarshal(spec, &tree); err != nil {
		return fmt.Errorf("spec 解析失败：%w", err)
	}
	return walk(tree, "spec")
}

func containsFold(list []string, want string) bool {
	for _, item := range list {
		if strings.EqualFold(item, want) {
			return true
		}
	}
	return false
}

// DisplayName 返回插件的展示名：优先用清单里的 name，其次 spec 自带标题，
// 最后回落 kind —— 总得有个能念出来的名字，否则安装列表里全是空白行。
func (p Plugin) DisplayName() string {
	if name := strings.TrimSpace(p.Name); name != "" {
		return name
	}
	switch p.Kind {
	case KindBalance:
		var spec BalanceSpec
		if json.Unmarshal(p.Spec, &spec) == nil && strings.TrimSpace(spec.Title) != "" {
			return spec.Title
		}
	case KindPool:
		var spec PoolSpec
		if json.Unmarshal(p.Spec, &spec) == nil && strings.TrimSpace(spec.Title) != "" {
			return spec.Title
		}
	}
	return string(p.Kind)
}
