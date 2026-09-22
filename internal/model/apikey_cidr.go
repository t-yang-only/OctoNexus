package model

import (
	"fmt"
	"net"
	"strings"
)

// ParseCIDRList 解析一组「IP 或 CIDR 网段」，返回可用于匹配的网段列表。
//
// 两条口径：
//   - 裸 IP（10.0.0.5）按单主机网段处理（等价 10.0.0.5/32 或 /128），
//     否则用户写单个 IP 会得到"永远不匹配"的诡异结果。
//   - 空列表返回 (nil, nil)，语义是"不限制"，调用方据此直接放行；
//     这与"配了但全写错"必须区分开——后者是错误，不能让请求静默通过。
func ParseCIDRList(entries []string) ([]*net.IPNet, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]*net.IPNet, 0, len(entries))
	for _, raw := range entries {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		if !strings.Contains(item, "/") {
			ip := net.ParseIP(item)
			if ip == nil {
				return nil, fmt.Errorf("invalid IP or CIDR: %s", item)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, network, err := net.ParseCIDR(item)
		if err != nil {
			return nil, fmt.Errorf("invalid IP or CIDR: %s", item)
		}
		out = append(out, network)
	}
	if len(out) == 0 {
		// 全是空白项：按"没配"处理，不要当成"配了但谁都匹配不上"。
		return nil, nil
	}
	return out, nil
}

// IPAllowedByCIDRs 判断 ip 是否落在任一网段内。networks 为空表示不限制。
func IPAllowedByCIDRs(ip string, networks []*net.IPNet) bool {
	if len(networks) == 0 {
		return true
	}
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		// 取不到来源 IP 时按拒绝处理：放行会让白名单在"拿不到 IP"的边缘情况下失效。
		return false
	}
	for _, network := range networks {
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}
