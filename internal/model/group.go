package model

import (
	"fmt"
)

// 分组选择上游成员的模式。
type GroupMode string

const (
	GroupModeManual   GroupMode = "manual"   // 只使用人工选中的成员。
	GroupModeFailover GroupMode = "failover" // 按成员排序选择并在失败时切换。
)

// 分组 Relay 的持久化配置，数据库中以 JSON 存储。
type GroupRelayConfig struct {
	MemberMaxAttempts                     int `json:"member_max_attempts" binding:"omitempty,min=1"`                        // 单个成员包含首次请求的总尝试次数，仅在故障转移模式生效。
	MemberRetryIntervalSeconds            int `json:"member_retry_interval_seconds" binding:"omitempty,min=1"`              // 同一成员相邻两次尝试之间的等待秒数。
	MemberNonStreamResponseTimeoutSeconds int `json:"member_non_stream_response_timeout_seconds" binding:"omitempty,min=1"` // 单个成员返回完整非流式响应的超时秒数。
	MemberStreamFirstEventTimeoutSeconds  int `json:"member_stream_first_event_timeout_seconds" binding:"omitempty,min=1"`  // 单个成员返回首个有效流事件的超时秒数。
	MemberCooldownSeconds                 int `json:"member_cooldown_seconds" binding:"omitempty,min=1"`                    // 单个成员耗尽尝试后被跳过的秒数，仅在故障转移模式生效。
	MemberAffinitySeconds                 int `json:"member_affinity_seconds" binding:"omitempty,min=0"`                    // 成员亲和时间:故障切换成功后继续保持当前成员的秒数;当前成员失败会立即结束亲和,0 表示不保持。
}

// DefaultGroupRelayConfig 返回新分组使用的 Relay 默认配置。
func DefaultGroupRelayConfig() GroupRelayConfig {
	return GroupRelayConfig{
		MemberMaxAttempts:                     2,
		MemberRetryIntervalSeconds:            3,
		MemberNonStreamResponseTimeoutSeconds: 120,
		MemberStreamFirstEventTimeoutSeconds:  30,
		MemberCooldownSeconds:                 60,
		MemberAffinitySeconds:                 300,
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
	if config.MemberCooldownSeconds < 1 {
		config.MemberCooldownSeconds = defaults.MemberCooldownSeconds
	}
	if config.MemberAffinitySeconds < 0 {
		config.MemberAffinitySeconds = defaults.MemberAffinitySeconds
	}
}

// 客户端模型名称及其可手动选择或故障转移的上游分组。
type Group struct {
	ID           int              `json:"id" gorm:"primaryKey"`                                                          // 分组主键。
	Name         string           `json:"name" gorm:"unique;not null"`                                                   // 客户端请求使用的模型名称。
	Mode         GroupMode        `json:"mode" gorm:"not null;default:manual" binding:"omitempty,oneof=manual failover"` // 选择成员的模式。
	ActiveItemID int              `json:"active_item_id" gorm:"not null;default:0"`                                      // 手动模式指定的成员, 故障转移模式忽略该值, 0 表示未指定; 写入侧字段, 读取一律用响应中的 runtime.current_item_id, 出 JSON 仅为让备份转储带上它。
	RelayConfig  GroupRelayConfig `json:"relay_config" gorm:"serializer:json"`                                           // 该分组的 Relay 路由配置。
	Items        []GroupItem      `json:"items" gorm:"foreignKey:GroupID;constraint:OnDelete:CASCADE"`                   // 该分组可手动选择或故障转移的分组项; 读取时恒为数组, 空集合也给出以免各消费方各自兜底。
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

// 分组内一个可选择的成员项: 或引用一条渠道授权, 或引用一个子分组, 二者互斥。
// 引用渠道授权时该成员即一个可转发的上游目标; 引用子分组时选路把子分组展平后的成员并入本分组
// (op.FlattenGroupItems 递归展开), 本层只负责把引用存对: 二者必须且只能给一个,
// 防循环与深度上限由 op 层在写入时校验。
// 读取时补齐授权两侧的名称, 所属渠道与可用性: 界面只需展示与排序, 由此无需再按主键回查渠道, 模型与凭据。
// 补齐的字段不含上游凭据本身, 转发所需的完整授权由 Relay 另行按主键取。
type GroupItem struct {
	ID             int           `json:"id" gorm:"primaryKey"`                                                                 // 分组项主键。
	GroupID        int           `json:"group_id" gorm:"not null;index:idx_group_grant,unique;index:idx_group_child,unique"`     // 所属分组 ID。
	ChannelGrantID *int          `json:"channel_grant_id,omitempty" gorm:"index:idx_group_grant,unique"`                        // 引用的渠道授权 ID; 子分组成员为 NULL (NULL 不参与唯一判重)。
	ChildGroupID   *int          `json:"child_group_id,omitempty" gorm:"index:idx_group_child,unique"`                          // 引用的子分组 ID; 授权成员为 NULL。
	ChannelGrant   *ChannelGrant `json:"-" gorm:"foreignKey:ChannelGrantID;references:ID;constraint:OnDelete:CASCADE"`           // 仅用于声明级联外键, 授权被删除时成员随之删除; 读取时不填充, 展示所需字段见下方。
	Priority       int           `json:"priority" gorm:"not null"`                                                             // Priority 决定界面展示和故障转移模式下的成员切换顺序。

	ChannelID      int      `json:"channel_id" gorm:"-"`   // 授权所属渠道 ID。
	ChannelName    string   `json:"channel_name" gorm:"-"` // 授权所属渠道名称。
	ModelName      string   `json:"model_name" gorm:"-"`   // 授权引用的上游模型名称。
	KeyName        string   `json:"key_name" gorm:"-"`     // 授权引用的凭据名称。
	Protocols      Protocol `json:"protocols" gorm:"-"`    // 授权支持的协议位掩码。
	ChildGroupName string   `json:"child_group_name" gorm:"-"` // 子分组成员的目标分组名称; 授权成员为空。
	Available      bool     `json:"available" gorm:"-"`    // 授权成员: 渠道与凭据均启用且模型, 凭据均存在时为真; 子分组成员: 展平后代里任一可转发即为真 (op.groupSnapshot 回填, 与选路同一缓存底座); 为假表示该成员当前无法转发, 但仍需列出以便移除。
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
	Name        string           `json:"name" binding:"required"`                        // 客户端请求使用的模型名称。
	Mode        GroupMode        `json:"mode" binding:"omitempty,oneof=manual failover"` // 选择成员的模式, 留空按手动。
	RelayConfig GroupRelayConfig `json:"relay_config"`                                   // Relay 路由配置, 零值由后端补默认。
	Items       []GroupItemInput `json:"items"`                                          // 初始成员集合。
}

// 分组普通配置, 成员和当前成员的变更请求; 分组主键走路径, 不进请求体。
// 当前成员是分组的一个普通可选字段, 与其余字段共用本请求: 它不需要独立的权限, 审计或并发粒度。
type GroupUpdateRequest struct {
	Name         *string           `json:"name,omitempty"`                                           // Name 仅在名称变更时发送。
	Mode         *GroupMode        `json:"mode,omitempty" binding:"omitempty,oneof=manual failover"` // Mode 仅在选择模式变更时发送。
	RelayConfig  *GroupRelayConfig `json:"relay_config,omitempty"`                                   // RelayConfig 仅在 Relay 配置变更时发送完整配置。
	Items        *[]GroupItemInput `json:"items,omitempty"`                                          // 新的成员集合, 整体替换; 提交顺序即优先级顺序。
	ActiveItemID *int              `json:"active_item_id,omitempty"`                                 // 手动模式指定的当前成员, 0 表示取消选择; 用指针以便与"未提交该字段"区分。
}

// 提交分组成员时按渠道授权主键或子分组主键引用, 两者互斥由 ValidateGroupItemRef 校验。
// 授权与子分组在提交前均已存在, 主键必然存在, 故成员无需按名称引用;
// 成员自身的主键不参与提交: 整体替换按引用匹配, 已有成员的主键与统计由后端保留。
// 提交侧用 0 表示"不引用该侧"; 落库时 0 转为 NULL, 与外键及唯一索引的语义对齐。
type GroupItemInput struct {
	ChannelGrantID int `json:"channel_grant_id"` // 待引用的渠道授权 ID; 引用子分组时为 0。
	ChildGroupID   int `json:"child_group_id"`   // 待引用的子分组 ID; 引用渠道授权时为 0。
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
