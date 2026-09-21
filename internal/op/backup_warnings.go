package op

import (
	"fmt"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/secret"
)

// importSecretWarnings 汇总"备份里带密文、但用本实例密钥解不开"的条数（渠道凭据之外的三类）。
//
// 与 countUndecryptableChannelKeys 同一口径: 密文与 credential.key 绑定, 跨实例恢复或换过密钥时
// 这些值还原不了。不回报的话, 表现出来只是"导入成功、随后节点探活全失败 / 官方账号 401",
// 而用户从提示里看不出是备份缺字段还是密钥不对。
func importSecretWarnings(dump *model.DBDump) []string {
	var warnings []string

	if n := countUndecryptable(dump.ProxyNodes, func(row model.ProxyNodeExport) []string { return []string{row.ParamsOut} }, proxyNodeAAD); n > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"有 %d 个出口节点的参数是密文但本实例的密钥解不开（备份来自另一实例或 credential.key 已更换），需要重新导入这些节点的订阅", n))
	}
	if n := countUndecryptable(dump.ProxySubscriptions, func(row model.ProxySubscriptionExport) []string { return []string{row.URLCipherOut} }, proxySubscriptionAAD); n > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"有 %d 条代理订阅地址是密文但本实例的密钥解不开，需要重新填写订阅地址", n))
	}
	// 官方账号的密文是 base64(nonce|ciphertext)、AAD=服务商（没有 enc:v1: 前缀），因此按服务商解。
	badAccounts := 0
	for _, row := range dump.OfficialAccounts {
		broken := false
		for _, enc := range []string{row.AccessCipherOut, row.RefreshCipherOut} {
			if enc == "" {
				continue
			}
			if _, err := officialDecrypt(row.Provider, enc); err != nil {
				broken = true
			}
		}
		if broken {
			badAccounts++
		}
	}
	if badAccounts > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"有 %d 个官方账号的凭据是密文但本实例的密钥解不开，需要重新授权", badAccounts))
	}
	return warnings
}

// countUndecryptable 统计一批密文字段里"解不开"的条数: 每行只要有一个字段解不开就计一行。
// secret.OpenWith 对未加前缀的值按明文原样返回（存量行不失效），所以这里只数真解不开的那些。
func countUndecryptable[T any](rows []T, fields func(T) []string, aad string) int {
	bad := 0
	for _, row := range rows {
		for _, value := range fields(row) {
			if value == "" {
				continue
			}
			if _, err := secret.OpenWith(aad, value); err != nil {
				bad++
				break
			}
		}
	}
	return bad
}

// countDanglingProxyNodeRefs 统计"绑定了出口节点、但该节点不在库里"的渠道与凭据条数。
//
// 导入是增量语义: 备份里的节点行可能因冲突被跳过、而渠道行照旧写入。绑定悬空的后果是转发
// fail-closed 直接报错（这是对的行为, 绝不静默直连真实出口），但必须在导入结果里说清楚,
// 否则表现成"导入成功、随后这些渠道全部 502"。
func countDanglingProxyNodeRefs() int {
	conn := db.GetDB()
	if conn == nil {
		return 0
	}
	var channels, keys int64
	conn.Raw("SELECT count(*) FROM channels WHERE proxy_node_id <> 0 AND proxy_node_id NOT IN (SELECT id FROM proxy_nodes)").Scan(&channels)
	conn.Raw("SELECT count(*) FROM channel_keys WHERE proxy_node_id <> 0 AND proxy_node_id NOT IN (SELECT id FROM proxy_nodes)").Scan(&keys)
	return int(channels + keys)
}
