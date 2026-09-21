package model

import "time"

// ProxyNode 是一个从 Clash/mihomo 配置里导入的出口节点（R-proxy-001）。
//
// 设计口径：
//   - 节点参数（ss 的 password、vmess 的 uuid、hysteria2 的 password 等）是**凭据**，
//     一律密文落库，复用 internal/secret 的统一密钥口径（AAD: pool:proxy-node）；
//     明文只在进程内存在，出参一律不带 Params。
//   - LocalPort 是"这个节点在本地内核里对应的 HTTP 入站端口"，由 octopus 自己分配（冷门端口段），
//     账号绑定最终就落到 http://127.0.0.1:<LocalPort> 这样一个出口上；端口随节点启停增减，
//     所以绑定存 node_id 而不是存端口字符串。
//   - 探活结论（LastExitIP 等）只作运维信息，不参与选路。
type ProxyNode struct {
	ID        int    `json:"id" gorm:"primaryKey"`
	Name      string `json:"name" gorm:"not null;uniqueIndex:idx_proxy_node_name"` // 节点名（Clash 里可能重名，导入时按来源去重）
	Type      string `json:"type" gorm:"not null"`                                 // ss / vmess / vless / trojan / hysteria2 / anytls / socks5 / http ...
	Server    string `json:"server" gorm:"not null"`
	Port      int    `json:"port" gorm:"not null"`
	Params    string `json:"-" gorm:"not null;default:''"`                              // 密文：除 name/type/server/port 之外的全部节点字段
	Source    string `json:"source" gorm:"not null;default:''"`                         // 来源：订阅名或 manual
	SubID     int    `json:"sub_id" gorm:"not null;default:0;index:idx_proxy_node_sub"` // 归属订阅（0=手工/文件导入）
	LocalPort int    `json:"local_port" gorm:"not null;default:0"`                      // 本地内核入站端口（0=尚未分配）
	// 注意：这里不能写 gorm:"default:true" —— 带 default 标签时 GORM 把零值 false 当成"没给"，
	// 显式禁用会被写成 DEFAULT true（本项目在 APIKey.Enabled 上踩过同一个坑）。默认"启用"由入口层赋值。
	Enabled bool `json:"enabled" gorm:"not null"`

	LastProbeAt *time.Time `json:"last_probe_at,omitempty"`
	LastProbeOK bool       `json:"last_probe_ok" gorm:"not null;default:false"`
	LastExitIP  string     `json:"last_exit_ip" gorm:"not null;default:''"` // 最近一次探活看到的出口 IP（防关联的可见证据）
	LastError   string     `json:"last_error" gorm:"not null;default:''"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProxySubscription 是一个 Clash 订阅来源（R-proxy-001）。
//
// URL 里通常带 token（等于凭据），因此按凭据处理：密文落库、出参只给打码后的形状。
// 拉取纪律沿用项目既有口径：只走 http(s)、固定超时、响应体上限、不跟随跨域跳转。
type ProxySubscription struct {
	ID          int        `json:"id" gorm:"primaryKey"`
	Name        string     `json:"name" gorm:"not null;uniqueIndex:idx_proxy_sub_name"`
	URLCipher   string     `json:"-" gorm:"not null;default:''"`        // 密文：订阅地址
	URLHint     string     `json:"url_hint" gorm:"not null;default:''"` // 明文提示（scheme+host+路径前缀，不含 token）
	Enabled     bool       `json:"enabled" gorm:"not null"`
	AutoRefresh bool       `json:"auto_refresh" gorm:"not null;default:false"` // 是否随节点同步任务自动刷新
	NodeCount   int        `json:"node_count" gorm:"not null;default:0"`
	InfoNote    string     `json:"info_note" gorm:"not null;default:''"` // 订阅方塞在节点列表里的运营信息（剩余流量/到期日），原样展示
	LastFetchAt *time.Time `json:"last_fetch_at,omitempty"`
	LastError   string     `json:"last_error" gorm:"not null;default:''"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ProxyNodeInput 是节点新增/更新的入参形状（Params 为明文 JSON，落库前加密）。
type ProxyNodeInput struct {
	Name    string `json:"name" binding:"required"`
	Type    string `json:"type" binding:"required"`
	Server  string `json:"server" binding:"required"`
	Port    int    `json:"port" binding:"required"`
	Params  string `json:"params"` // 明文 JSON（除 name/type/server/port 外的字段）
	Enabled *bool  `json:"enabled"`
}

// ProxySubscriptionInput 是订阅新增/更新的入参形状。
type ProxySubscriptionInput struct {
	Name        string `json:"name" binding:"required"`
	URL         string `json:"url"` // 明文订阅地址（允许更新时留空表示不改）
	Enabled     *bool  `json:"enabled"`
	AutoRefresh *bool  `json:"auto_refresh"`
}
