package model

import (
	"testing"
)

// 裸 IP 必须按单主机网段处理。
//
// 反例：若把 "10.0.0.5" 直接丢给 net.ParseCIDR 会失败，用户写了单个 IP 却
// 得到「永远不匹配」，白名单变成拒服务。
func TestParseCIDRListAcceptsBareIP(t *testing.T) {
	networks, err := ParseCIDRList([]string{"10.0.0.5"})
	if err != nil {
		t.Fatalf("裸 IP 应被接受: %v", err)
	}
	if len(networks) != 1 {
		t.Fatalf("应解析出 1 个网段，实得 %d", len(networks))
	}
	if !IPAllowedByCIDRs("10.0.0.5", networks) {
		t.Fatalf("裸 IP 应匹配它自己")
	}
	if IPAllowedByCIDRs("10.0.0.6", networks) {
		t.Fatalf("裸 IP 不该匹配邻居（/32 而非整个网段）")
	}
}

// IPv6 裸地址同样要能解析成 /128，别只照顾 IPv4。
func TestParseCIDRListAcceptsIPv6(t *testing.T) {
	networks, err := ParseCIDRList([]string{"::1"})
	if err != nil {
		t.Fatalf("IPv6 裸地址应被接受: %v", err)
	}
	if !IPAllowedByCIDRs("::1", networks) {
		t.Fatalf("IPv6 裸地址应匹配它自己")
	}
	if IPAllowedByCIDRs("::2", networks) {
		t.Fatalf("IPv6 裸地址不该匹配邻居")
	}
}

// CIDR 网段按前缀长度判断，边界要准。
func TestParseCIDRListRespectsMaskBoundary(t *testing.T) {
	networks, err := ParseCIDRList([]string{"192.168.1.0/24"})
	if err != nil {
		t.Fatalf("CIDR 应被接受: %v", err)
	}
	for _, ip := range []string{"192.168.1.1", "192.168.1.254", "192.168.1.0"} {
		if !IPAllowedByCIDRs(ip, networks) {
			t.Fatalf("%s 应在 192.168.1.0/24 内", ip)
		}
	}
	for _, ip := range []string{"192.168.2.1", "192.168.0.255", "10.0.0.1"} {
		if IPAllowedByCIDRs(ip, networks) {
			t.Fatalf("%s 不该在 192.168.1.0/24 内", ip)
		}
	}
}

// 空列表 = 不限制。这是升级安全性的分水岭：
// 存量 Key 的 allowed_cidrs 为空，行为必须与"没这个功能"逐字一致。
func TestIPAllowedEmptyListMeansUnrestricted(t *testing.T) {
	for _, ip := range []string{"1.2.3.4", "::1", "", "not-an-ip"} {
		if !IPAllowedByCIDRs(ip, nil) {
			t.Fatalf("空列表应放行 %q（不限制语义）", ip)
		}
	}
}

// 配了网段但取不到来源 IP 时必须拒绝，不能放行。
//
// 放行会让白名单在"地址解析不出来"的边缘情况下失效，而这正是攻击者
// 最可能去构造的输入。
func TestIPAllowedRejectsUnparsableIPWhenRestricted(t *testing.T) {
	networks, err := ParseCIDRList([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	for _, ip := range []string{"", "   ", "not-an-ip", "999.999.999.999"} {
		if IPAllowedByCIDRs(ip, networks) {
			t.Fatalf("受限时取不到合法 IP 应拒绝，却放行了 %q", ip)
		}
	}
}

// 多个条目之间是「或」关系。
func TestIPAllowedMatchesAnyEntry(t *testing.T) {
	networks, err := ParseCIDRList([]string{"10.0.0.0/8", "192.168.1.5", "2001:db8::/32"})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !IPAllowedByCIDRs("10.1.2.3", networks) {
		t.Fatalf("应命中第一个网段")
	}
	if !IPAllowedByCIDRs("192.168.1.5", networks) {
		t.Fatalf("应命中裸 IP 条目")
	}
	if !IPAllowedByCIDRs("2001:db8::1", networks) {
		t.Fatalf("应命中 IPv6 网段")
	}
	if IPAllowedByCIDRs("8.8.8.8", networks) {
		t.Fatalf("不该命中任何条目")
	}
}

// 非法取值必须报错，不能静默变空。
//
// 反例：把 "10.0.0.0/33" 当空处理，用户会以为白名单生效了，
// 实际是全放行 —— 这是最危险的一类静默失败。
func TestParseCIDRListRejectsInvalid(t *testing.T) {
	for _, bad := range []string{"10.0.0.0/33", "192.168.1.300", "abc", "10.0.0.0/-1"} {
		if _, err := ParseCIDRList([]string{bad}); err == nil {
			t.Fatalf("%q 应被拒绝", bad)
		}
	}
}

// 全是空白项按「没配」处理（不限制），而不是「配了但谁都匹配不上」。
func TestParseCIDRListBlankEntriesMeanUnrestricted(t *testing.T) {
	for _, input := range [][]string{{""}, {"   "}, {"", "  "}} {
		networks, err := ParseCIDRList(input)
		if err != nil {
			t.Fatalf("空白项不该报错: %v", err)
		}
		if len(networks) != 0 {
			t.Fatalf("空白项应解析为空列表，实得 %d 个", len(networks))
		}
		if !IPAllowedByCIDRs("1.2.3.4", networks) {
			t.Fatalf("空白项应等价于不限制")
		}
	}
}

// 混合输入：空白项被跳过，有效项照常生效。
func TestParseCIDRListSkipsBlankAmongValid(t *testing.T) {
	networks, err := ParseCIDRList([]string{"", "10.0.0.0/8", "  ", "192.168.1.1"})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(networks) != 2 {
		t.Fatalf("应保留 2 个有效网段，实得 %d", len(networks))
	}
	if !IPAllowedByCIDRs("10.9.9.9", networks) || !IPAllowedByCIDRs("192.168.1.1", networks) {
		t.Fatalf("有效项应生效")
	}
}
