package migrate

import (
	"fmt"

	"github.com/bestruirui/octopus/internal/secret"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 15,
		Up:      migrateChannelKeyEncryption,
	})
}

// migrateChannelKeyEncryption 把存量的明文渠道凭据改成密文（R-sec-001 余项 / T-sec-001）。
//
// 背景：channel_keys.key 一直是明文落库 —— 数据库文件或备份被拿走，上游凭据就跟着泄露。
// 官方账号与号池规格早已是 AES-256-GCM 密文（op/official_account.go、op/pool_adapter.go），
// 只剩渠道凭据这一条明文路径。本轮把读写两侧都收口到 op/credential_cipher.go：
// 库里密文、进程内明文，带前缀的值是密文、没有前缀的值按明文处理。
//
// 口径：
//   - 只处理没有前缀的行（幂等：跑第二次不会二次加密，也不会把已加密的行弄坏）。
//   - 没有可用密钥（环境变量与密钥文件都没有、且密钥文件写不出来）时**整次跳过**：
//     这些行本来就是明文，跳过不会造成新的暴露；把启动卡死在迁移上才是真的把服务弄挂。
//     密钥一旦可用（设了 OCTOPUS_OFFICIAL_KEY，或让它能写数据目录），下一次启动就会补上。
//   - 只 UPDATE 行、不碰表结构：与 009 的教训一致。
//
// 回退注意：加密后的行只有新版本能读。老版本二进制会把密文当 API key 发给上游（表现为 401），
// 因此降级前需要先把凭据改回明文（面板重新填写即可）。这一点写在迁移日志里，不靠人记。
func migrateChannelKeyEncryption(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channel_keys") {
		return nil
	}
	total, err := channelKeyRowCount(db)
	if err != nil {
		return err
	}
	if total == 0 {
		return nil
	}
	if !secret.EncryptionEnabled() {
		log.Warnf("渠道凭据加密跳过：没有可用的加密密钥（%d 行仍是明文）。设置 OCTOPUS_OFFICIAL_KEY 或允许在数据目录写入 %s 即可启用", total, secret.KeyFileName)
		return nil
	}
	sealed, err := secret.SealLegacyChannelKeys(db)
	if err != nil {
		return fmt.Errorf("failed to encrypt channel keys: %w", err)
	}
	if sealed > 0 {
		log.Infof("渠道凭据加密：%d/%d 行明文已转为密文（密钥来源：%s）", sealed, total, secret.KeySource())
	}
	return nil
}

func channelKeyRowCount(db *gorm.DB) (int64, error) {
	var count int64
	if err := db.Table("channel_keys").Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed to count channel keys: %w", err)
	}
	return count, nil
}
