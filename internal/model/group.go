package model

import (
	"fmt"
)

// 分组选择上游成员的模式。
type GroupMode string

const (
	GroupModeManual        GroupMode = "manual"         // 只使用人工选中的成员。
	GroupModeFailover      GroupMode = "failover"       // 按成员排序选择并在失败时切换。
	GroupModeLowestCost    GroupMode = "lowest_cost"    // R-route-001 第一阶段：按成员单价由低到高定序（无价格数据的成员沉底），失败/冷却/探测语义与 failover 一致。
	GroupModeQualityFirst  GroupMode = "quality_first"  // NM-DS-004：按成员最近窗口成功率由高到低定序（无样本按中性先验乐观处理），失败自动让位给表现更好的成员。
	GroupModeLowestLatency GroupMode = "lowest_latency" // NM-DS-006：按成员最近一次尝试耗时由小到大定序（无耗时数据按 0 参与排序，同样是乐观先验）。
	GroupModeLeastBusy     GroupMode = "least_busy"     // NM-DS-006：按成员当前在途请求数由少到多定序，把并发摊到空闲成员上。
	GroupModeLowestTpmRpm  GroupMode = "lowest_tpm_rpm" // NM-DS-014：按成员最近一分钟的消耗（token 数优先、其次请求数）由少到多定序；上游真实限额不可知，故排的是“我们自己近期往它发了多少”，效果是摊平负载、少撞限流。
	GroupModeWeighted      GroupMode = "weighted"       // NM-DS-018：加权综合（成本/质量/延迟/在途/近期消耗），权重来自设置项 route_weight_*，让「哪一维更重要」由用户决定。
	GroupModeAllocate      GroupMode = "allocate"       // T-allocate-001：额度分压——按成员各自的剩余请求数（包月余量, 或余额÷单次请求成本）按比例分配流量：谁还能干得多一点谁就多承担一点，而不是把流量全压在同一把钥匙上直到它被榨干（用户口径："每个 key 可能只有一点钱，所以要进行分压，按照剩余请求数进行最优分配"）。
	// NM-DS-XXX 智能路由（对齐阶跃 Step Router 的用法）：客户端只填一个模型名（就是分组名），
	// 由本层按**请求特征**判定复杂度并决定走哪一档成员——复杂请求交给靠前的成员（决策引擎），
	// 简单请求交给靠后的成员（执行引擎），从而在不改客户端的前提下同时控成本与保质量。
	// 判定特征与阶跃文档一致：消息轮数、输入量、工具数量。
	GroupModeSmart GroupMode = "smart"
)

// IsValid 报告该模式是否为已支持的分组选路模式（导入备份等按值校验的入口用它,
// 避免像 oneof 标签那样每加一个模式都要各自同步一遍）: 新增模式时改这里,
// 并同步 Group/GroupCreateRequest/GroupUpdateRequest 三处 binding oneof 标签。
func (mode GroupMode) IsValid() bool {
	switch mode {
	case GroupModeManual, GroupModeFailover, GroupModeLowestCost, GroupModeQualityFirst, GroupModeLowestLatency, GroupModeLeastBusy, GroupModeLowestTpmRpm, GroupModeWeighted, GroupModeSmart, GroupModeAllocate:
		return true
	}
	return false
}

// 分组 Relay 的持久化配置，数据库中以 JSON 存储。
type GroupRelayConfig struct {
	MemberMaxAttempts                     int `json:"member_max_attempts" binding:"omitempty,min=1"`                        // 单个成员包含首次请求的总尝试次数，仅在故障转移模式生效。
	MemberRetryIntervalSeconds            int `json:"member_retry_interval_seconds" binding:"omitempty,min=1"`              // 同一成员相邻两次尝试之间的等待秒数。
	MemberNonStreamResponseTimeoutSeconds int `json:"member_non_stream_response_timeout_seconds" binding:"omitempty,min=1"` // 单个成员返回完整非流式响应的超时秒数。
	MemberStreamFirstEventTimeoutSeconds  int `json:"member_stream_first_event_timeout_seconds" binding:"omitempty,min=1"`  // 单个成员返回首个有效流事件的超时秒数。
	// 首个事件之后的「无进展」上限（T-timeout-001）。上游吐了首帧就再也不出字时，原先没有任何上限：
	// 首帧超时定时器在首个事件到达后就停掉了，转发循环会一直阻塞在读事件上，直到客户端自己放弃
	// （线上日志实证：流式请求挂着 300–760 秒才被客户端断开，而当时配的首帧超时只有 25 秒）。
	// 语义：两次「进展」之间（收到一个上游事件 或 成功写给客户端一帧）的最长间隔，命中即结束本次响应。
	// 0 = 关闭该保护（保持旧行为）。默认 300 秒是刻意留宽：部分中转站是「假流式」——先发一个
	// 角色空帧，正文等整段生成完才一次性下发，静默间隔可能长于普通的逐字流式。
	MemberStreamIdleTimeoutSeconds int `json:"member_stream_idle_timeout_seconds" binding:"omitempty,min=0"`
	MemberCooldownSeconds          int `json:"member_cooldown_seconds" binding:"omitempty,min=1"` // 单个成员耗尽尝试后被跳过的秒数，仅在故障转移模式生效。
	MemberAffinitySeconds          int `json:"member_affinity_seconds" binding:"omitempty,min=0"` // 成员亲和时间:故障切换成功后继续保持当前成员的秒数;当前成员失败会立即结束亲和,0 表示不保持。
	// 首字竞速 (T-hedge-001): 提交首字节之前并发向排序靠前的多个成员发同一请求, 取最快给出有效响应者。
	// 默认关闭: 竞速意味着同一份输入可能被多个上游各处理一次, 上游按 token 计费时最坏付 width 份钱。
	HedgeEnabled      bool `json:"hedge_enabled"`                                         // 是否开启首字竞速。
	HedgeWidth        int  `json:"hedge_width" binding:"omitempty,min=2,max=5"`           // 并发路数（含首选）, 2..5, 缺省 2。
	HedgeAfterMs      int  `json:"hedge_after_ms" binding:"omitempty,min=0"`              // 首选在该毫秒数内无首个有效响应就追加竞速路, 0 表示不按延迟触发。
	HedgePeakInFlight int  `json:"hedge_peak_in_flight" binding:"omitempty,min=0,max=64"` // 该分组在途请求数达到该值时立即并发竞速, 0 表示不按在途触发。                    // 成员亲和时间:故障切换成功后继续保持当前成员的秒数;当前成员失败会立即结束亲和,0 表示不保持。
	// 智能路由（GroupModeSmart）的复杂度阈值, 0..100, 缺省 50：请求复杂度评分 ≥ 该值就算「复杂请求」,
	// 走靠前的成员（决策引擎档）; 低于该值走靠后的成员（执行引擎档）。评分口径见 internal/relay/smart.go。
	SmartRouteThreshold int `json:"smart_route_threshold" binding:"omitempty,min=1,max=100"`
}

// DefaultGroupRelayConfig 返回新分组使用的 Relay 默认配置。
func DefaultGroupRelayConfig() GroupRelayConfig {
	return GroupRelayConfig{
		MemberMaxAttempts:                     2,
		MemberRetryIntervalSeconds:            3,
		MemberNonStreamResponseTimeoutSeconds: 120,
		MemberStreamFirstEventTimeoutSeconds:  30,
		MemberStreamIdleTimeoutSeconds:        300,
		MemberCooldownSeconds:                 60,
		MemberAffinitySeconds:                 300,
		HedgeWidth:                            2,
		HedgeAfterMs:                          800,
		SmartRouteThreshold:                   50,
	}
}

// NormalizeGroupRelayConfig 补齐分组 Relay 配置中的空值。
func NormalizeGroupRelayConfig(config *GroupRelayConfig) {
	defaults := DefaultGroupRelayConfig()
	if *config == (GroupRelayConfig{}) {
		*config = defaults
		return
	}
	if config.MemberMaxAttempts < 1 {
		config.MemberMaxAttempts = defaults.MemberMaxAttempts
	}
	if config.MemberRetryIntervalSeconds < 1 {
		config.MemberRetryIntervalSeconds = defaults.MemberRetryIntervalSeconds
	}
	if config.MemberNonStreamResponseTimeoutSeconds < 1 {
		config.MemberNonStreamResponseTimeoutSeconds = defaults.MemberNonStreamResponseTimeoutSeconds
	}
	if config.MemberStreamFirstEventTimeoutSeconds < 1 {
		config.MemberStreamFirstEventTimeoutSeconds = defaults.MemberStreamFirstEventTimeoutSeconds
	}
	// 与其它超时不同, 这里**不**用默认值兜底: 0 是「关闭该保护」的合法取值, 已经存在的分组
	// （存量 JSON 里没有这个键）升上来时保持旧行为不变, 是否收紧由使用方在面板上显式决定。
	if config.MemberStreamIdleTimeoutSeconds < 0 {
		config.MemberStreamIdleTimeoutSeconds = 0
	}
	if config.MemberCooldownSeconds < 1 {
		config.MemberCooldownSeconds = defaults.MemberCooldownSeconds
	}
	if config.MemberAffinitySeconds < 0 {
		config.MemberAffinitySeconds = defaults.MemberAffinitySeconds
	}
	// 智能路由阈值: 只在智能路由模式下有意义; 越界或未设置都夹回默认 50（1..100）。
	// 与流式无进展上限不同, 这里兜底是安全的 —— 阈值只在 GroupModeSmart 下被读, 不会改变既有模式的行为。
	if config.SmartRouteThreshold < 1 || config.SmartRouteThreshold > 100 {
		config.SmartRouteThreshold = defaults.SmartRouteThreshold
	}
	// 竞速配置: 未开启时保持全零（不影响任何既有行为）; 开启后把越界值夹到边界。
	if config.HedgeEnabled {
		if config.HedgeWidth == 0 {
			config.HedgeWidth = defaults.HedgeWidth
		}
		if config.HedgeWidth < 2 {
			config.HedgeWidth = 2
		}
		if config.HedgeWidth > 5 {
			config.HedgeWidth = 5
		}
		if config.HedgeAfterMs < 0 {
			config.HedgeAfterMs = 0
		}
		if config.HedgePeakInFlight < 0 {
			config.HedgePeakInFlight = 0
		}
		if config.HedgePeakInFlight > 64 {
			config.HedgePeakInFlight = 64
		}
	}
}

// 客户端模型名称及其可手动选择或故障转移的上游分组。
type Group struct {
	ID           int              `json:"id" gorm:"primaryKey"`                                                                                                                                                     // 分组主键。
	Name         string           `json:"name" gorm:"unique;not null"`                                                                                                                                              // 客户端请求使用的模型名称。
	Mode         GroupMode        `json:"mode" gorm:"not null;default:manual" binding:"omitempty,oneof=manual failover lowest_cost quality_first lowest_latency least_busy lowest_tpm_rpm weighted smart allocate"` // 选择成员的模式。
	ActiveItemID int              `json:"active_item_id" gorm:"not null;default:0"`                                                                                                                                 // 手动模式指定的成员, 故障转移模式忽略该值, 0 表示未指定; 写入侧字段, 读取一律用响应中的 runtime.current_item_id, 出 JSON 仅为让备份转储带上它。
	RelayConfig  GroupRelayConfig `json:"relay_config" gorm:"serializer:json"`                                                                                                                                      // 该分组的 Relay 路由配置。
	Items        []GroupItem      `json:"items" gorm:"foreignKey:GroupID;constraint:OnDelete:CASCADE"`                                                                                                              // 该分组可手动选择或故障转移的分组项; 读取时恒为数组, 空集合也给出以免各消费方各自兜底。
}

// WithItemsForTest 返回成员被整体替换为 items 的分组副本（仅测试接线用，
// 生产路径一律经缓存写回，不直接拼装分组形状）。
func (group Group) WithItemsForTest(items []GroupItem) Group {
	group.Items = items
	return group
}

// WithItems 返回成员被整体替换为 flat 的分组副本: 供选路把展平后的授权平面表
// 喂给路由选择, 亲和/冷却/上限等路由状态仍按顶层分组 ID 持有 (冻结口径)。
// 本体只读引用, 不改缓存行。
func (group Group) WithItems(flat []GroupItem) Group {
	group.Items = flat
	return group
}

// 智能路由（mode=smart）成员的显式档位取值。空值 = 未声明，此时按成员顺序自动对半切分（既有行为）。
const (
	GroupSmartTierAuto      = ""          // 未声明档位：按顶层成员顺序自动对半切分。
	GroupSmartTierDecision  = "decision"  // 决策引擎档：强/贵，复杂请求优先用它。
	GroupSmartTierExecution = "execution" // 执行引擎档：快/便宜，简单请求优先用它。
)

// IsValidSmartTier 报告显式档位取值是否合法；空值合法（= 未声明，回落到顺序口径）。
func IsValidSmartTier(tier string) bool {
	switch tier {
	case GroupSmartTierAuto, GroupSmartTierDecision, GroupSmartTierExecution:
		return true
	}
	return false
}

// 分组内一个可选择的成员项: 或引用一条渠道授权, 或引用一个子分组, 二者互斥。
// 引用渠道授权时该成员即一个可转发的上游目标; 引用子分组时选路把子分组展平后的成员并入本分组
// (op.FlattenGroupItems 递归展开), 本层只负责把引用存对: 二者必须且只能给一个,
// 防循环与深度上限由 op 层在写入时校验。
// 读取时补齐授权两侧的名称, 所属渠道与可用性: 界面只需展示与排序, 由此无需再按主键回查渠道, 模型与凭据。
// 补齐的字段不含上游凭据本身, 转发所需的完整授权由 Relay 另行按主键取。
type GroupItem struct {
	ID             int           `json:"id" gorm:"primaryKey"`                                                               // 分组项主键。
	GroupID        int           `json:"group_id" gorm:"not null;index:idx_group_grant,unique;index:idx_group_child,unique"` // 所属分组 ID。
	ChannelGrantID *int          `json:"channel_grant_id,omitempty" gorm:"index:idx_group_grant,unique"`                     // 引用的渠道授权 ID; 子分组成员为 NULL (NULL 不参与唯一判重)。
	ChildGroupID   *int          `json:"child_group_id,omitempty" gorm:"index:idx_group_child,unique"`                       // 引用的子分组 ID; 授权成员为 NULL。
	ChannelGrant   *ChannelGrant `json:"-" gorm:"foreignKey:ChannelGrantID;references:ID;constraint:OnDelete:CASCADE"`       // 仅用于声明级联外键, 授权被删除时成员随之删除; 读取时不填充, 展示所需字段见下方。
	Priority       int           `json:"priority" gorm:"not null"`                                                           // Priority 决定界面展示和故障转移模式下的成员切换顺序。
	// SmartTier 是智能路由（mode=smart）的显式档位：留空 = 按成员顺序自动对半切分（既有行为，逐字不变），
	// decision = 决策引擎档（强/贵，复杂请求优先用它），execution = 执行引擎档（快/便宜）。
	// 只要有任意一个成员显式声明了档位，整个分组就改走显式口径：声明 decision 的进决策档，
	// 其余（含未声明的）进执行档；目标档为空时选路会回退全体成员，所以「标错一边」不会让功能不可用。
	SmartTier string `json:"smart_tier,omitempty" gorm:"size:16" binding:"omitempty,oneof=decision execution"`

	ChannelID      int      `json:"channel_id" gorm:"-"`       // 授权所属渠道 ID。
	ChannelName    string   `json:"channel_name" gorm:"-"`     // 授权所属渠道名称。
	ModelName      string   `json:"model_name" gorm:"-"`       // 授权引用的上游模型名称。
	KeyName        string   `json:"key_name" gorm:"-"`         // 授权引用的凭据名称。
	Protocols      Protocol `json:"protocols" gorm:"-"`        // 授权支持的协议位掩码。
	ChildGroupName string   `json:"child_group_name" gorm:"-"` // 子分组成员的目标分组名称; 授权成员为空。
	Available      bool     `json:"available" gorm:"-"`        // 授权成员: 渠道与凭据均启用且模型, 凭据均存在时为真; 子分组成员: 展平后代里任一可转发即为真 (op.groupSnapshot 回填, 与选路同一缓存底座); 为假表示该成员当前无法转发, 但仍需列出以便移除。
}

// GrantRef 返回本成员引用的渠道授权主键; 子分组成员返回 0 (不匹配任何授权, 调用方按缺失处理)。
func (item GroupItem) GrantRef() int {
	if item.ChannelGrantID != nil {
		return *item.ChannelGrantID
	}
	return 0
}

// ChildRef 返回本成员引用的子分组主键; 授权成员返回 0。
func (item GroupItem) ChildRef() int {
	if item.ChildGroupID != nil {
		return *item.ChildGroupID
	}
	return 0
}

// GroupItemMaxDepth 是分组嵌套引用的最大深度, 防止校验与未来的递归展开被恶意或手误的自引用拖死。
// 8 层已远超真实层级需求; 子分组链超过该深度视为配置错误, 拒绝写入。
const GroupItemMaxDepth = 8

// ValidateGroupItemRef 校验一条成员输入的引用形状: 渠道授权与子分组二选一, 不得同时给出或同时为空。
// childGroupID 有效性 (存在性/非自引用/防循环) 属于 op 层事务内校验, 此处只判形状。
func ValidateGroupItemRef(channelGrantID, childGroupID int) error {
	switch {
	case channelGrantID != 0 && childGroupID != 0:
		return fmt.Errorf("group item cannot reference both a channel grant and a child group")
	case channelGrantID == 0 && childGroupID == 0:
		return fmt.Errorf("group item must reference either a channel grant or a child group")
	}
	return nil
}

// 创建分组请求; 成员顺序即优先级顺序。
// 不收主键与当前成员: 分组主键由数据库分配, 当前成员在创建后另行指定。
type GroupCreateRequest struct {
	Name        string           `json:"name" binding:"required"`                                                                                                                   // 客户端请求使用的模型名称。
	Mode        GroupMode        `json:"mode" binding:"omitempty,oneof=manual failover lowest_cost quality_first lowest_latency least_busy lowest_tpm_rpm weighted smart allocate"` // 选择成员的模式, 留空按手动。
	RelayConfig GroupRelayConfig `json:"relay_config"`                                                                                                                              // Relay 路由配置, 零值由后端补默认。
	Items       []GroupItemInput `json:"items"`                                                                                                                                     // 初始成员集合。
}

// 分组普通配置, 成员和当前成员的变更请求; 分组主键走路径, 不进请求体。
// 当前成员是分组的一个普通可选字段, 与其余字段共用本请求: 它不需要独立的权限, 审计或并发粒度。
type GroupUpdateRequest struct {
	Name         *string           `json:"name,omitempty"`                                                                                                                                      // Name 仅在名称变更时发送。
	Mode         *GroupMode        `json:"mode,omitempty" binding:"omitempty,oneof=manual failover lowest_cost quality_first lowest_latency least_busy lowest_tpm_rpm weighted smart allocate"` // Mode 仅在选择模式变更时发送。
	RelayConfig  *GroupRelayConfig `json:"relay_config,omitempty"`                                                                                                                              // RelayConfig 仅在 Relay 配置变更时发送完整配置。
	Items        *[]GroupItemInput `json:"items,omitempty"`                                                                                                                                     // 新的成员集合, 整体替换; 提交顺序即优先级顺序。
	ActiveItemID *int              `json:"active_item_id,omitempty"`                                                                                                                            // 手动模式指定的当前成员, 0 表示取消选择; 用指针以便与"未提交该字段"区分。
}

// 提交分组成员时按渠道授权主键或子分组主键引用, 两者互斥由 ValidateGroupItemRef 校验。
// 授权与子分组在提交前均已存在, 主键必然存在, 故成员无需按名称引用;
// 成员自身的主键不参与提交: 整体替换按引用匹配, 已有成员的主键与统计由后端保留。
// 提交侧用 0 表示"不引用该侧"; 落库时 0 转为 NULL, 与外键及唯一索引的语义对齐。
type GroupItemInput struct {
	ChannelGrantID int `json:"channel_grant_id"` // 待引用的渠道授权 ID; 引用子分组时为 0。
	ChildGroupID   int `json:"child_group_id"`   // 待引用的子分组 ID; 引用渠道授权时为 0。
	// SmartTier 是智能路由（mode=smart）的显式档位：留空 = 按成员顺序自动对半切分，
	// decision = 决策引擎档，execution = 执行引擎档。非法取值在绑定层挡下（400），不会落到这里。
	SmartTier string `json:"smart_tier,omitempty" binding:"omitempty,oneof=decision execution"`
}

// GrantRefPtr 返回授权引用的落库形状: 未引用时为 nil (NULL)。
func (input GroupItemInput) GrantRefPtr() *int {
	if input.ChannelGrantID == 0 {
		return nil
	}
	return &input.ChannelGrantID
}

// ChildRefPtr 返回子分组引用的落库形状: 未引用时为 nil (NULL)。
func (input GroupItemInput) ChildRefPtr() *int {
	if input.ChildGroupID == 0 {
		return nil
	}
	return &input.ChildGroupID
}
