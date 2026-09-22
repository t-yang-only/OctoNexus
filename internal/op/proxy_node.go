package op

import (
	"context"
	"errors"
	"fmt"
	"github.com/bestruirui/octopus/internal/collector"
	"github.com/charmbracelet/log"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/clashcfg"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/secret"
	"gorm.io/gorm"
)

// 代理节点池（R-proxy-001）。
//
// 用途：给"每个渠道 / 每个账号"各配一个独立出口（不同出口 IP），
// 让同一上游看到的不是一个 IP 上挂着几十个账号（防关联）。
//
// 落库纪律：节点参数（ss 的 password、vmess 的 uuid、hysteria2 的 password…）与订阅地址都是**凭据**，
// 一律经 internal/secret 加密（AAD: pool:proxy-node）；密钥不可用时**拒绝写入**，绝不退回明文。
// 出参一律不带密文与明文（model.ProxyNode.Params 是 json:"-"）。

const proxyNodeAAD = "pool:proxy-node"
const proxySubscriptionAAD = "pool:proxy-subscription"

// ErrProxyNodeInUse 表示节点仍被渠道或账号引用（handler 据此回 409 而不是 400）。
var ErrProxyNodeInUse = errors.New("proxy node still in use")

// ProxyImportResult 是一次导入的结果。
type ProxyImportResult struct {
	Imported int      `json:"imported"` // 新增节点数
	Updated  int      `json:"updated"`  // 同名节点更新（凭据/端口变了才计）
	Skipped  int      `json:"skipped"`  // 库内已有且内容完全一致
	Source   string   `json:"source"`
	Notes    []string `json:"notes,omitempty"`
	Infos    []string `json:"infos,omitempty"` // 订阅里的运营信息（剩余流量/到期），不是节点
}

// proxyNodeDB 是 op 层的句柄回落惯用法：调用方给了连接用给的，没给就用全局。
func proxyNodeDB(conn *gorm.DB) *gorm.DB {
	if conn != nil {
		return conn
	}
	return db.GetDB()
}

// sealProxyNode 把明细字段加密后写回行内（不改 Params 之外的东西）。
func sealProxyNodeParams(plain string) (string, error) {
	if strings.TrimSpace(plain) == "" {
		plain = "{}"
	}
	sealed, err := secret.SealWith(proxyNodeAAD, plain)
	if err != nil {
		return "", fmt.Errorf("节点参数加密失败（%w）：请先让凭据加密可用（设置 OCTOPUS_OFFICIAL_KEY 或让数据目录可写）", err)
	}
	return sealed, nil
}

// OpenProxyNodeParams 解出节点参数明文（明文 JSON）。解密失败一律报错，绝不把密文当参数用。
func OpenProxyNodeParams(node model.ProxyNode) (string, error) {
	if !secret.IsSealed(node.Params) {
		return node.Params, nil
	}
	plain, err := secret.OpenWith(proxyNodeAAD, node.Params)
	if err != nil {
		return "", fmt.Errorf("节点 %q 的参数解不开（加密密钥与录入时不符）: %w", node.Name, err)
	}
	return plain, nil
}

// ProxyNodeList 列出全部节点（可只列启用的）。
func ProxyNodeList(conn *gorm.DB, onlyEnabled bool) ([]model.ProxyNode, error) {
	if conn == nil {
		conn = proxyNodeDB(conn)
	}
	q := conn.Model(&model.ProxyNode{}).Order("id asc")
	if onlyEnabled {
		q = q.Where("enabled = ?", true)
	}
	var nodes []model.ProxyNode
	if err := q.Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("list proxy nodes: %w", err)
	}
	return nodes, nil
}

// ProxyNodeCreate 新增一个节点（Params 传明文 JSON）。
func ProxyNodeCreate(conn *gorm.DB, in model.ProxyNodeInput) (model.ProxyNode, error) {
	if conn == nil {
		conn = proxyNodeDB(conn)
	}
	name := strings.TrimSpace(in.Name)
	server := strings.TrimSpace(in.Server)
	if name == "" || strings.TrimSpace(in.Type) == "" || server == "" || in.Port <= 0 || in.Port > 65535 {
		return model.ProxyNode{}, fmt.Errorf("节点信息不完整：name/type/server/port 必填且 port 合法")
	}
	sealed, err := sealProxyNodeParams(in.Params)
	if err != nil {
		return model.ProxyNode{}, err
	}
	node := model.ProxyNode{
		Name:    name,
		Type:    strings.ToLower(strings.TrimSpace(in.Type)),
		Server:  server,
		Port:    in.Port,
		Params:  sealed,
		Source:  "manual",
		Enabled: in.Enabled == nil || *in.Enabled,
	}
	if err := conn.Create(&node).Error; err != nil {
		return model.ProxyNode{}, fmt.Errorf("create proxy node: %w", err)
	}
	return node, nil
}

// ProxyNodeUpdate 按 id 更新节点；Params 为空串表示不改参数。
func ProxyNodeUpdate(conn *gorm.DB, id int, in model.ProxyNodeInput) (model.ProxyNode, error) {
	if conn == nil {
		conn = proxyNodeDB(conn)
	}
	var node model.ProxyNode
	if err := conn.First(&node, id).Error; err != nil {
		return model.ProxyNode{}, err
	}
	updates := map[string]any{}
	if v := strings.TrimSpace(in.Name); v != "" {
		updates["name"] = v
	}
	if v := strings.TrimSpace(in.Type); v != "" {
		updates["type"] = strings.ToLower(v)
	}
	if v := strings.TrimSpace(in.Server); v != "" {
		updates["server"] = v
	}
	if in.Port > 0 && in.Port <= 65535 {
		updates["port"] = in.Port
	}
	if strings.TrimSpace(in.Params) != "" {
		sealed, err := sealProxyNodeParams(in.Params)
		if err != nil {
			return model.ProxyNode{}, err
		}
		updates["params"] = sealed
	}
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
	}
	if len(updates) > 0 {
		if err := conn.Model(&model.ProxyNode{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return model.ProxyNode{}, fmt.Errorf("update proxy node: %w", err)
		}
	}
	if err := conn.First(&node, id).Error; err != nil {
		return model.ProxyNode{}, err
	}
	return node, nil
}

// ProxyNodeDelete 删除节点：被渠道或账号绑定时拒绝（先解绑），避免"代理设了但节点没了"的静默失效。
func ProxyNodeDelete(conn *gorm.DB, id int) error {
	if conn == nil {
		conn = proxyNodeDB(conn)
	}
	usedBy, err := proxyNodeUsages(conn, id)
	if err != nil {
		return err
	}
	if len(usedBy) > 0 {
		return fmt.Errorf("%w：仍被 %s 引用，先解绑再删除", ErrProxyNodeInUse, strings.Join(usedBy, "、"))
	}
	return conn.Delete(&model.ProxyNode{}, id).Error
}

// ProxyNodeMarkProbe 把一次出口探活的结果写回节点（出口 IP 写进 LastExitIP，面板直接显示）。
func ProxyNodeMarkProbe(conn *gorm.DB, id int, exitIP string, ok bool, failure string) error {
	conn = proxyNodeDB(conn)
	now := time.Now()
	return conn.Model(&model.ProxyNode{}).Where("id = ?", id).Updates(map[string]any{
		"last_probe_at": now,
		"last_probe_ok": ok,
		"last_exit_ip":  exitIP,
		"last_error":    failure,
	}).Error
}

// ProxyNodeEndpoint 把"绑定到哪个节点"解析成"本轮请求从哪个本地出口出去"。
//
// 出口是内核为每个节点开的本地 HTTP 入站：http://127.0.0.1:<LocalPort>。
// 未就绪（没分配端口/已停用/不存在）时返回错误而**不是**空串或直连：
// 静默直连会把用户的真实出口暴露给上游，正好毁掉这个功能存在的意义。
func ProxyNodeEndpoint(nodeID int) (string, error) {
	conn := proxyNodeDB(nil)
	var node model.ProxyNode
	if err := conn.First(&node, nodeID).Error; err != nil {
		return "", fmt.Errorf("代理节点不存在（id=%d）：请重新选择节点", nodeID)
	}
	if !node.Enabled {
		return "", fmt.Errorf("代理节点 %q 已停用", node.Name)
	}
	if node.LocalPort <= 0 || node.LocalPort > 65535 {
		return "", fmt.Errorf("代理节点 %q 尚未就绪（内核未分配本地端口）：先在代理页启动内核并同步端口", node.Name)
	}
	return fmt.Sprintf("http://127.0.0.1:%d", node.LocalPort), nil
}

// ProxyNodeEndpointOrEmpty 是给"探测/统计"这类**不该因出口未就绪而报错**的场合用的宽松版本：
// 返回空串表示"没有绑定节点"，调用方按既有逻辑（全局代理/直连）走；
// 但"绑定了却未就绪"仍然返回错误 —— 宽松不等于把绑定的意图吞掉。
func ProxyNodeEndpointOrEmpty(nodeID int) (string, error) {
	if nodeID <= 0 {
		return "", nil
	}
	return ProxyNodeEndpoint(nodeID)
}

// ProxyNodeUsageMap 返回"节点 id → 引用它的渠道/账号名称列表"，供面板显示与删除前提示。
func ProxyNodeUsageMap(conn *gorm.DB) (map[string][]string, error) {
	conn = proxyNodeDB(conn)
	usage := map[string][]string{}
	var channels []model.Channel
	if err := conn.Model(&model.Channel{}).Where("proxy_node_id > 0").Find(&channels).Error; err != nil {
		return nil, fmt.Errorf("load channel proxy bindings: %w", err)
	}
	for _, c := range channels {
		id := fmt.Sprintf("%d", c.ProxyNodeID)
		usage[id] = append(usage[id], "渠道 "+c.Name)
	}
	var keys []model.ChannelKey
	if err := conn.Model(&model.ChannelKey{}).Where("proxy_node_id > 0").Find(&keys).Error; err != nil {
		return nil, fmt.Errorf("load channel key proxy bindings: %w", err)
	}
	for _, k := range keys {
		id := fmt.Sprintf("%d", k.ProxyNodeID)
		usage[id] = append(usage[id], "账号 "+k.Name)
	}
	return usage, nil
}

// proxyNodeUsages 找出谁在引用这个节点（渠道级与账号级各查一遍）。
func proxyNodeUsages(conn *gorm.DB, id int) ([]string, error) {
	var usages []string
	var channels []model.Channel
	if err := conn.Model(&model.Channel{}).Where("proxy_node_id = ?", id).Find(&channels).Error; err != nil {
		return nil, fmt.Errorf("check channel usage: %w", err)
	}
	for _, c := range channels {
		usages = append(usages, "渠道 "+c.Name)
	}
	var keys []model.ChannelKey
	if err := conn.Model(&model.ChannelKey{}).Where("proxy_node_id = ?", id).Find(&keys).Error; err != nil {
		return nil, fmt.Errorf("check channel key usage: %w", err)
	}
	for _, k := range keys {
		usages = append(usages, "账号 "+k.Name)
	}
	return usages, nil
}

// ProxyNodeImportYAML 把一份 Clash 配置里的节点导入节点池（按 name 去重）。
//
// 去重口径：同名节点视为"同一个站点的同一个入口"，内容（类型/服务器/端口/参数）一致时跳过，
// 不一致时更新——这样订阅轮换服务器地址后能一键刷新，而不会在库里堆出几十个历史副本。
func ProxyNodeImportYAML(conn *gorm.DB, raw []byte, source string, subID int) (ProxyImportResult, error) {
	if conn == nil {
		conn = proxyNodeDB(conn)
	}
	parsed, err := clashcfg.Parse(raw)
	if err != nil {
		return ProxyImportResult{Source: source}, err
	}
	result := ProxyImportResult{Source: source, Notes: parsed.Notes, Infos: parsed.Infos}

	existing := map[string]model.ProxyNode{}
	var rows []model.ProxyNode
	if err := conn.Model(&model.ProxyNode{}).Find(&rows).Error; err != nil {
		return result, fmt.Errorf("load existing proxy nodes: %w", err)
	}
	for _, row := range rows {
		existing[row.Name] = row
	}

	for _, node := range parsed.Nodes {
		params := node.ParamsJSON()
		if row, ok := existing[node.Name]; ok {
			sameType := row.Type == node.Type && row.Server == node.Server && row.Port == node.Port
			plain, err := OpenProxyNodeParams(row)
			if err != nil {
				// 解不开就更新（用新导入的内容覆盖），但要如实记一条，不能装作没事
				result.Notes = append(result.Notes, fmt.Sprintf("节点 %q 原参数解不开，已用导入内容覆盖", node.Name))
			} else if sameType && plain == params {
				result.Skipped++
				continue
			}
			sealed, err := sealProxyNodeParams(params)
			if err != nil {
				return result, err
			}
			updates := map[string]any{
				"type": node.Type, "server": node.Server, "port": node.Port, "params": sealed,
			}
			if subID > 0 {
				updates["sub_id"] = subID
				updates["source"] = source
			}
			if err := conn.Model(&model.ProxyNode{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
				return result, fmt.Errorf("update proxy node %q: %w", node.Name, err)
			}
			result.Updated++
			continue
		}
		sealed, err := sealProxyNodeParams(params)
		if err != nil {
			return result, err
		}
		row := model.ProxyNode{
			Name: node.Name, Type: node.Type, Server: node.Server, Port: node.Port,
			Params: sealed, Source: source, SubID: subID, Enabled: true,
		}
		if err := conn.Create(&row).Error; err != nil {
			return result, fmt.Errorf("create proxy node %q: %w", node.Name, err)
		}
		existing[node.Name] = row
		result.Imported++
	}
	return result, nil
}

// ---------- 订阅 ----------

// ProxySubscriptionList 列出订阅（不回明文地址）。
func ProxySubscriptionList(conn *gorm.DB) ([]model.ProxySubscription, error) {
	if conn == nil {
		conn = proxyNodeDB(conn)
	}
	var subs []model.ProxySubscription
	if err := conn.Model(&model.ProxySubscription{}).Order("id asc").Find(&subs).Error; err != nil {
		return nil, fmt.Errorf("list proxy subscriptions: %w", err)
	}
	return subs, nil
}

// ProxySubscriptionCreate 新增订阅（地址密文落库，另存一串脱敏提示便于识别）。
func ProxySubscriptionCreate(conn *gorm.DB, in model.ProxySubscriptionInput) (model.ProxySubscription, error) {
	if conn == nil {
		conn = proxyNodeDB(conn)
	}
	name := strings.TrimSpace(in.Name)
	rawURL := strings.TrimSpace(in.URL)
	if name == "" || rawURL == "" {
		return model.ProxySubscription{}, fmt.Errorf("订阅名与地址都必填")
	}
	sealed, err := secret.SealWith(proxySubscriptionAAD, rawURL)
	if err != nil {
		return model.ProxySubscription{}, fmt.Errorf("订阅地址加密失败：%w", err)
	}
	sub := model.ProxySubscription{
		Name: name, URLCipher: sealed, URLHint: clashcfg.RedactURL(rawURL),
		Enabled:     in.Enabled == nil || *in.Enabled,
		AutoRefresh: in.AutoRefresh != nil && *in.AutoRefresh,
	}
	if err := conn.Create(&sub).Error; err != nil {
		return model.ProxySubscription{}, fmt.Errorf("create proxy subscription: %w", err)
	}
	return sub, nil
}

// ProxySubscriptionUpdate 更新订阅（URL 留空表示不改）。
func ProxySubscriptionUpdate(conn *gorm.DB, id int, in model.ProxySubscriptionInput) (model.ProxySubscription, error) {
	if conn == nil {
		conn = proxyNodeDB(conn)
	}
	var sub model.ProxySubscription
	if err := conn.First(&sub, id).Error; err != nil {
		return model.ProxySubscription{}, err
	}
	updates := map[string]any{}
	if v := strings.TrimSpace(in.Name); v != "" {
		updates["name"] = v
	}
	if v := strings.TrimSpace(in.URL); v != "" {
		sealed, err := secret.SealWith(proxySubscriptionAAD, v)
		if err != nil {
			return model.ProxySubscription{}, fmt.Errorf("订阅地址加密失败：%w", err)
		}
		updates["url_cipher"] = sealed
		updates["url_hint"] = clashcfg.RedactURL(v)
	}
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
	}
	if in.AutoRefresh != nil {
		updates["auto_refresh"] = *in.AutoRefresh
	}
	if len(updates) > 0 {
		if err := conn.Model(&model.ProxySubscription{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return model.ProxySubscription{}, fmt.Errorf("update proxy subscription: %w", err)
		}
	}
	if err := conn.First(&sub, id).Error; err != nil {
		return model.ProxySubscription{}, err
	}
	return sub, nil
}

// ProxySubscriptionDelete 删除订阅；其节点一并删除（节点是订阅的派生数据），但被绑定的节点会拒绝整体删除。
func ProxySubscriptionDelete(conn *gorm.DB, id int) error {
	if conn == nil {
		conn = proxyNodeDB(conn)
	}
	var nodes []model.ProxyNode
	if err := conn.Model(&model.ProxyNode{}).Where("sub_id = ?", id).Find(&nodes).Error; err != nil {
		return fmt.Errorf("load subscription nodes: %w", err)
	}
	for _, node := range nodes {
		usages, err := proxyNodeUsages(conn, node.ID)
		if err != nil {
			return err
		}
		if len(usages) > 0 {
			return fmt.Errorf("订阅下的节点 %q 仍被引用（%s），先解绑再删除订阅", node.Name, strings.Join(usages, "、"))
		}
	}
	if err := conn.Where("sub_id = ?", id).Delete(&model.ProxyNode{}).Error; err != nil {
		return fmt.Errorf("delete subscription nodes: %w", err)
	}
	return conn.Delete(&model.ProxySubscription{}, id).Error
}

// ProxySubscriptionRefresh 拉取订阅并导入其节点。
func ProxySubscriptionRefresh(ctx context.Context, conn *gorm.DB, id int) (ProxyImportResult, error) {
	if conn == nil {
		conn = proxyNodeDB(conn)
	}
	var sub model.ProxySubscription
	if err := conn.First(&sub, id).Error; err != nil {
		return ProxyImportResult{}, err
	}
	if !secret.IsSealed(sub.URLCipher) {
		return ProxyImportResult{Source: sub.Name}, fmt.Errorf("订阅 %q 的地址未加密或为空，请重新保存一次地址", sub.Name)
	}
	rawURL, err := secret.OpenWith(proxySubscriptionAAD, sub.URLCipher)
	if err != nil {
		return ProxyImportResult{Source: sub.Name}, fmt.Errorf("订阅 %q 的地址解不开（加密密钥与录入时不符）", sub.Name)
	}
	body, err := clashcfg.Fetch(ctx, rawURL)
	now := time.Now()
	if err != nil {
		conn.Model(&model.ProxySubscription{}).Where("id = ?", id).
			Updates(map[string]any{"last_error": err.Error()})
		return ProxyImportResult{Source: sub.Name}, err
	}
	result, err := ProxyNodeImportYAML(conn, body, sub.Name, sub.ID)
	if err != nil {
		conn.Model(&model.ProxySubscription{}).Where("id = ?", id).
			Updates(map[string]any{"last_error": err.Error()})
		return result, err
	}
	var count int64
	conn.Model(&model.ProxyNode{}).Where("sub_id = ?", sub.ID).Count(&count)
	conn.Model(&model.ProxySubscription{}).Where("id = ?", id).Updates(map[string]any{
		"last_fetch_at": now, "last_error": "", "node_count": int(count),
		"info_note": strings.Join(result.Infos, " | "),
	})
	return result, nil
}

// ProxyNodeProbeAll 逐个探测启用的出口节点，把结果写回（last_probe_ok / last_exit_ip / last_error）。
//
// 为什么要有它：节点池会抖（实测同一节点几分钟前 200、几分钟后就 EOF），而"不通的节点"如果不被标记，
// 就会被一直选中——表现成"登录页打不开 / 采集 EOF"这种看起来像功能坏了的故障。这里定时把健康状态刷新，
// 选路与备用出口都据此跳过不健康的节点；探活恢复后又会自动可用。
func ProxyNodeProbeAll(ctx context.Context) {
	conn := proxyNodeDB(nil)
	var nodes []model.ProxyNode
	if err := conn.Where("enabled = ? AND local_port > 0", true).Order("id asc").Find(&nodes).Error; err != nil {
		log.Warnf("节点探活：读节点失败：%v", err)
		return
	}
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for _, node := range nodes {
		wg.Add(1)
		sem <- struct{}{}
		go func(node model.ProxyNode) {
			defer wg.Done()
			defer func() { <-sem }()
			ok, exitIP, failure := probeOneNode(node.LocalPort)
			if err := ProxyNodeMarkProbe(conn, node.ID, exitIP, ok, failure); err != nil {
				log.Warnf("节点探活：写回失败 id=%d：%v", node.ID, err)
			}
		}(node)
	}
	wg.Wait()
	log.Infof("节点探活完成：共 %d 个", len(nodes))
}

// ProxyChannelBlocker 是一个「渠道被不通的出口节点挡住」的实例。
type ProxyChannelBlocker struct {
	ChannelID   int    `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	NodeID      int    `json:"node_id"`
	NodeName    string `json:"node_name"`
	NodeError   string `json:"node_error"`
}

// ProxyNodeBlockedChannels 找出「启用中、绑定了出口节点、而该节点最近一次探活不通」的渠道。
//
// 为什么需要它：告警规则引擎的两个指标（错误率/延迟）都基于**已发生的转发流量**，
// 而"出口节点坏了"是流量还没发生的状态 —— 渠道被坏节点挡住之后根本没有请求进来，
// 于是错误率为零、永远不触发告警。实测这台机器上 115 个节点里 99 个不通、
// 15 个渠道里 10 个绑着坏节点，而面板与通知渠道全程静默，
// 只有主动去查库才发现（本会话就是这样发现的）。
//
// 只报「绑定关系 + 节点探活结论」，不替用户改绑定：换哪个节点是人的决定。
func ProxyNodeBlockedChannels() ([]ProxyChannelBlocker, error) {
	conn := proxyNodeDB(nil)
	var rows []struct {
		ChannelID   int
		ChannelName string
		NodeID      int
		NodeName    string
		NodeError   string
	}
	if err := conn.Table("channels AS c").
		Select("c.id AS channel_id, c.name AS channel_name, p.id AS node_id, p.name AS node_name, p.last_error AS node_error").
		Joins("JOIN proxy_nodes AS p ON p.id = c.proxy_node_id").
		Where("c.enabled = ? AND c.proxy_node_id <> 0 AND p.enabled = ? AND p.last_probe_ok = ?", true, true, false).
		Order("c.id asc").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询被节点挡住的渠道: %w", err)
	}

	out := make([]ProxyChannelBlocker, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProxyChannelBlocker{
			ChannelID:   r.ChannelID,
			ChannelName: r.ChannelName,
			NodeID:      r.NodeID,
			NodeName:    r.NodeName,
			NodeError:   r.NodeError,
		})
	}
	return out, nil
}

// probeOneNode 用某节点的本地出口探一个稳定目标，返回是否可达、出口 IP 与失败原因。
func probeOneNode(localPort int) (bool, string, string) {
	if localPort <= 0 {
		return false, "", "节点没有分配本地端口"
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", localPort)
	client, err := collector.ProxyClient(endpoint)
	if err != nil {
		return false, "", err.Error()
	}
	client.Timeout = 22 * time.Second
	// 探测目标要选"从这些出口一定到得了"的：实测 api.ipify.org 对整池节点都不可达（会把 115 个节点
	// 全判成不通，反而把健康筛选变成摆设）；Cloudflare 的 trace 端点稳定，且回包里直接带出口 IP。
	response, err := client.Get("https://www.cloudflare.com/cdn-cgi/trace")
	if err != nil {
		return false, "", trimProbeError(err.Error())
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	exitIP := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "ip=") {
			exitIP = strings.TrimSpace(strings.TrimPrefix(line, "ip="))
			break
		}
	}
	if response.StatusCode >= 400 {
		return false, exitIP, fmt.Sprintf("HTTP %d", response.StatusCode)
	}
	return true, exitIP, ""
}

// trimProbeError 压缩探活错误文本（日志/面板都要短）。
func trimProbeError(text string) string {
	if len(text) > 160 {
		return text[:160]
	}
	return text
}
