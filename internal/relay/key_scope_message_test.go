package relay

// keyScopeRejectMessage 的判据。
//
// # 要防住的事
//
//  1. **只说"不支持"而不说"支持哪些"** —— 客户端拿到 400 也不知道该把 model
//     改成什么。线上实测有 3 条这样的失败，用户唯一的办法是去面板翻 Key 的设置。
//  2. **清单过长把响应体撑爆** —— 白名单可能配了几十个模型，全列会让错误响应
//     比正常响应还大；截断后必须说明"还有"，否则用户以为只允许这几个。
//  3. **原话前缀丢失** —— 既有客户端可能按这句话匹配，改文案不能把它抹掉。

import (
	"strings"
	"testing"
)

func TestKeyScopeRejectMessageListsAllowedModels(t *testing.T) {
	msg := keyScopeRejectMessage([]string{"allowed-a", "allowed-b"})
	if !strings.HasPrefix(msg, "model not supported by this api key") {
		t.Errorf("应保留原话作前缀，实得 %q", msg)
	}
	if !strings.Contains(msg, "allowed-a") || !strings.Contains(msg, "allowed-b") {
		t.Errorf("应列出全部允许模型，实得 %q", msg)
	}
}

// 清单超过上限时必须截断，并说明总数（否则用户以为只允许列出的那几个）。
func TestKeyScopeRejectMessageTruncatesLongList(t *testing.T) {
	many := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		many = append(many, string(rune('a'+i)))
	}
	msg := keyScopeRejectMessage(many)

	// 前 8 个必须出现
	for _, name := range many[:8] {
		if !strings.Contains(msg, name) {
			t.Errorf("前 8 个应全部列出，缺 %q（%q）", name, msg)
			break
		}
	}
	// 第 9 个及以后不应出现（已截断）
	if strings.Contains(msg, many[9]) {
		t.Errorf("第 10 个及以后不该出现（已截断），实得 %q", msg)
	}
	// 必须说明总数
	if !strings.Contains(msg, "20") {
		t.Errorf("截断后应说明总数 20，实得 %q", msg)
	}
	// 原话前缀仍在
	if !strings.HasPrefix(msg, "model not supported by this api key") {
		t.Errorf("截断也不该丢原话前缀，实得 %q", msg)
	}
}

// 单个模型的清单不该被截断逻辑影响。
func TestKeyScopeRejectMessageSingleModel(t *testing.T) {
	msg := keyScopeRejectMessage([]string{"only-one"})
	if !strings.Contains(msg, "only-one") {
		t.Errorf("应列出唯一允许的模型，实得 %q", msg)
	}
	if !strings.HasPrefix(msg, "model not supported by this api key") {
		t.Errorf("应保留原话前缀，实得 %q", msg)
	}
}
