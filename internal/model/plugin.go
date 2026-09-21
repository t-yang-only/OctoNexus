package model

import "time"

// Plugin 是社区反代扩展插件的注册信息（R-plugin-001）。
//
// 要解决的事：各种账号池（各家官方网页版、第三方中转、自建反代站）形态各异，逐家写适配器
// 会把主仓拖成"每来一个站点就改一次转发链"。所以把这一层开放出去 —— 社区玩家自己写反代工具
// （把某个账号池变成 OpenAI 兼容的 HTTP 接口），octopus 负责三件事：
//
//  1. 运行环境：为插件分配一个本地入站端口、托管其进程（启动/停止/状态/日志尾部）；
//  2. 全局隐秘代理：把插件出网强制指向节点池的某个出口（`OCTOPUS_EGRESS_PROXY` +
//     `HTTP_PROXY/HTTPS_PROXY/ALL_PROXY`），插件无需自己实现代理；未就绪时**拒绝启动**，
//     绝不静默直连真实 IP（与转发链同一口径）；
//  3. 接进主链路：自动注册成渠道（base_url=http://127.0.0.1:<port>），于是分压、监控、选路、
//     余额、失败记账这些既有能力对插件一律生效 —— 插件是"普通上游"，不是特殊分支。
//
// 清单文件是插件目录下的 plugin.json（见 internal/plugin/manifest.go），本表是它的落库视图
// 加上 octopus 自己维护的运行时列（Port / Status / PID / ChannelID）。
type Plugin struct {
	ID      int    `json:"id" gorm:"primaryKey"`
	Slug    string `json:"slug" gorm:"not null;uniqueIndex:idx_plugin_slug"` // 插件标识 = 目录名，唯一
	Name    string `json:"name" gorm:"not null"`
	Version string `json:"version" gorm:"not null;default:''"`
	// Runtime: exec（octopus 拉起进程，可注入出口）| http（指向已存在的服务，出口由对方自己负责）。
	Runtime    string `json:"runtime" gorm:"not null;default:'exec'"`
	Entry      string `json:"entry" gorm:"not null;default:''"`    // exec: 相对插件目录的入口文件；http: 服务地址
	Args       string `json:"args" gorm:"not null;default:''"`     // JSON 数组文本；支持 {port}/{dir}/{data}/{slug} 占位
	PortEnv    string `json:"port_env" gorm:"not null;default:''"` // 告诉插件监听端口的变量名（默认 PORT）
	Protocol   string `json:"protocol" gorm:"not null;default:'openai'"`
	BasePath   string `json:"base_path" gorm:"not null;default:'/v1'"`
	HealthPath string `json:"health_path" gorm:"not null;default:'/v1/models'"`
	EgressMode string `json:"egress_mode" gorm:"not null;default:'pool'"` // pool（强制走节点出口）| direct（显式声明允许直连）
	Enabled    bool   `json:"enabled" gorm:"not null"`                    // 是否允许被启动（不影响已在跑的进程）
	AutoStart  bool   `json:"auto_start" gorm:"not null"`                 // 随实例启动一起拉起（默认 false）
	// 注意：这里不能写 gorm:"default:true" —— 带 default 标签时 GORM 把零值 false 当"没给"，
	// 显式关掉会被写成 DEFAULT true（本项目在 APIKey.Enabled / ProxyNode.Enabled 上踩过同一个坑）。
	// 默认值一律由入口层赋值。
	AutoChannel bool `json:"auto_channel" gorm:"not null"` // 启动后自动注册/更新渠道（默认 true）

	EgressNodeID int `json:"egress_node_id" gorm:"not null;default:0"` // 0 = 用设置里的全局默认出口
	// TokenCipher 是自动注册渠道时用的凭据（密文，AAD "plugin:token"）：同一把明文既作为渠道 Key
	// 落库（经渠道凭据收口加密），也在启动时经环境变量注入给插件进程，插件据此校验"请求来自 octopus"。
	// 出参一律不带（json:"-"），面板也不回显。
	TokenCipher string `json:"-" gorm:"not null;default:''"`
	Port        int    `json:"port" gorm:"not null;default:0"`           // 分配到的本地入站端口（0=未分配）
	Status      string `json:"status" gorm:"not null;default:'stopped'"` // stopped / running / exited / error
	// 必须显式钉列名：GORM 的命名策略把 `PID` 拆成 `p_id`，而回写状态用的是 map（原始列名 "pid"），
	// 两处对不上就是"进程起来了、状态回写报 no such column: pid"——启动接口回 400，插件却在后台跑着。
	PID       int `json:"pid" gorm:"column:pid;not null;default:0"`
	ChannelID   int    `json:"channel_id" gorm:"not null;default:0"` // 自动注册出来的渠道主键（0=未注册）
	LastError   string `json:"last_error" gorm:"not null;default:''"`

	LastStartAt *time.Time `json:"last_start_at,omitempty"`
	LastExitAt  *time.Time `json:"last_exit_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// PluginUpdateRequest 是可改的运行期选定项（清单里的结构字段以文件为准，改文件后重新扫描）。
type PluginUpdateRequest struct {
	Enabled      *bool `json:"enabled"`
	AutoStart    *bool `json:"auto_start"`
	EgressNodeID *int  `json:"egress_node_id"`
}

// PluginStatus 是面板/API 的运行时视图：清单字段 + 实况（进程、端口、出口、日志尾部）。
// 出参按契约不带任何凭据：插件令牌只经环境变量注入给插件进程，不回显（回显等于把它写进日志与快照）。
type PluginStatus struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Runtime     string   `json:"runtime"`
	Enabled     bool     `json:"enabled"`
	AutoStart   bool     `json:"auto_start"`
	AutoChannel bool     `json:"auto_channel"`
	Protocol    string   `json:"protocol"`
	Entry       string   `json:"entry"`
	Args        []string `json:"args"`
	Running     bool     `json:"running"`
	Status      string   `json:"status"`
	PID         int      `json:"pid"`
	Port        int      `json:"port"`
	BasePath    string   `json:"base_path"`
	HealthPath  string   `json:"health_path"`
	// Endpoint 是"插件作为上游"的地址（形态与普通渠道的 base_url 一致，可被渠道直接引用）。
	Endpoint string `json:"endpoint"`
	// EgressProxy 是注入给插件的出口（http://127.0.0.1:<节点入站端口>）；direct 模式为空。
	EgressProxy  string `json:"egress_proxy"`
	EgressMode   string `json:"egress_mode"`
	EgressNodeID int    `json:"egress_node_id"`
	// EgressNote 说明出口为什么不可用（未绑节点/节点未就绪），此时插件**不会被启动**。
	EgressNote  string   `json:"egress_note,omitempty"`
	ChannelID   int      `json:"channel_id"`
	LastError   string   `json:"last_error,omitempty"`
	LastStartAt string   `json:"last_start_at,omitempty"`
	LastExitAt  string   `json:"last_exit_at,omitempty"`
	Dir         string   `json:"dir"`
	LogTail     []string `json:"log_tail,omitempty"`
	Detail      string   `json:"detail,omitempty"` // 清单解析警告（如"声明了 direct 出口"）
}
