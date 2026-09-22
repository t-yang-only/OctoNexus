package op

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
	"gorm.io/gorm"
)

var (
	groupCache     = cache.New[int, model.Group](16) // 按主键保存完整分组配置。
	groupNameIndex = cache.New[string, int](16)      // 客户端模型名对应的分组主键。
)

// GroupList 返回缓存中的全部分组, 成员已补齐界面展示所需的名称与可用性, 按名称定序。
// 不含实时路由状态: 路由状态由 Relay 持有, 而 Relay 依赖本包, 故由处理器在返回前补齐。
// 定序是为 API Key 面板的模型选择器: 那里没有排序开关, 而缓存遍历顺序随机;
// 分组页自带升降序开关, 会按开关重排, 不依赖此顺序。
func GroupList() []model.Group {
	groups := make([]model.Group, 0, groupCache.Len())
	for _, group := range groupCache.GetAll() {
		groups = append(groups, groupSnapshot(group))
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return groups
}

// GroupGet 返回指定分组的读取副本, 成员已补齐界面展示所需的名称与可用性。
// 不含实时路由状态: 与 GroupList 同理, 由处理器在返回前补齐。
func GroupGet(id int) (model.Group, error) {
	group, ok := groupCache.Get(id)
	if !ok {
		return model.Group{}, fmt.Errorf("group not found")
	}
	return groupSnapshot(group), nil
}

// GroupListModel 返回缓存中的全部分组模型名, 按名称定序。
// 两个消费方都不提供排序开关: /v1/models 由第三方客户端直接展示, API Key 面板按返回顺序列出可用模型,
// 而缓存遍历顺序随机, 故顺序须由此处定稿。
func GroupListModel() []string {
	models := make([]string, 0, groupCache.Len())
	for _, group := range groupCache.GetAll() {
		models = append(models, group.Name)
	}
	sort.Strings(models)
	return models
}

// GroupGetByName 返回客户端模型名称对应的分组配置, 供转发选路使用。
// 渠道或凭据被禁用及授权两侧缺失的成员不参与选路, 重新可用后会在下一轮读取时自动恢复。
func GroupGetByName(name string) (model.Group, error) {
	groupID, ok := groupNameIndex.Get(name)
	if !ok {
		return model.Group{}, fmt.Errorf("group not found")
	}
	group, ok := groupCache.Get(groupID)
	if !ok {
		return model.Group{}, fmt.Errorf("group not found")
	}
	group = groupSnapshot(group)
	group.Items = slices.DeleteFunc(group.Items, func(item model.GroupItem) bool { return !item.Available })
	return group, nil
}

// AutoGroupName 返回渠道自动分组的客户端模型名, 形如 渠道名/模型名。
// 名称去空白, 模型名保留原大小写: 分组名是客户端请求的模型标识, 大小写属于协议语义,
// 全局过滤等匹配另行规范化, 此处不替调用方改写。
func AutoGroupName(channelName, modelName string) (string, error) {
	channelName = strings.TrimSpace(channelName)
	modelName = strings.TrimSpace(modelName)
	if channelName == "" {
		return "", fmt.Errorf("channel name is required")
	}
	if modelName == "" {
		return "", fmt.Errorf("channel model name is required")
	}
	return channelName + "/" + modelName, nil
}

// autoGroupConfig 返回自动分组的固定配置: 故障转移优先按提交顺序选路,
// 附默认 Relay 参数, 后续人工仍可在分组页改回手动或调整参数。
func autoGroupConfig() (model.GroupMode, model.GroupRelayConfig) {
	return model.GroupModeFailover, model.DefaultGroupRelayConfig()
}

// ensureAutoGroupsLocked 在同一事务内为指定渠道的模型补齐 渠道名/模型名 自动分组,
// 已存在的分组按 grants 补齐缺失成员, 不删已有成员, 不改模式与 Relay 参数。
// 幂等: 重复调用不会产生重复分组或重复成员, 只补缺失。
// 调用方必须在渠道子表 (凭据, 模型, 授权) 同一事务内提交后调用, 否则查不到刚写入的授权。
// 缓存由调用方在事务提交后统一刷新, 此处只写库: 事务内的行对外部缓存不可见, 写了也读不到。
func ensureAutoGroupsLocked(tx *gorm.DB, channelID int, channelName string, modelNames []string) error {
	channelName = strings.TrimSpace(channelName)
	if channelName == "" {
		return fmt.Errorf("channel name is required")
	}
	seenModels := make(map[string]struct{}, len(modelNames))
	models := make([]string, 0, len(modelNames))
	for _, name := range modelNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seenModels[name]; ok {
			continue
		}
		seenModels[name] = struct{}{}
		models = append(models, name)
	}
	if len(models) == 0 {
		return nil
	}

	var channelModels []model.ChannelModel
	if err := tx.Where("channel_id = ?", channelID).Find(&channelModels).Error; err != nil {
		return fmt.Errorf("failed to load channel models: %w", err)
	}
	modelIDByName := make(map[string]int, len(channelModels))
	for _, channelModel := range channelModels {
		modelIDByName[channelModel.Name] = channelModel.ID
	}
	modelIDs := make([]int, 0, len(channelModels))
	for _, channelModel := range channelModels {
		modelIDs = append(modelIDs, channelModel.ID)
	}
	var grants []model.ChannelGrant
	if len(modelIDs) > 0 {
		if err := tx.Where("channel_model_id IN ?", modelIDs).Find(&grants).Error; err != nil {
			return fmt.Errorf("failed to load channel grants: %w", err)
		}
	}
	grantIDsByModel := make(map[int][]int, len(channelModels))
	for _, grant := range grants {
		grantIDsByModel[grant.ChannelModelID] = append(grantIDsByModel[grant.ChannelModelID], grant.ID)
	}

	mode, relayConfig := autoGroupConfig()
	for _, modelName := range models {
		modelID, ok := modelIDByName[modelName]
		if !ok {
			continue
		}
		grantIDs := grantIDsByModel[modelID]
		if len(grantIDs) == 0 {
			continue
		}
		sort.Ints(grantIDs)
		groupName, err := AutoGroupName(channelName, modelName)
		if err != nil {
			return err
		}
		var group model.Group
		if err := tx.Where("name = ?", groupName).First(&group).Error; err != nil {
			if err != gorm.ErrRecordNotFound {
				return fmt.Errorf("failed to load auto group %q: %w", groupName, err)
			}
			group = model.Group{Name: groupName, Mode: mode, RelayConfig: relayConfig}
			if err := tx.Create(&group).Error; err != nil {
				return fmt.Errorf("failed to create auto group %q: %w", groupName, err)
			}
		}
		var items []model.GroupItem
		if err := tx.Where("group_id = ?", group.ID).Find(&items).Error; err != nil {
			return fmt.Errorf("failed to load auto group items %q: %w", groupName, err)
		}
		grantSet := make(map[int]struct{}, len(items))
		maxPriority := 0
		for _, item := range items {
			grantSet[item.GrantRef()] = struct{}{}
			if item.Priority > maxPriority {
				maxPriority = item.Priority
			}
		}
		for _, grantID := range grantIDs {
			if _, ok := grantSet[grantID]; ok {
				continue
			}
			maxPriority++
			grantRef := grantID
			if err := tx.Create(&model.GroupItem{GroupID: group.ID, ChannelGrantID: &grantRef, Priority: maxPriority}).Error; err != nil {
				return fmt.Errorf("failed to append auto group item %q: %w", groupName, err)
			}
		}
	}
	// 上面只做了「新增」：遍历本次提交的模型，缺分组就建、缺成员就补。
	// 撤销授权后分组会残留（成员指向已删除的授权），客户端还能在列表里看到、能选中、
	// 然后失败 —— 所以这里补上「清理」。详见 pruneStaleAutoGroups 的注释。
	return pruneStaleAutoGroups(tx, channelID, channelName)
}

// GroupCreate 创建分组及其成员并刷新缓存, 返回创建后的分组。
// 成员的提交顺序即优先级顺序。
func GroupCreate(req *model.GroupCreateRequest, ctx context.Context) (*model.Group, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("group name is required")
	}
	group := model.Group{
		Name:        name,
		Mode:        req.Mode,
		RelayConfig: req.RelayConfig,
		Items:       make([]model.GroupItem, len(req.Items)),
	}
	if group.Mode == "" {
		group.Mode = model.GroupModeManual
	}
	model.NormalizeGroupRelayConfig(&group.RelayConfig)
	for i, item := range req.Items {
		if err := model.ValidateGroupItemRef(item.ChannelGrantID, item.ChildGroupID); err != nil {
			return nil, err
		}
		group.Items[i] = model.GroupItem{ChannelGrantID: item.GrantRefPtr(), ChildGroupID: item.ChildRefPtr(), Priority: i + 1, SmartTier: item.SmartTier}
	}
	// 子分组引用在落库前校验: 引用存在性可查, 自引用因新分组还没有主键而无法表达。
	if err := validateGroupTreeRefs(db.GetDB(), group.Items); err != nil {
		return nil, err
	}
	if err := db.GetDB().WithContext(ctx).Create(&group).Error; err != nil {
		return nil, err
	}
	groupCache.Set(group.ID, group)
	groupNameIndex.Set(group.Name, group.ID)
	snapshot := groupSnapshot(group)
	return &snapshot, nil
}

// GroupUpdate 更新分组配置, 成员和当前成员，并返回刷新后的分组。
func GroupUpdate(id int, req *model.GroupUpdateRequest, ctx context.Context) (*model.Group, error) {
	oldGroup, ok := groupCache.Get(id)
	if !ok {
		return nil, fmt.Errorf("group not found")
	}
	oldName := oldGroup.Name

	var selectFields []string
	updates := model.Group{ID: id}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, fmt.Errorf("group name is required")
		}
		selectFields = append(selectFields, "name")
		updates.Name = name
	}
	if req.Mode != nil {
		selectFields = append(selectFields, "mode")
		updates.Mode = *req.Mode
	}
	if req.RelayConfig != nil {
		config := *req.RelayConfig
		model.NormalizeGroupRelayConfig(&config)
		selectFields = append(selectFields, "relay_config")
		updates.RelayConfig = config
	}

	var group model.Group
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(selectFields) > 0 {
			if err := tx.Model(&model.Group{}).Where("id = ?", id).Select(selectFields).Updates(&updates).Error; err != nil {
				return fmt.Errorf("failed to update group: %w", err)
			}
		}
		if req.Items != nil {
			if err := syncGroupItems(tx, id, *req.Items); err != nil {
				return err
			}
		}
		if err := tx.Preload("Items").First(&group, id).Error; err != nil {
			return fmt.Errorf("failed to load updated group: %w", err)
		}
		// 当前成员在成员集合定稿后才写入: syncGroupItems 会清空指向已删除成员的当前成员,
		// 先写会被它覆盖; 归属校验同样只对最终集合成立。
		if req.ActiveItemID != nil {
			if *req.ActiveItemID != 0 && !slices.ContainsFunc(group.Items, func(item model.GroupItem) bool { return item.ID == *req.ActiveItemID }) {
				return fmt.Errorf("group item not found")
			}
			if err := tx.Model(&model.Group{}).Where("id = ?", id).Update("active_item_id", *req.ActiveItemID).Error; err != nil {
				return fmt.Errorf("failed to update active item: %w", err)
			}
			group.ActiveItemID = *req.ActiveItemID
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sortGroupItems(group.Items)
	groupCache.Set(group.ID, group)
	groupNameIndex.Set(group.Name, group.ID)
	if oldName != group.Name {
		groupNameIndex.Del(oldName)
	}
	snapshot := groupSnapshot(group)
	return &snapshot, nil
}

// syncGroupItems 按提交的成员集合新增, 重排与删除分组成员。
// 成员按引用键 (渠道授权或子分组) 在分组内唯一, 该键作为匹配依据, 由此已有成员保留其主键:
// 主键被分组的当前成员和 Relay 的路由状态引用, 换主键会让人工选择与冷却记录失效。
// 优先级一律按提交顺序重写, 前端只需提交当前排列, 无需自行算出哪些成员的顺序发生了变化。
// 子分组引用在此做事务内校验: 引用存在、非本分组自引用、不成环且深度不超上限。
func syncGroupItems(tx *gorm.DB, groupID int, requested []model.GroupItemInput) error {
	for _, item := range requested {
		if err := model.ValidateGroupItemRef(item.ChannelGrantID, item.ChildGroupID); err != nil {
			return err
		}
	}
	if err := validateChildGroupRefs(tx, groupID, requested); err != nil {
		return err
	}

	var existing []model.GroupItem
	if err := tx.Where("group_id = ?", groupID).Find(&existing).Error; err != nil {
		return fmt.Errorf("failed to load group items: %w", err)
	}
	existingByRef := make(map[[2]int]model.GroupItem, len(existing))
	for _, item := range existing {
		existingByRef[[2]int{item.GrantRef(), item.ChildRef()}] = item
	}

	for priority, requestedItem := range requested {
		ref := [2]int{requestedItem.ChannelGrantID, requestedItem.ChildGroupID}
		current, ok := existingByRef[ref]
		if !ok {
			newItem := model.GroupItem{GroupID: groupID, ChannelGrantID: requestedItem.GrantRefPtr(), ChildGroupID: requestedItem.ChildRefPtr(), Priority: priority + 1, SmartTier: requestedItem.SmartTier}
			if err := tx.Create(&newItem).Error; err != nil {
				return fmt.Errorf("failed to create group item: %w", err)
			}
			continue
		}
		// 位置与档位分属两次改动, 合并成一次写入; 档位必须显式写空串才能清掉（Updates 的 map 形式不受零值忽略影响）。
		updates := map[string]any{}
		if current.Priority != priority+1 {
			updates["priority"] = priority + 1
		}
		if current.SmartTier != requestedItem.SmartTier {
			updates["smart_tier"] = requestedItem.SmartTier
		}
		if len(updates) > 0 {
			if err := tx.Model(&model.GroupItem{}).Where("id = ?", current.ID).
				Updates(updates).Error; err != nil {
				return fmt.Errorf("failed to update group item: %w", err)
			}
		}
		delete(existingByRef, ref)
	}

	deletedIDs := make([]int, 0, len(existingByRef))
	for _, item := range existingByRef {
		deletedIDs = append(deletedIDs, item.ID)
	}
	if len(deletedIDs) == 0 {
		return nil
	}
	// 被删掉的成员可能正是当前人工指定的成员, 需一并清空, 否则分组会指向一个已不存在的成员。
	if err := tx.Model(&model.Group{}).
		Where("id = ? AND active_item_id IN ?", groupID, deletedIDs).
		Update("active_item_id", 0).Error; err != nil {
		return fmt.Errorf("failed to clear active item: %w", err)
	}
	if err := tx.Delete(&model.GroupItem{}, deletedIDs).Error; err != nil {
		return fmt.Errorf("failed to delete group items: %w", err)
	}
	return nil
}

// validateChildGroupRefs 校验一批待写入的子分组引用: 目标存在、非自引用, 且连同库内既有子分组链
// 不成环、深度不超上限。校验按"提交后的最终形状"进行: 以 requested 视为本分组的全部成员,
// 既覆盖增量改动也覆盖整体替换, 避免只校验新增行而漏掉既有行构成的环。
func validateChildGroupRefs(tx *gorm.DB, groupID int, requested []model.GroupItemInput) error {
	// 引用存在性 + 非自引用: 子分组是另一条分组行, 指向自己会立即成环。
	for _, item := range requested {
		if item.ChildGroupID == 0 {
			continue
		}
		if item.ChildGroupID == groupID {
			return fmt.Errorf("group cannot reference itself as a child")
		}
		var count int64
		if err := tx.Model(&model.Group{}).Where("id = ?", item.ChildGroupID).Count(&count).Error; err != nil {
			return fmt.Errorf("failed to load child group %d: %w", item.ChildGroupID, err)
		}
		if count == 0 {
			return fmt.Errorf("child group %d not found", item.ChildGroupID)
		}
	}

	// 从每个被引用的子分组出发沿库内边走链, 出现回边到 groupID 即成环;
	// 深度按模型层上限截断, 超限同样拒绝。库内其余分组未受本次提交影响, 是校验的既有底座。
	depthByGroup := make(map[int]int)
	var walk func(childID, depth int) error
	walk = func(childID, depth int) error {
		if depth > model.GroupItemMaxDepth {
			return fmt.Errorf("group nesting exceeds max depth %d", model.GroupItemMaxDepth)
		}
		if childID == groupID {
			return fmt.Errorf("group nesting must not form a cycle")
		}
		// 剪枝只在"该节点此前已从更深 (或同深) 起点完整走过"时成立: 那次无错说明子树高度受
		// 更严的预算约束, 本次更浅的起点更不可能越限。反向剪枝 (浅起点先走、深起点后到)
		// 会把深链第二次到达时的超限判成"已验证", 恰好漏判菱形共享子树的超限链。
		// 记录取最深起点, 同一节点的记录深度单调加深且上界为 MaxDepth, 终止性不受影响。
		if seen, ok := depthByGroup[childID]; ok && seen >= depth {
			return nil
		}
		depthByGroup[childID] = depth
		var edges []model.GroupItem
		if err := tx.Where("group_id = ? AND child_group_id IS NOT NULL", childID).Find(&edges).Error; err != nil {
			return fmt.Errorf("failed to load child edges of group %d: %w", childID, err)
		}
		for _, edge := range edges {
			if err := walk(edge.ChildRef(), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, item := range requested {
		if item.ChildGroupID != 0 {
			if err := walk(item.ChildGroupID, 1); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateGroupTreeRefs 是新建分组场景的子分组引用校验: 分组行尚未落库, 事务校验退化为
// 引用存在性 (目标分组此刻不可能回指本分组, 自引用因无主键而无法表达)。
// conn 注入库句柄 (生产传全局库, 测试传直连内存库), 与 syncGroupItems 同款可测口径。
func validateGroupTreeRefs(conn *gorm.DB, items []model.GroupItem) error {
	requested := make([]model.GroupItemInput, len(items))
	for i, item := range items {
		requested[i] = model.GroupItemInput{ChannelGrantID: item.GrantRef(), ChildGroupID: item.ChildRef()}
	}
	return conn.Transaction(func(tx *gorm.DB) error {
		return validateChildGroupRefs(tx, 0, requested)
	})
}

// GroupDel 删除分组及其成员; 其他分组指向本分组的子分组成员一并移除,
// 它们随目标消失而失去语义, 留着只会让快照按悬空引用渲染。成员删除不会影响被其他分组引用的渠道授权。
func GroupDel(id int, ctx context.Context) error {
	group, ok := groupCache.Get(id)
	if !ok {
		return fmt.Errorf("group not found")
	}
	if err := groupDelOn(db.GetDB().WithContext(ctx), id); err != nil {
		return err
	}
	groupCache.Del(id)
	groupNameIndex.Del(group.Name)
	// 其他分组的成员集合因级联清理而变化, 整体刷新以让快照与库内一致。
	if err := groupRefreshCache(ctx); err != nil {
		return fmt.Errorf("failed to refresh groups: %w", err)
	}
	return nil
}

// groupDelOn 是 GroupDel 的事务本体, 收库句柄 (生产全局库 / 测试直连内存库),
// 与 quota 族同款可测口径: 级联清理悬空子分组引用的语义在此落库, 缓存处置由调用方负责。
func groupDelOn(conn *gorm.DB, id int) error {
	return conn.Transaction(func(tx *gorm.DB) error {
		// 先收集引用本分组的成员行, 清掉其所在分组的当前成员后一并删除。
		var refItemIDs []int
		if err := tx.Model(&model.GroupItem{}).Select("id").
			Where("child_group_id = ?", id).Pluck("id", &refItemIDs).Error; err != nil {
			return fmt.Errorf("failed to load child references of group %d: %w", id, err)
		}
		if len(refItemIDs) > 0 {
			if err := tx.Model(&model.Group{}).
				Where("active_item_id IN ?", refItemIDs).
				Update("active_item_id", 0).Error; err != nil {
				return fmt.Errorf("failed to clear active items referencing group %d: %w", id, err)
			}
			if err := tx.Delete(&model.GroupItem{}, refItemIDs).Error; err != nil {
				return fmt.Errorf("failed to delete child references of group %d: %w", id, err)
			}
		}
		if err := tx.Delete(&model.Group{}, id).Error; err != nil {
			return fmt.Errorf("failed to delete group: %w", err)
		}
		return nil
	})
}

// groupRefreshCache 从数据库刷新完整分组缓存和名称索引。
// 缓存只存库内行, 成员的名称与可用性在读取时由 groupSnapshot 现算: 它们随渠道与凭据变化,
// 存进缓存就得在每次渠道改动后跟着刷新一遍。
func groupRefreshCache(ctx context.Context) error {
	groups := []model.Group{}
	if err := db.GetDB().WithContext(ctx).
		Preload("Items").
		Find(&groups).Error; err != nil {
		return err
	}
	groupCache.Clear()
	groupNameIndex.Clear()
	for _, group := range groups {
		sortGroupItems(group.Items)
		groupCache.Set(group.ID, group)
		groupNameIndex.Set(group.Name, group.ID)
	}
	return nil
}

// FlattenGroupItems 把分组的嵌套成员深度优先展平为可转发的授权成员平面表,
// 子组成员按引用行位置 splice 进来, 引用行本身不进入结果。
// 读侧三线防御与写侧校验 (validateChildGroupRefs) 同口径: 路径回边剪除
// (覆盖脏缓存/手改库在写侧之后引入的环)、超过 GroupItemMaxDepth 截断、
// 同一成员行按 ID 去重 (菱形共享子组只并一次)。
// 冷却/探测/亲和的键是展平后具体成员行 ID (跨树唯一), 子分组不持有独立路由状态;
// 缺失的子分组引用直接跳过 (GroupDel 已在写侧级联, 残留只是脏缓存的瞬态)。
func FlattenGroupItems(group model.Group) []model.GroupItem {
	flat, _ := FlattenGroupItemsWithTopCounts(group)
	return flat
}

// FlattenGroupItemsWithTopCounts 与 FlattenGroupItems 完全同语义（同一个遍历实现），
// 额外返回**每个顶层成员**贡献的展平成员条数（与 group.Items 等长、按引用顺序）。
//
// 用途（T-smart-006）：智能路由的档位必须在**顶层成员**粒度上切分 —— 子分组是一条链，
// 若按展平后的成员条数对半切，链会被从中间切开（例如决策链 1 个成员 + 执行链 2 个成员时，
// 复杂请求会把执行链的第一个成员算进决策档）。展平成员在遍历顺序上按顶层成员分段且连续，
// 所以「前 k 个顶层成员贡献的条数之和」就是切分点在平面表里的下标。
func FlattenGroupItemsWithTopCounts(group model.Group) ([]model.GroupItem, []int) {
	seen := make(map[int]struct{})
	out := make([]model.GroupItem, 0, len(group.Items))
	counts := make([]int, 0, len(group.Items))
	for _, item := range group.Items {
		before := len(out)
		flattenInto(model.Group{ID: group.ID, Items: []model.GroupItem{item}}, 0, []int{group.ID}, seen, &out)
		counts = append(counts, len(out)-before)
	}
	return out, counts
}

func flattenInto(group model.Group, depth int, path []int, seen map[int]struct{}, out *[]model.GroupItem) {
	flattenIntoWithTier(group, depth, path, seen, out, model.GroupSmartTierAuto)
}

// flattenIntoWithTier 是展平的实际实现；tier 是外层成员声明的智能路由档位，用来下传给整条链。
//
// 档位是**顶层成员粒度**的（子分组整条链归档），所以一个被标记为决策引擎档的子分组，
// 它内部的每一条成员也必须带上同一个档位 —— 否则展平之后档位就丢在子分组那一层，
// 标在子分组上等于没标。内层自己声明的档位优先于外层下传的值（就近原则）。
func flattenIntoWithTier(group model.Group, depth int, path []int, seen map[int]struct{}, out *[]model.GroupItem, tier string) {
	if depth > model.GroupItemMaxDepth {
		return
	}
	for _, item := range group.Items {
		inherited := tier
		if item.SmartTier != model.GroupSmartTierAuto {
			inherited = item.SmartTier
		}
		childID := item.ChildRef()
		if childID == 0 {
			if _, dup := seen[item.ID]; dup {
				continue
			}
			seen[item.ID] = struct{}{}
			item.SmartTier = inherited
			*out = append(*out, item)
			continue
		}
		child, ok := groupCache.Get(childID)
		if !ok {
			continue
		}
		if slices.Contains(path, childID) {
			continue
		}
		flattenIntoWithTier(child, depth+1, append(path, childID), seen, out, inherited)
	}
}

// sortGroupItems 按优先级和主键生成稳定的成员顺序。
func sortGroupItems(items []model.GroupItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Priority != items[j].Priority {
			return items[i].Priority < items[j].Priority
		}
		return items[i].ID < items[j].ID
	})
}

// groupSnapshot 为成员补齐授权两侧的名称, 所属渠道与可用性。
// 可用性在此一次定稿: 渠道与凭据均启用且模型, 凭据均存在时可转发, 否则仍列出该成员但标记不可用,
// 由此界面无需再按渠道列表回查, 也不会出现前后端各判一套的分歧。
//
// 子分组成员的 Available 由展平后代回填 (T-group-002): 子树里只要还有一个可转发授权,
// 该引用行即视为可用——选路展开后它能产生目标; 空树/全禁用的子树恒为假, 与"悬空引用
// 跳过"的转发语义对称 (GroupDel 已在写侧级联, 残留只剩脏缓存/手改库)。
func groupSnapshot(group model.Group) model.Group {
	// 成员恒为数组: 读取侧承诺该字段不为 null, 空分组也要给出空数组。
	group.Items = append(make([]model.GroupItem, 0, len(group.Items)), group.Items...)
	for i := range group.Items {
		if group.Items[i].ChildRef() != 0 {
			// 子分组成员: 名称取目标分组名。可用性按展平后代回填:
			// 读的是与转发同一份缓存底座, 与选路行为同一口径。
			if child, ok := groupCache.Get(group.Items[i].ChildRef()); ok {
				group.Items[i].ChildGroupName = child.Name
				for _, leaf := range FlattenGroupItems(child) {
					grant, ok := channelGrantCache.Get(leaf.GrantRef())
					if !ok {
						continue
					}
					channelModel, modelOK := channelModelCache.Get(grant.ChannelModelID)
					channelKey, keyOK := channelKeyCache.Get(grant.ChannelKeyID)
					if !modelOK || !keyOK {
						continue
					}
					channel, channelOK := channelCache.Get(channelModel.ChannelID)
					if !channelOK {
						continue
					}
					if channel.Enabled && channelKey.Enabled {
						group.Items[i].Available = true
						break
					}
				}
			}
			continue
		}
		grant, ok := channelGrantCache.Get(group.Items[i].GrantRef())
		if !ok {
			continue
		}
		group.Items[i].Protocols = grant.Protocols
		channelModel, modelOK := channelModelCache.Get(grant.ChannelModelID)
		channelKey, keyOK := channelKeyCache.Get(grant.ChannelKeyID)
		if !modelOK || !keyOK {
			continue
		}
		group.Items[i].ChannelID = channelModel.ChannelID
		group.Items[i].ModelName = channelModel.Name
		group.Items[i].KeyName = channelKey.Name
		channel, channelOK := channelCache.Get(channelModel.ChannelID)
		if !channelOK {
			continue
		}
		group.Items[i].ChannelName = channel.Name
		group.Items[i].Available = channel.Enabled && channelKey.Enabled
	}
	return group
}
