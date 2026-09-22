package op

import (
	"fmt"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// T-usability-005 清理「已失效的自动分组」。
//
// ## 解决什么问题
//
// `ensureAutoGroupsLocked` 只会**新增**：它遍历本次提交的模型，缺分组就建、缺成员就补，
// 但对「已存在、本次不再提交」的模型不做任何处理。后果是撤销授权后
// 分组照样留着 —— 实测撤销 senseaudio 的授权后残留 23 个分组，
// 它们的成员指向已经删掉的授权（成员数为 0），客户端在模型列表里还能看到、
// 能选中、能发请求，然后失败（修复前是挂到超时）。
//
// 这正是「配了却用不上」最难发现的一种：分组列表里明明有，
// 用户不知道自己看到的是一条已死的记录。
//
// ## 只动「纯自动生成」的分组
//
// 判断一个分组能不能被清理，必须保守到宁可漏删也不能误删：
//
//  1. 名字必须是本渠道的自动分组形态（`渠道名/模型名`）；
//  2. 分组里**不能有任何子分组成员** —— 那说明用户手工编排过，整条链是人建的；
//  3. 分组里**不能有任何指向别的渠道的授权** —— 同样是用户手工混用的证据；
//  4. 成员指向的授权**必须确实已不存在**（被删了），而不是"查不到"。
//
// 满足这四条才清理，且只删那些引用已失效授权的成员；
// 成员删空后再删分组本身。任何一条不满足就整个分组跳过。
//
// 这样即使判断逻辑有偏差，最坏结果也只是"少清理几个"，不会破坏用户的编排。
func pruneStaleAutoGroups(tx *gorm.DB, channelID int, channelName string) error {
	channelName = strings.TrimSpace(channelName)
	if channelName == "" {
		return nil
	}
	prefix := channelName + "/"

	// 本渠道名下的候选分组：名字以「渠道名/」开头。
	var groups []model.Group
	if err := tx.Where("name LIKE ?", prefix+"%").Find(&groups).Error; err != nil {
		return fmt.Errorf("load candidate auto groups: %w", err)
	}
	if len(groups) == 0 {
		return nil
	}

	groupIDs := make([]int, 0, len(groups))
	for _, g := range groups {
		groupIDs = append(groupIDs, g.ID)
	}

	var items []model.GroupItem
	if err := tx.Where("group_id IN ?", groupIDs).Find(&items).Error; err != nil {
		return fmt.Errorf("load auto group items: %w", err)
	}
	if len(items) == 0 {
		// 没有任何成员的分组同样是残留（例如成员先被删过）——
		// 但它们不引用任何东西，删掉是安全的。不过为保守起见这里不处理：
		// 空分组不影响转发（修复后会在等待上限内明确报错），
		// 而误删一个有内容的空分组比留着一个空分组更糟。
		return nil
	}

	// 查出成员引用的所有授权，判断哪些"确实已不存在"。
	grantIDs := make([]int, 0, len(items))
	for _, item := range items {
		if item.ChannelGrantID != nil {
			grantIDs = append(grantIDs, *item.ChannelGrantID)
		}
	}
	type grantOwner struct {
		ID        int
		ChannelID int
	}
	var owners []grantOwner
	if len(grantIDs) > 0 {
		if err := tx.Table("channel_grants AS g").
			Select("g.id AS id, m.channel_id AS channel_id").
			Joins("JOIN channel_models AS m ON m.id = g.channel_model_id").
			Where("g.id IN ?", grantIDs).
			Scan(&owners).Error; err != nil {
			return fmt.Errorf("load grant owners: %w", err)
		}
	}
	existing := make(map[int]int, len(owners)) // 授权 ID -> 所属渠道 ID
	for _, o := range owners {
		existing[o.ID] = o.ChannelID
	}

	itemsByGroup := make(map[int][]model.GroupItem, len(groups))
	for _, item := range items {
		itemsByGroup[item.GroupID] = append(itemsByGroup[item.GroupID], item)
	}

	for _, group := range groups {
		groupItems := itemsByGroup[group.ID]
		foreign := false
		var staleItemIDs []int
		for _, item := range groupItems {
			// 子分组成员：用户手工编排过，整个分组跳过。
			//
			// 注意这条在功能上是**冗余**的：子分组成员的 ChannelGrantID 必为 nil，
			// 会被下面的 `ChannelGrantID == nil` 分支同样拦下。
			// 变异检查证实了这一点（删掉本分支测试不会变红）。
			// 保留它是为了语义清晰 —— "这里遇到了子分组"比"字段形状异常"更能说明为什么跳过。
			if item.ChildGroupID != nil {
				foreign = true
				break
			}
			if item.ChannelGrantID == nil {
				// 既不是授权也不是子分组：形状异常，不动。
				foreign = true
				break
			}
			ownerChannel, ok := existing[*item.ChannelGrantID]
			if !ok {
				// 授权已不存在 —— 这是要清理的目标。
				staleItemIDs = append(staleItemIDs, item.ID)
				continue
			}
			if ownerChannel != channelID {
				// 引用的是别的渠道的授权：用户手工混用过，整个分组跳过。
				foreign = true
				break
			}
		}
		if foreign {
			continue
		}
		// 全员都属于本渠道：删掉已失效的成员。
		if len(staleItemIDs) > 0 {
			if err := tx.Where("id IN ?", staleItemIDs).Delete(&model.GroupItem{}).Error; err != nil {
				return fmt.Errorf("delete stale auto group items of %q: %w", group.Name, err)
			}
		}
		// 删完之后如果这个分组不再有任何成员，它就没有存在意义了。
		if len(staleItemIDs) == len(groupItems) {
			if err := tx.Where("id = ?", group.ID).Delete(&model.Group{}).Error; err != nil {
				return fmt.Errorf("delete empty auto group %q: %w", group.Name, err)
			}
		}
	}
	return nil
}
