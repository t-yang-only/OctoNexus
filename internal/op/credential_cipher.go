package op

import (
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/secret"
	"github.com/charmbracelet/log"
	"sync"
)

// 渠道凭据静态加密的 op 侧入口（R-sec-001 余项）：库内密文、进程内明文。
//
// 加解密原语与密钥解析在 internal/secret（migrate 也要用它，而 migrate 不能依赖 op），
// 这里只留「读写两侧各自收口在哪」这一层业务约定：
//   - 写入侧（syncChannelKeys、官方账号物化）：落库前调用 sealChannelKeyForStore；
//   - 读取侧（channelRefreshCache、reloadChannelChildren、officialPoolKeys）：读出来调用 DecryptChannelKeyRows。
var credentialWarnOnce sync.Once

// sealChannelKeyForStore 是写入侧的宽松封装：没有可用密钥时退回明文并只告警一次。
//
// 为什么允许退回明文：加密密钥在存量安装上可能确实不可用（既没设环境变量、也写不了密钥文件），
// 此时「拒绝保存凭据」会把一个加密能力的缺失升级成功能不可用；而明文正是这些安装当前的状态，
// 退回它不引入新的暴露面（代价是这类安装得不到静态加密的收益，日志里说清楚）。
func sealChannelKeyForStore(plain string) string {
	if plain == "" || secret.IsSealed(plain) {
		return plain
	}
	sealed, err := secret.Seal(plain)
	if err == nil {
		return sealed
	}
	credentialWarnOnce.Do(func() {
		log.Warnf("凭据加密不可用（%v）：channel_keys.key 仍以明文落库。设置 OCTOPUS_OFFICIAL_KEY 或允许在数据目录写入 %s 即可启用静态加密",
			err, secret.KeyFileName)
	})
	return plain
}

// DecryptChannelKeyRows 就地解密一批凭据行；解不开就报错（绝不把密文当明文放进缓存）。
func DecryptChannelKeyRows(rows []model.ChannelKey) error {
	return secret.DecryptRows(rows)
}

// CredentialEncryptionEnabled 报告凭据加密是否可用（启动日志用）。
func CredentialEncryptionEnabled() bool {
	return secret.EncryptionEnabled()
}

// CredentialKeySource 返回密钥来源（环境变量名或密钥文件路径），未配置时为空串。
func CredentialKeySource() string {
	return secret.KeySource()
}
