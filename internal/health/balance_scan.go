package health

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/rhttp"
)

// BalanceRead 是一次余额读取的结果。
//
// Reason 是给用户看的失败原因（成功为空）——"未读到余额"必须能自证是哪种原因：
// 站点没有这个接口、被 Cloudflare 挡了、凭据不对、还是网络不通。只说"未知"用户无从下手，
// 这是本轮实测踩到的真问题：16 个渠道全挂在"未读到"，面板却没告诉用户为什么。
type BalanceRead struct {
	Quota     float64
	Used      float64
	Remaining float64
	OK        bool
	ReasonText string
	ReasonKey string // 机器可读的原因码（面板据此归类统计）
	HTTPCode  int
	// Source 是这次读数来自哪个协议族的接口（user_balance / usage / new_api_self / openai_billing）。
	// 面板与排障需要它：同一个"已用"数字，来自站点自有接口和来自 OpenAI Billing 兼容口径的
	// 可信度不同（后者常带占位额度）。
	Source string
	// Spent 是"已用"金额（USD）。有些站点只给用量、不给可信额度（实测 pipixia/转转 把额度写成 1 亿），
	// 此时 OK=false 但 Spent 仍有值：面板要能说清"额度不可信、已用 $33.97"。
	Spent float64
	// QuotaKnown 标记额度字段是否可信；占位值一律 false（绝不进总额）。
	QuotaKnown bool
	// InCurrency 标记这个数是**已经折算好的货币值**（USD），而不是 new-api 口径的"点"。
	//
	// 为什么必须分开：new-api 的 /api/user/self 给的是 quota 点（按 balance_points_per_unit 折算），
	// 而 /user/balance 与 /usage 给的就是美元。少这个标记，聚合层会把 32.53 美元再除一次 500000，
	// 结果显示成 $0.000065（本轮实测：apikey.fan 的 32.53 就是这么变成 6.5e-05 的）。
	InCurrency bool
}

// 原因码：面板与统计都用它，避免按文案匹配。
const (
	ReasonUnreachable  = "unreachable"   // 网络/超时/DNS
	ReasonEndpointGone = "no_endpoint"   // 端点不存在（404/405）：该站点没有这个余额接口
	ReasonUnauthorized = "unauthorized"  // 401/403：凭据不对或被 WAF 挡
	ReasonUnparsable   = "unparsable"    // 200 但正文里找不到额度字段
	ReasonManual       = "manual"        // 人手工录入
	ReasonProxyNode    = "proxy_node"    // 绑定的出口节点没就绪
)

// ReadBalance 用调用方给定的客户端读一次余额。
//
// client 由调用方构造是刻意的：余额请求要和转发走**同一个出口**（绑定节点时就是该节点的本地入站），
// 否则"余额从真实 IP 读、请求从节点出"本身就是一条可被关联的痕迹。
// path 为空时用 new-api 系的 /api/user/self；各家站点自建额度接口时用设置项覆盖。
func ReadBalance(ctx context.Context, baseURL, monitorToken string, client *http.Client, path string) BalanceRead {
	if strings.TrimSpace(path) == "" {
		path = BalanceUserSelfPath
	}
	endpoint := joinBalanceURL(baseURL, path)
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return BalanceRead{ReasonText: "余额端点地址非法", ReasonKey: ReasonEndpointGone}
	}
	if monitorToken != "" {
		req.Header.Set("Authorization", "Bearer "+monitorToken)
	}
	req.Header.Set("Accept", "application/json")
	if client == nil {
		return BalanceRead{ReasonText: "没有可用的出网客户端", ReasonKey: ReasonUnreachable}
	}
	resp, err := client.Do(req)
	if err != nil {
		return BalanceRead{ReasonText: "网络不可达：" + trimReason(err.Error()), ReasonKey: ReasonUnreachable}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return BalanceRead{ReasonText: fmt.Sprintf("上游拒绝读取（HTTP %d）：该站点不提供余额接口，或凭据无权读", resp.StatusCode),
			ReasonKey: ReasonUnauthorized, HTTPCode: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return BalanceRead{ReasonText: fmt.Sprintf("该站点没有余额接口（HTTP %d）", resp.StatusCode),
			ReasonKey: ReasonEndpointGone, HTTPCode: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return BalanceRead{ReasonText: "读取响应失败", ReasonKey: ReasonUnreachable, HTTPCode: resp.StatusCode}
	}
	q, u, r, ok := ParseBalancePayload(body)
	if !ok {
		return BalanceRead{ReasonText: "接口有响应但正文里没有额度字段（可自定义余额接口路径）",
			ReasonKey: ReasonUnparsable, HTTPCode: resp.StatusCode}
	}
	return BalanceRead{Quota: q, Used: u, Remaining: r, OK: true, HTTPCode: resp.StatusCode}
}

// trimReason 收窄错误文案长度：错误串可能很长且带换行，面板只留一句。
func trimReason(text string) string {
	text = strings.ReplaceAll(text, "\n", " ")
	if len(text) > 120 {
		return text[:120] + "…"
	}
	return text
}

// FetchBalance 对单个渠道做一次余额采集（只读，不停用）。
// baseURL 取渠道 BaseURL；monitorToken 为监控专用凭证，与转发 key 分离存储
// （P2 口径：监控凭证不复用转发凭据）；useProxy 为渠道代理开关。
// 端点失败/解析失败时返回 ok=false，调用方只记事件，不阻断转发。
func FetchBalance(ctx context.Context, baseURL, monitorToken string, useProxy bool) (quota, used, remaining float64, ok bool) {
	var client *http.Client
	var err error
	if useProxy {
		client, err = rhttp.Proxy()
	} else {
		client, err = rhttp.Direct()
	}
	if err != nil || client == nil {
		return 0, 0, 0, false
	}
	read := ReadBalance(ctx, baseURL, monitorToken, client, BalanceUserSelfPath)
	return read.Quota, read.Used, read.Remaining, read.OK
}

// ScanOneChannel 采集单个渠道并做指纹变化判定与阈值判定。
// lastFingerprint 为上次指纹（空表示首次）；threshold<=0 表示未配置阈值。
// 返回快照（Changed 指示指纹是否变化）与可选的阈值事件（nil 表示未触发）。
func ScanOneChannel(ctx context.Context, channelID int, baseURL, monitorToken string, useProxy bool, lastFingerprint string, threshold float64) (BalanceSnapshot, *ThresholdEvent) {
	snap := BalanceSnapshot{ChannelID: channelID}
	q, u, r, ok := FetchBalance(ctx, baseURL, monitorToken, useProxy)
	if !ok {
		return snap, nil
	}
	snap.Quota, snap.Used, snap.Remaining = q, u, r
	fp := FingerprintBalance(q, u, r)
	snap.Changed = fp != lastFingerprint
	if BelowThreshold(r, threshold) {
		return snap, &ThresholdEvent{ChannelID: channelID, Remaining: r, Threshold: threshold}
	}
	return snap, nil
}

// joinBalanceURL 拼接 base 与相对路径，避免双斜杠。
func joinBalanceURL(base, path string) string {
	if base == "" {
		return path
	}
	baseSlash := strings.HasSuffix(base, "/")
	pathSlash := strings.HasPrefix(path, "/")
	switch {
	case baseSlash && pathSlash:
		return base + path[1:]
	case !baseSlash && !pathSlash:
		return base + "/" + path
	default:
		return base + path
	}
}

// balanceProbePayload 仅供单测构造宽容字段样本，保持与上游形状解耦。
func balanceProbePayload(fields map[string]any) []byte {
	b, _ := json.Marshal(fields)
	return b
}
