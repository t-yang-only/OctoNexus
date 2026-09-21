package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// R-balance-002：上游余额读取要按**协议族**依次探测。
//
// 为什么必须这样：实测 15 家站点里只有 3 家能用 sk- Key 读到余额，而它们各走一种接口
// （apikey.fan 的 /user/balance、openagents 的 /usage、new-api 系的 OpenAI Billing 兼容口径）。
// 只认 new-api 的 /api/user/self 会把本来读得到的站点一起记成"未读到"——这正是用户看到的
// 「未读到余额渠道 16」里一部分的真实原因。
func TestParseGenericBalance(t *testing.T) {
	cases := []struct {
		name string
		body string
		want float64
		ok   bool
	}{
		{"apikey.fan 账号级余额", `{"balance":32.53154022,"isValid":true,"remaining":32.53154022,"unit":"USD"}`, 32.53154022, true},
		// 判据不只"读到了"：这家的 balance 是美元，不能被点口径抢走（实测这里错过一次，
		// 32.53 美元被当成 32.53 点再除 500000，面板显示 6.5e-05）。
		{"apikey.fan 金额优先于点口径", `{"balance":32.53154022,"remaining":32.53154022}`, 32.53154022, true},
		{"DeepSeek 官方 balance_infos", `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"12.34"}]}`, 12.34, true},
		// new-api 的 quota 是"点"，折算成货币在 op 层按 balance_points_per_unit 做；
		// 这里只断言"读到了数"——把点当美元宣传才是真缺陷。
		{"new-api quota 点口径", `{"data":{"quota":16126570,"used_quota":0}}`, 16126570, true},
		{"HTML 回退页", `<html><body>not found</body></html>`, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			read, ok := parseGenericBalance([]byte(tc.body))
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if diff := read.Remaining - tc.want; diff > 0.0001 || diff < -0.0001 {
				t.Fatalf("remaining = %v, want %v", read.Remaining, tc.want)
			}
		})
	}

	// 单位口径单独钉住：钱的名字（balance/remaining）必须被标成货币，点口径（quota）必须不标。
	money, _ := parseGenericBalance([]byte(`{"balance":32.53,"remaining":32.53}`))
	if !money.InCurrency {
		t.Fatal("balance/remaining 是货币字段，不能被当成点")
	}
	points, _ := parseGenericBalance([]byte(`{"data":{"quota":16265000,"used_quota":0}}`))
	if points.InCurrency {
		t.Fatal("new-api 的 quota 是点，不能标成货币（会被少折算 500000 倍）")
	}
}

func TestParseUsageBalance(t *testing.T) {
	read, ok := parseUsageBalance([]byte(`{"key_prefix":"sk-demo","cost_usd_used":23.244912,"cost_limit_usd":100.0,"cost_usd_remaining":76.755088,"is_active":true}`))
	if !ok {
		t.Fatal("openagents 形状应当解析成功")
	}
	if read.Remaining != 76.755088 || read.Used != 23.244912 || read.Quota != 100 {
		t.Fatalf("read = %+v", read)
	}

	// 只给已用与限额时也要能算出剩余（有的站点不给 remaining 字段）。
	only, ok := parseUsageBalance([]byte(`{"cost_usd_used":10,"cost_limit_usd":30}`))
	if !ok || only.Remaining != 20 {
		t.Fatalf("used+limit 形状: read=%+v ok=%v", only, ok)
	}
}

func TestParseBillingUsageUnitIsCents(t *testing.T) {
	// 实测 pipixia 返回 total_usage=3396.6634，单位是美分 ⇒ $33.97。当成美元会放大 100 倍。
	spent, ok := parseBillingUsage([]byte(`{"total_usage":3396.6634}`))
	if !ok {
		t.Fatal("应当解析成功")
	}
	if spent < 33.96 || spent > 33.98 {
		t.Fatalf("spent = %v, want ≈33.97（美分→美元）", spent)
	}
}

// TestReadBalanceAutoFollowsProtocolFamily 用真实 HTTP 桩逐族验证探测链：只有某种接口可用时，
// 必须命中那一族并给出对应来源；额度是占位值的站点必须"额度未知但用量已知"。
func TestReadBalanceAutoFollowsProtocolFamily(t *testing.T) {
	serve := func(handlers map[string]http.HandlerFunc) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if handler, ok := handlers[r.URL.Path]; ok {
				handler(w, r)
				return
			}
			http.NotFound(w, r)
		}))
	}
	ok := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
				t.Errorf("缺少 Bearer 凭据：%q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}
	}

	t.Run("user_balance 族", func(t *testing.T) {
		server := serve(map[string]http.HandlerFunc{
			PathUserBalance: ok(`{"balance":32.53154022,"remaining":32.53154022,"unit":"USD"}`),
		})
		defer server.Close()
		read := ReadBalanceAuto(context.Background(), server.URL, "sk-test", server.Client(), "")
		if !read.OK || read.Source != SourceUserBalance {
			t.Fatalf("read = %+v, want source=%s", read, SourceUserBalance)
		}
		if read.Remaining != 32.53154022 {
			t.Fatalf("remaining = %v", read.Remaining)
		}
	})

	t.Run("usage 族", func(t *testing.T) {
		server := serve(map[string]http.HandlerFunc{
			PathUsage: ok(`{"cost_usd_used":23.244912,"cost_limit_usd":100.0,"cost_usd_remaining":76.755088}`),
		})
		defer server.Close()
		read := ReadBalanceAuto(context.Background(), server.URL, "sk-test", server.Client(), "")
		if !read.OK || read.Source != SourceUsage || read.Remaining != 76.755088 {
			t.Fatalf("read = %+v", read)
		}
	})

	t.Run("new-api self 族", func(t *testing.T) {
		server := serve(map[string]http.HandlerFunc{
			PathNewAPISelf: ok(`{"data":{"quota":5000000,"used_quota":1000000}}`),
		})
		defer server.Close()
		read := ReadBalanceAuto(context.Background(), server.URL, "sk-test", server.Client(), "")
		if !read.OK || read.Source != SourceNewAPISelf {
			t.Fatalf("read = %+v", read)
		}
	})

	t.Run("占位额度只算用量", func(t *testing.T) {
		server := serve(map[string]http.HandlerFunc{
			PathBillingSubscrip: ok(`{"hard_limit_usd":100000000,"soft_limit_usd":100000000}`),
			PathBillingUsage:    ok(`{"total_usage":3396.6634}`),
		})
		defer server.Close()
		read := ReadBalanceAuto(context.Background(), server.URL, "sk-test", server.Client(), "")
		if read.OK {
			t.Fatalf("占位额度不能当余额：%+v", read)
		}
		if read.Quota != 0 {
			t.Fatalf("占位值必须被拒（quota = %v）", read.Quota)
		}
		if read.ReasonKey != ReasonQuotaPlaceholder {
			t.Fatalf("原因码 = %q, want %q", read.ReasonKey, ReasonQuotaPlaceholder)
		}
		if read.Spent < 33.96 || read.Spent > 33.98 {
			t.Fatalf("已用 = %v, want ≈33.97", read.Spent)
		}
		if !strings.Contains(read.ReasonText, "用量可信") {
			t.Fatalf("文案要说清额度不可信但用量可信：%q", read.ReasonText)
		}
	})

	t.Run("真实额度减去用量", func(t *testing.T) {
		server := serve(map[string]http.HandlerFunc{
			PathBillingSubscrip: ok(`{"hard_limit_usd":100.0}`),
			PathBillingUsage:    ok(`{"total_usage":2000}`),
		})
		defer server.Close()
		read := ReadBalanceAuto(context.Background(), server.URL, "sk-test", server.Client(), "")
		if !read.OK || read.Remaining != 80 {
			t.Fatalf("read = %+v, want remaining=80", read)
		}
	})

	t.Run("全不可用时报出试过哪些路径", func(t *testing.T) {
		server := serve(nil)
		defer server.Close()
		read := ReadBalanceAuto(context.Background(), server.URL, "sk-test", server.Client(), "")
		if read.OK {
			t.Fatalf("不该有读数：%+v", read)
		}
		if read.ReasonKey != ReasonEndpointGone {
			t.Fatalf("原因码 = %q, want %q", read.ReasonKey, ReasonEndpointGone)
		}
		for _, path := range []string{PathUserBalance, PathUsage, PathNewAPISelf, PathBillingSubscrip} {
			if !strings.Contains(read.ReasonText, path) {
				t.Fatalf("文案里应列出试过的路径 %s：%q", path, read.ReasonText)
			}
		}
	})
}
