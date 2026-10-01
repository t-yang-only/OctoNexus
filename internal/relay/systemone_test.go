package relay

import (
	"strings"
	"testing"
)

// T-verify-002 TypeSafe System One 兼容转发的判据。
//
// 这个转发器绕开了协议转换层（System One 的 body 形态与三家标准协议都不同），
// 所以这里能测的是它自己那部分逻辑：URL 归一化、错误形状、工具函数。
// 端到端的可用性由活体测试覆盖（需要真实 key）。

// URL 归一化：用户填带 /v1 或不带 /v1 都要拼成同一个地址。
//
// 反例：不归一化会拼出 https://api.typesafe.ai/v1/v1/systemone，
// 上游只回一句 "Not Found"，排查时要绕一圈才想到是路径重复。
func TestSystemOneURLNormalizesBase(t *testing.T) {
	cases := map[string]string{
		"https://api.typesafe.ai":        "https://api.typesafe.ai/v1/systemone",
		"https://api.typesafe.ai/":       "https://api.typesafe.ai/v1/systemone",
		"https://api.typesafe.ai/v1":     "https://api.typesafe.ai/v1/systemone",
		"https://api.typesafe.ai/v1/":    "https://api.typesafe.ai/v1/systemone",
		"  https://api.typesafe.ai/v1  ": "https://api.typesafe.ai/v1/systemone",
		// 带路径前缀的自建反代也要拼对
		"https://gw.example.com/ts":    "https://gw.example.com/ts/v1/systemone",
		"https://gw.example.com/ts/v1": "https://gw.example.com/ts/v1/systemone",
	}
	for input, want := range cases {
		if got := systemOneURL(input); got != want {
			t.Fatalf("systemOneURL(%q) = %q, want %q", input, got, want)
		}
	}
}

// 归一化后绝不出现重复的 /v1/v1 —— 这是上面那条的具体化，单独钉住。
func TestSystemOneURLNeverDoublesV1(t *testing.T) {
	for _, base := range []string{
		"https://api.typesafe.ai",
		"https://api.typesafe.ai/v1",
		"https://api.typesafe.ai/v1/",
	} {
		got := systemOneURL(base)
		if len(got) > 0 && strings.Contains(got, "/v1/v1") {
			t.Fatalf("拼出了重复的 /v1：%q", got)
		}
	}
}

// 截断只影响超长文本，短文本原样返回。
func TestTruncateText(t *testing.T) {
	short := "upstream 400: bad request"
	if got := truncateText(short, 500); got != short {
		t.Fatalf("短文本不该被改动：%q", got)
	}
	long := ""
	for i := 0; i < 200; i++ {
		long += "0123456789"
	}
	got := truncateText(long, 500)
	if len(got) <= 500 {
		t.Fatalf("截断后应保留前 500 字符再加标记，实得 %d", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("截断应留下可见标记，实得尾部：%q", got[len(got)-20:])
	}
}

// 白名单包含判断。
func TestContainsString(t *testing.T) {
	items := []string{"a", "b", "c"}
	if !containsString(items, "b") {
		t.Fatalf("应包含 b")
	}
	if containsString(items, "d") {
		t.Fatalf("不该包含 d")
	}
	if containsString(nil, "a") {
		t.Fatalf("空切片不该包含任何值")
	}
}
