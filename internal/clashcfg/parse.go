// Package clashcfg 解析 Clash / mihomo 配置与订阅，抽出可用的出口节点（R-proxy-001）。
//
// 为什么单独成包：解析逻辑要能被"文件导入 / 粘贴导入 / 订阅拉取"三条入口共用，且要独立于
// 数据库与 op 层（op 依赖 db，解析不该依赖任何一层），这样单测可以只喂 YAML 字符串。
//
// 口径：
//   - 只认 proxies 与 proxy-providers 两类节点来源；**不解析 rules / proxy-groups 的路由语义**——
//     导入的是"出口节点"，具体走哪个节点由 octopus 的账号绑定决定，拿 Clash 的分流规则进来只会两套规则打架。
//   - 节点字段除 name/type/server/port 外一律原样透传（放进 Params），这样上游内核能力升级时不必改我们。
//   - 同名节点按 name 去重（Clash 里重名会导致内核按名选不中），重名时后者加序号后缀并记一条 Note。
package clashcfg

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Node 是解析出来的一个出口节点。
type Node struct {
	Name   string
	Type   string
	Server string
	Port   int
	Params map[string]any // 除 name/type/server/port 之外的字段，原样保留
}

// ParamsJSON 把透传字段序列化成稳定 JSON（键排序，便于 diff 与去重）。
func (n Node) ParamsJSON() string {
	if len(n.Params) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(n.Params))
	for k := range n.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make(map[string]any, len(n.Params))
	// 用 json 的 map 序无关性即可，但为了"同一份配置两次导入得到同一字符串"，
	// 这里显式走 json.Marshal（Go 的 encoding/json 对 map 键排序输出）。
	for _, k := range keys {
		ordered[k] = n.Params[k]
	}
	raw, err := json.Marshal(ordered)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// Result 是一次解析的结果。
type Result struct {
	Nodes []Node
	// Infos 是订阅塞在 proxies 里的**信息伪节点**（"剩余流量：728.26 GB"、"套餐到期：长期有效"、
	// "✅续费网址:..." 这类）。它们形状上是合法节点（有 server/port），实际连不通，
	// 进节点池只会变成坏出口；但它们携带的流量/到期信息对用户有用，所以单独收在这里。
	Infos []string
	Notes []string
}

// infoNodeMarkers 是订阅方用来在节点列表顶部显示运营信息的关键词。
// 命中即按信息伪节点处理：不进节点池，改计入 Result.Infos。
var infoNodeMarkers = []string{
	"剩余流量", "套餐到期", "过期时间", "到期时间", "到期", "续费", "重置",
	"官网", "客服", "订阅", "流量", "群组", "电报", "telegram",
	"expire", "traffic", "reset", "website", "renew",
}

// looksLikeInfoNode 判断一个节点名是不是订阅信息而不是真节点。
// 判据刻意保守：只在名字**以这些词开头或整体包含**时命中，避免误伤叫"日本-流量专线"这类真节点名。
func looksLikeInfoNode(name string) bool {
	lowered := strings.ToLower(strings.TrimSpace(name))
	if lowered == "" {
		return false
	}
	for _, m := range infoNodeMarkers {
		m = strings.ToLower(m)
		if strings.HasPrefix(lowered, m) || strings.HasPrefix(lowered, "✅"+m) || strings.HasPrefix(lowered, "⭐"+m) {
			return true
		}
	}
	// "剩余流量：728.26 GB" 这类常带冒号+数字，前面还可能有 emoji，故再做一次"含关键词且带冒号"的判断。
	if strings.Contains(lowered, "：") || strings.Contains(lowered, ":") {
		for _, m := range infoNodeMarkers {
			if strings.Contains(lowered, strings.ToLower(m)) {
				return true
			}
		}
	}
	return false
}

// 支持的节点类型：都是"需要一个内核才能拨号"的协议，加本地 socks5/http（这两种 octopus 自己也能用，
// 但同样允许，方便把别的工具、别的机器上的出口接进来）。
var knownTypes = map[string]bool{
	"ss": true, "ssr": true, "vmess": true, "vless": true, "trojan": true,
	"hysteria": true, "hysteria2": true, "tuic": true, "anytls": true,
	"socks5": true, "http": true, "snell": true, "wireguard": true,
}

// Parse 解析一份 Clash/mihomo YAML，抽出其中的 proxies。
//
// 宽松优先：任何"形状不完整"的节点（缺 server/port、类型不支持）都跳过并记 Note，
// 而不是整份配置失败——订阅里常常混着内核私有字段和注释节点。
func Parse(raw []byte) (Result, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return Result{}, fmt.Errorf("解析 Clash 配置失败：%w", err)
	}
	items, ok := doc["proxies"]
	if !ok {
		return Result{}, fmt.Errorf("这份配置里没有 proxies 列表（可能是只含 proxy-providers 的配置，请改用订阅地址或节点清单）")
	}
	list, ok := items.([]any)
	if !ok {
		return Result{}, fmt.Errorf("proxies 不是列表")
	}

	result := Result{}
	seen := map[string]int{}
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		node, err := nodeFromMap(m)
		if err != nil {
			result.Notes = append(result.Notes, fmt.Sprintf("第 %d 个节点跳过：%v", i+1, err))
			continue
		}
		if looksLikeInfoNode(node.Name) {
			result.Infos = append(result.Infos, node.Name)
			continue
		}
		if n, dup := seen[node.Name]; dup {
			node.Name = node.Name + "-" + strconv.Itoa(n+1)
			result.Notes = append(result.Notes, fmt.Sprintf("节点名重复：原名 %q 已存在，本条改名 %q", strings.TrimSuffix(node.Name, "-"+strconv.Itoa(n+1)), node.Name))
		}
		seen[node.Name]++
		result.Nodes = append(result.Nodes, node)
	}
	if len(result.Infos) > 0 {
		result.Notes = append(result.Notes, fmt.Sprintf(
			"识别到 %d 条订阅信息（不是真节点，未进节点池）：%s",
			len(result.Infos), strings.Join(result.Infos, "；")))
	}
	if len(result.Nodes) == 0 {
		result.Notes = append(result.Notes, "没有解析到任何可用节点")
	}
	return result, nil
}

func nodeFromMap(m map[string]any) (Node, error) {
	node := Node{Params: map[string]any{}}
	for k, v := range m {
		switch k {
		case "name":
			node.Name = strings.TrimSpace(toString(v))
		case "type":
			node.Type = strings.ToLower(strings.TrimSpace(toString(v)))
		case "server":
			node.Server = strings.TrimSpace(toString(v))
		case "port":
			node.Port = toInt(v)
		default:
			node.Params[k] = v
		}
	}
	if node.Name == "" {
		return Node{}, fmt.Errorf("缺少 name")
	}
	if node.Server == "" {
		return Node{}, fmt.Errorf("缺少 server")
	}
	if node.Port <= 0 || node.Port > 65535 {
		return Node{}, fmt.Errorf("port 非法或缺失: %d", node.Port)
	}
	if !knownTypes[node.Type] {
		return Node{}, fmt.Errorf("暂不支持的节点类型 %q", node.Type)
	}
	return node, nil
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int:
		return strconv.Itoa(t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func toInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n
		}
	}
	return 0
}
