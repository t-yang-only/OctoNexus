package op

import (
	"context"
	"fmt"
	"time"

	"github.com/bestruirui/octopus/internal/db"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// quotaDB 是停用/恢复/审计可注入的数据库抽象：*gorm.DB 天然满足，测试传入直连内存库隔离。
type quotaDB interface {
	Model(value any) *gorm.DB
	Create(value any) *gorm.DB
	Where(query any, args ...any) *gorm.DB
}

// quotaConnOrDefault 为空时走全局库，测试可传入直连内存库隔离。
func quotaConnOrDefault(conn quotaDB) quotaDB {
	if conn != nil {
		return conn
	}
	return dbConnAdapter{}
}

// dbConnAdapter 把全局库适配到 quotaDB（Model/Create/Where 三件套 *gorm.DB 本就满足）。
type dbConnAdapter struct{}

func (dbConnAdapter) Model(value any) *gorm.DB  { return db.GetDB().Model(value) }
func (dbConnAdapter) Create(value any) *gorm.DB { return db.GetDB().Create(value) }
func (dbConnAdapter) Where(query any, args ...any) *gorm.DB {
	return db.GetDB().Where(query, args...)
}

// quotaKeyStore 是停用/恢复可注入的凭据读写抽象：生产走缓存+全局库，测试走直连内存库。
type quotaKeyStore interface {
	get(channelID int) (model.Channel, bool)
	keysOf(channelID int) []model.ChannelKey
	setEnabled(keyID int, enabled bool)
}

// cacheQuotaKeyStore 是生产实现：读缓存，写库+回写缓存。
type cacheQuotaKeyStore struct{}

func (cacheQuotaKeyStore) get(channelID int) (model.Channel, bool) {
	return channelCache.Get(channelID)
}

func (cacheQuotaKeyStore) keysOf(channelID int) []model.ChannelKey {
	keys := make([]model.ChannelKey, 0)
	for _, k := range channelKeyCache.GetAll() {
		if k.ChannelID == channelID && k.Enabled {
			keys = append(keys, k)
		}
	}
	return keys
}

func (cacheQuotaKeyStore) setEnabled(keyID int, enabled bool) {
	if k, ok := channelKeyCache.Get(keyID); ok {
		k.Enabled = enabled
		channelKeyCache.Set(keyID, k)
	}
}

// directQuotaKeyStore 是测试实现：直连内存库读写，不碰生产缓存。
type directQuotaKeyStore struct {
	conn *gorm.DB
}

func (s directQuotaKeyStore) get(channelID int) (model.Channel, bool) {
	var channel model.Channel
	if err := s.conn.First(&channel, channelID).Error; err != nil {
		return model.Channel{}, false
	}
	return channel, true
}

func (s directQuotaKeyStore) keysOf(channelID int) []model.ChannelKey {
	var keys []model.ChannelKey
	if err := s.conn.Where("channel_id = ? AND enabled = ?", channelID, true).Find(&keys).Error; err != nil {
		return nil
	}
	return keys
}

func (s directQuotaKeyStore) setEnabled(keyID int, enabled bool) {
	_ = s.conn.Model(&model.ChannelKey{}).Where("id = ?", keyID).Update("enabled", enabled).Error
}

// QuotaZeroStop 余额归零自动停用：剩余额度 <= 0 时停用该渠道下全部启用中的凭据。
// 停用粒度为凭据级（ChannelKey.Enabled=false），同渠道多凭据互不牵连，渠道整行不动，
// 授权行不删；转发侧经 ChannelGrantGet（凭据停用即不可转发）与候选/分组可用性口径自然生效。
// 幂等：全部已停用时仍落一条审计（note=already_stopped），调用方可直接重试无需前置检查。
// actor 固定为 system:auto（自动停用无人工操作人）；手动恢复走 QuotaManualRestore。
func QuotaZeroStop(channelID int, remaining float64) (stopped []int, err error) {
	return quotaZeroStopOn(nil, channelID, remaining)
}

func quotaZeroStopOn(conn *gorm.DB, channelID int, remaining float64) ([]int, error) {
	if remaining > model.QuotaZeroThreshold {
		return nil, fmt.Errorf("remaining %v above zero threshold", remaining)
	}
	var store quotaKeyStore = cacheQuotaKeyStore{}
	var audit quotaDB
	if conn != nil {
		store = directQuotaKeyStore{conn: conn}
		audit = conn
	}
	channel, ok := store.get(channelID)
	if !ok {
		return nil, fmt.Errorf("channel not found")
	}
	keys := store.keysOf(channelID)
	if len(keys) == 0 {
		quotaAuditOn(audit, channelID, 0, model.QuotaActionAutoStop, remaining, "system:auto", "already_stopped")
		return nil, nil
	}
	keyIDs := make([]int, 0, len(keys))
	for _, k := range keys {
		keyIDs = append(keyIDs, k.ID)
	}
	if conn != nil {
		if err := conn.Model(&model.ChannelKey{}).Where("id IN ?", keyIDs).Update("enabled", false).Error; err != nil {
			return nil, fmt.Errorf("failed to disable channel keys: %w", err)
		}
		for _, k := range keys {
			store.setEnabled(k.ID, false)
		}
	} else {
		if err := db.GetDB().Model(&model.ChannelKey{}).Where("id IN ?", keyIDs).Update("enabled", false).Error; err != nil {
			return nil, fmt.Errorf("failed to disable channel keys: %w", err)
		}
		for _, k := range keys {
			store.setEnabled(k.ID, false)
		}
	}
	quotaAuditOn(audit, channel.ID, keys[0].ID, model.QuotaActionAutoStop, remaining, "system:auto", fmt.Sprintf("stopped %d keys", len(keys)))
	return keyIDs, nil
}

// QuotaManualRestore 手动恢复：重新启用单个凭据。不自动恢复（避免归零抖动反复开关）。
// actor 由调用方传入（管理接口取当前用户）；恢复本身记审计；已启用时为 no-op 但仍记审计。
func QuotaManualRestore(channelKeyID int, actor string) error {
	return quotaManualRestoreOn(nil, channelKeyID, actor)
}

func quotaManualRestoreOn(conn *gorm.DB, channelKeyID int, actor string) error {
	var key model.ChannelKey
	var channelID int
	if conn != nil {
		if err := conn.First(&key, channelKeyID).Error; err != nil {
			return fmt.Errorf("channel key not found")
		}
		channelID = key.ChannelID
		if !key.Enabled {
			if err := conn.Model(&model.ChannelKey{}).Where("id = ?", channelKeyID).Update("enabled", true).Error; err != nil {
				return fmt.Errorf("failed to restore channel key: %w", err)
			}
		}
		quotaAuditOn(conn, channelID, channelKeyID, model.QuotaActionManualRestore, 0, actor, "manual restore")
		return nil
	}
	cached, ok := channelKeyCache.Get(channelKeyID)
	if !ok {
		return fmt.Errorf("channel key not found")
	}
	if !cached.Enabled {
		if err := db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", channelKeyID).Update("enabled", true).Error; err != nil {
			return fmt.Errorf("failed to restore channel key: %w", err)
		}
		cached.Enabled = true
		channelKeyCache.Set(channelKeyID, cached)
	}
	quotaAuditOn(nil, cached.ChannelID, channelKeyID, model.QuotaActionManualRestore, 0, actor, "manual restore")
	return nil
}

// QuotaActions 按渠道倒序分页查询停用/恢复审计。limit<=0 取默认 50，上限 QuotaActionPageMaxLimit。
func QuotaActions(channelID, limit, offset int) ([]model.QuotaAction, int64) {
	return quotaActionsOn(nil, channelID, limit, offset)
}

func quotaActionsOn(conn *gorm.DB, channelID, limit, offset int) ([]model.QuotaAction, int64) {
	if limit <= 0 {
		limit = 50
	}
	if limit > model.QuotaActionPageMaxLimit {
		limit = model.QuotaActionPageMaxLimit
	}
	if offset < 0 {
		offset = 0
	}
	var query *gorm.DB
	if conn != nil {
		query = conn.Model(&model.QuotaAction{})
	} else {
		query = db.GetDB().Model(&model.QuotaAction{})
	}
	if channelID > 0 {
		query = query.Where("channel_id = ?", channelID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0
	}
	var actions []model.QuotaAction
	if err := query.Order("id DESC").Limit(limit).Offset(offset).Find(&actions).Error; err != nil {
		return nil, total
	}
	return actions, total
}

// quotaAuditOn 落库一条停用/恢复审计；失败只记备忘，不阻断停用/恢复本身。
func quotaAuditOn(conn quotaDB, channelID, keyID int, action string, remaining float64, actor, note string) {
	entry := model.QuotaAction{
		ChannelID:    channelID,
		ChannelKeyID: keyID,
		Action:       action,
		Remaining:    remaining,
		Actor:        actor,
		Note:         note,
		CreatedAt:    time.Now(),
	}
	_ = quotaConnOrDefault(conn).Create(&entry).Error
}

// quotaRefreshChannelKeys 重新加载单个渠道的凭据缓存（停用/恢复后调用）。
// 存活行保留缓存中尚未落库的统计，沿 reloadChannelChildren 同一口径。
func quotaRefreshChannelKeys(ctx context.Context, channelID int) error {
	return reloadChannelChildren(ctx, channelID)
}
