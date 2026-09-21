package proxycore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

// BuildConfig 依据节点池生成内核配置，并返回其中"入站端口 → 节点"的映射。
//
// 形状（mihomo）：
//
//	proxies:   [ ...节点（参数来自密文解密后的明文）... ]
//	listeners: [ {name: octopus-<id>, type: http, port: <LocalPort>, listen: 127.0.0.1, proxy: <节点名>} ]
//	rules:     [ MATCH,DIRECT ]
//
// 几个刻意的取舍：
//   - **不写 mixed-port/port/socks-port**：不占那些"人尽皆知"的代理端口，出口只通过 listeners 暴露。
//   - **不导入 Clash 的 rules/proxy-groups**：走哪个出口由 octopus 的账号绑定决定，
//     把 Clash 的分流规则搬进来会变成两套路由打架。
//   - 每个入站端口只绑 127.0.0.1：出口是给本机 octopus 用的，不对局域网开放。
func BuildConfig(conn *gorm.DB) ([]byte, []int, error) {
	if conn == nil {
		return nil, nil, fmt.Errorf("数据库不可用")
	}
	var nodes []model.ProxyNode
	if err := conn.Model(&model.ProxyNode{}).Where("enabled = ?", true).Order("id asc").Find(&nodes).Error; err != nil {
		return nil, nil, fmt.Errorf("load proxy nodes: %w", err)
	}

	proxies := make([]map[string]any, 0, len(nodes))
	listeners := make([]map[string]any, 0, len(nodes))
	ports := make([]int, 0, len(nodes))
	names := map[string]bool{}
	for _, node := range nodes {
		if node.LocalPort <= 0 {
			continue
		}
		plain, err := op.OpenProxyNodeParams(node)
		if err != nil {
			// 解不开的节点必须让整车失败：把密文当参数写进配置，内核只会报一堆看不懂的错
			return nil, nil, err
		}
		extra := map[string]any{}
		if strings.TrimSpace(plain) != "" && strings.TrimSpace(plain) != "{}" {
			if err := json.Unmarshal([]byte(plain), &extra); err != nil {
				return nil, nil, fmt.Errorf("节点 %q 的参数不是合法 JSON：%w", node.Name, err)
			}
		}
		proxy := map[string]any{
			"name":   node.Name,
			"type":   node.Type,
			"server": node.Server,
			"port":   node.Port,
		}
		for key, value := range extra {
			switch key {
			case "name", "type", "server", "port":
				continue // 这四个字段由库里的列决定，不能被参数里的残留值覆盖
			}
			proxy[key] = value
		}
		proxies = append(proxies, proxy)

		// 内核要求 listen 必须是 IP（写 localhost 会被拒），端口一节点一个。
		listeners = append(listeners, map[string]any{
			"name":   fmt.Sprintf("octopus-%d", node.ID),
			"type":   "http",
			"port":   node.LocalPort,
			"listen": "127.0.0.1",
			"proxy":  node.Name,
		})
		ports = append(ports, node.LocalPort)
		names[node.Name] = true
	}
	if len(proxies) == 0 {
		return nil, nil, fmt.Errorf("没有可用节点（启用且已分配端口）")
	}

	config := map[string]any{
		"mode":         "rule",
		"log-level":    "warning",
		"allow-lan":    false,
		"bind-address": "127.0.0.1",
		"ipv6":         false,
		"proxies":      proxies,
		"listeners":    listeners,
		"rules":        []string{"MATCH,DIRECT"},
	}
	body, err := yaml.Marshal(config)
	if err != nil {
		return nil, nil, fmt.Errorf("生成内核配置失败：%w", err)
	}
	return body, ports, nil
}

// probeThrough 经指定本地出口访问探测地址，返回正文（出口 IP 探测用）。
func probeThrough(ctx context.Context, endpoint, probeURL string, dialer dialContextFunc) (string, error) {
	if strings.TrimSpace(probeURL) == "" {
		return "", fmt.Errorf("出口探测地址为空")
	}
	proxyURL, err := parseProxyURL(endpoint)
	if err != nil {
		return "", err
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy:                 http.ProxyURL(proxyURL),
			DialContext:           dialer,
			ResponseHeaderTimeout: 15 * time.Second,
		},
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return "", fmt.Errorf("构造探测请求失败：%w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("经节点出口探测失败：%w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", fmt.Errorf("读取探测响应失败：%w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("出口探测返回 HTTP %d：%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return strings.TrimSpace(string(body)), nil
}
