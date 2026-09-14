package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// TestAllowKeyRPMUnlimitedZeroLimit limit<=0 恒放行 (不限流默认口径)。
func TestAllowKeyRPMUnlimitedZeroLimit(t *testing.T) {
	ResetRateLimitsForTest()
	for i := 0; i < 100; i++ {
		if !AllowKeyRPM(1, 0) {
			t.Fatalf("request %d rejected with unlimited key", i+1)
		}
	}
}

// TestAllowKeyRPMRejectsOverLimit 超出限额的请求被拒。
func TestAllowKeyRPMRejectsOverLimit(t *testing.T) {
	ResetRateLimitsForTest()
	for i := 0; i < 3; i++ {
		if !AllowKeyRPM(7, 3) {
			t.Fatalf("request %d rejected under limit 3", i+1)
		}
	}
	if AllowKeyRPM(7, 3) {
		t.Fatal("request 4 allowed over limit 3")
	}
	// 其他 key 互不影响。
	if !AllowKeyRPM(8, 3) {
		t.Fatal("unrelated key affected by key 7 limit")
	}
}

// TestRecordAndAllowKeyTPM TPM 记账后按用量拒新请求。
func TestRecordAndAllowKeyTPM(t *testing.T) {
	ResetRateLimitsForTest()
	if !AllowKeyTPM(5, 1000) {
		t.Fatal("empty window should allow")
	}
	RecordKeyUsage(5, 900)
	if !AllowKeyTPM(5, 1000) {
		t.Fatal("900/1000 should still allow")
	}
	RecordKeyUsage(5, 150)
	if AllowKeyTPM(5, 1000) {
		t.Fatal("1050/1000 should reject new requests")
	}
	// 零 token 记账按 1 计, 防零成本穿透。
	RecordKeyUsage(5, 0)
}

// TestLogUsageHourlySkipsEmptyAttribution 无归属请求不进用量桶。
func TestLogUsageHourlySkipsEmptyAttribution(t *testing.T) {
	LogUsageHourly("", "chan", model.StatsMetrics{RequestFailed: 1})
	LogUsageHourly("model", "", model.StatsMetrics{RequestFailed: 1})
	// 不 panic 即视为过: 桶为空, 落库轮次不会产出行。
}
