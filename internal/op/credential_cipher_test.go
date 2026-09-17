package op

import (
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/secret"
)

// 本文件覆盖 R-sec-001 余项的 op 侧行为：凭据写入即密文、读回即明文、重复提交不重写。

func useCredentialTestKey(t *testing.T) {
	t.Helper()
	t.Setenv("OCTOPUS_OFFICIAL_KEY", "op-credential-test-key")
	secret.ResetKey()
	t.Cleanup(secret.ResetKey)
}

func TestSyncChannelKeysStoresCiphertext(t *testing.T) {
	useCredentialTestKey(t)
	conn := openAutoGroupTestDB(t)
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "cipher-channel"}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	if err := syncChannelKeys(conn, channel.ID, []model.ChannelKeyInput{
		{Name: "k1", Key: "sk-cipher-one"},
	}); err != nil {
		t.Fatalf("sync keys: %v", err)
	}

	var stored model.ChannelKey
	if err := conn.Where("channel_id = ?", channel.ID).First(&stored).Error; err != nil {
		t.Fatalf("load stored key: %v", err)
	}
	if !secret.IsSealed(stored.Key) {
		t.Fatalf("落库的凭据不是密文: %q", stored.Key)
	}
	if strings.Contains(stored.Key, "sk-cipher-one") {
		t.Fatalf("落库的密文里出现了明文")
	}

	// 读回即明文：装载侧的 DecryptChannelKeyRows 是唯一的解密点。
	rows := []model.ChannelKey{stored}
	if err := DecryptChannelKeyRows(rows); err != nil {
		t.Fatalf("decrypt rows: %v", err)
	}
	if rows[0].Key != "sk-cipher-one" {
		t.Fatalf("解密结果 = %q, 期望 sk-cipher-one", rows[0].Key)
	}
}

func TestSyncChannelKeysDoesNotRewriteUnchangedKey(t *testing.T) {
	useCredentialTestKey(t)
	conn := openAutoGroupTestDB(t)
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "idempotent-channel"}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	input := []model.ChannelKeyInput{{Name: "k1", Key: "sk-unchanged"}}
	if err := syncChannelKeys(conn, channel.ID, input); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	var first model.ChannelKey
	if err := conn.Where("channel_id = ?", channel.ID).First(&first).Error; err != nil {
		t.Fatalf("load first: %v", err)
	}

	// 同样的明文再提交一次：比对必须发生在明文上，否则"值没变"会被判成变了，
	// 每次保存都重新加密一次（密文变化 → 白写一次库，也让"到底改没改"无从判断）。
	if err := syncChannelKeys(conn, channel.ID, input); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	var second model.ChannelKey
	if err := conn.Where("channel_id = ?", channel.ID).First(&second).Error; err != nil {
		t.Fatalf("load second: %v", err)
	}
	if second.Key != first.Key {
		t.Fatalf("重复提交同一明文触发了重新加密（密文变了）")
	}
}

func TestSyncChannelKeysReEncryptsChangedKey(t *testing.T) {
	useCredentialTestKey(t)
	conn := openAutoGroupTestDB(t)
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "rotate-channel"}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := syncChannelKeys(conn, channel.ID, []model.ChannelKeyInput{{Name: "k1", Key: "sk-old"}}); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	if err := syncChannelKeys(conn, channel.ID, []model.ChannelKeyInput{{Name: "k1", Key: "sk-new"}}); err != nil {
		t.Fatalf("rotate sync: %v", err)
	}
	var stored model.ChannelKey
	if err := conn.Where("channel_id = ?", channel.ID).First(&stored).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	rows := []model.ChannelKey{stored}
	if err := DecryptChannelKeyRows(rows); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if rows[0].Key != "sk-new" {
		t.Fatalf("换过之后解密结果 = %q, 期望 sk-new", rows[0].Key)
	}
}
