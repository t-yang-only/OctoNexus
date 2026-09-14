package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestParseBalancePayload 覆盖宽容字段解析：标准形/data 嵌套/字符串数字/缺字段。
func TestParseBalancePayload(t *testing.T) {
	q, u, r, ok := ParseBalancePayload(balanceProbePayload(map[string]any{
		"quota": 100.0, "used_quota": 30.0, "remain_quota": 70.0,
	}))
	if !ok || q != 100 || u != 30 || r != 70 {
		t.Errorf("standard = %v/%v/%v ok=%v, want 100/30/70 true", q, u, r, ok)
	}

	q, u, r, ok = ParseBalancePayload(balanceProbePayload(map[string]any{
		"data": map[string]any{"quota": "200", "used": "50", "balance": "150"},
	}))
	if !ok || q != 200 || u != 50 || r != 150 {
		t.Errorf("nested+string = %v/%v/%v ok=%v, want 200/50/150 true", q, u, r, ok)
	}

	// 剩余缺失时由总额推导。
	q, u, r, ok = ParseBalancePayload(balanceProbePayload(map[string]any{
		"quota": 100.0, "used": 40.0,
	}))
	if !ok || r != 60 {
		t.Errorf("derived remaining = %v ok=%v, want 60 true", r, ok)
	}

	// 仅"已用"单字段：总额与剩余均无从推知，拒绝而非给出负剩余（147 审核修正）。
	if _, _, _, ok := ParseBalancePayload(balanceProbePayload(map[string]any{"used": 50.0})); ok {
		t.Fatalf("used-only ok=true, want false")
	}

	// 仅"剩余"单字段：总额由已用+剩余推导。
	q, u, r, ok = ParseBalancePayload(balanceProbePayload(map[string]any{"balance": 30.0}))
	if !ok || r != 30 || q != 30 {
		t.Errorf("remaining-only = %v/%v/%v ok=%v, want quota 30 remaining 30", q, u, r, ok)
	}

	// 全无额度字段 → ok=false，调用方只记事件不落库。
	if _, _, _, ok := ParseBalancePayload(balanceProbePayload(map[string]any{"msg": "ok"})); ok {
		t.Fatalf("empty fields ok=true, want false")
	}

	// 非 JSON → ok=false。
	if _, _, _, ok := ParseBalancePayload([]byte("not-json")); ok {
		t.Fatalf("bad json ok=true, want false")
	}
}

// TestFingerprintBalance 同值同指纹、异值异指纹。
func TestFingerprintBalance(t *testing.T) {
	a := FingerprintBalance(100, 30, 70)
	b := FingerprintBalance(100, 30, 70)
	c := FingerprintBalance(100, 30, 69)
	if a != b {
		t.Fatalf("same values differ: %s vs %s", a, b)
	}
	if a == c {
		t.Fatalf("different values share fingerprint %s", a)
	}
	if len(a) != 64 {
		t.Fatalf("fingerprint len = %d, want 64 (sha256 hex)", len(a))
	}
}

// TestBelowThreshold 阈值<=0 永不触发。
func TestBelowThreshold(t *testing.T) {
	if !BelowThreshold(5, 10) {
		t.Fatalf("5 < 10 should trigger")
	}
	if BelowThreshold(15, 10) {
		t.Fatalf("15 < 10 should not trigger")
	}
	if BelowThreshold(0, 0) {
		t.Fatalf("threshold 0 should never trigger")
	}
	if BelowThreshold(-1, -5) {
		t.Fatalf("negative threshold should never trigger")
	}
}

// TestFetchBalance 假上游：正常/非 2xx/坏 JSON 三分支。
func TestFetchBalance(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/self" {
			t.Errorf("path = %s, want /api/user/self", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer mon-token" {
			t.Errorf("auth = %q, want Bearer mon-token", got)
		}
		w.Write([]byte(`{"quota":100,"used_quota":20,"remain_quota":80}`))
	}))
	defer good.Close()

	q, u, r, ok := FetchBalance(context.Background(), good.URL, "mon-token", false)
	if !ok || q != 100 || u != 20 || r != 80 {
		t.Errorf("good = %v/%v/%v ok=%v, want 100/20/80 true", q, u, r, ok)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer bad.Close()
	if _, _, _, ok := FetchBalance(context.Background(), bad.URL, "x", false); ok {
		t.Fatalf("401 ok=true, want false")
	}

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not-json"))
	}))
	defer broken.Close()
	if _, _, _, ok := FetchBalance(context.Background(), broken.URL, "x", false); ok {
		t.Fatalf("bad json ok=true, want false")
	}
}

// TestScanOneChannel 覆盖变化判定与阈值事件分支。
func TestScanOneChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"quota":100,"used":90,"remaining":10}`))
	}))
	defer srv.Close()

	snap, ev := ScanOneChannel(context.Background(), 7, srv.URL, "", false, "", 20)
	if snap.ChannelID != 7 || snap.Remaining != 10 {
		t.Fatalf("snap = %+v, want channel 7 remaining 10", snap)
	}
	if !snap.Changed {
		t.Fatalf("first scan Changed=false, want true (empty last fingerprint)")
	}
	if ev == nil || ev.Threshold != 20 || ev.Remaining != 10 {
		t.Fatalf("event = %+v, want threshold 20 remaining 10", ev)
	}

	// 同值二次扫描：指纹不变；阈值未配置时无事件。
	fp := FingerprintBalance(100, 90, 10)
	snap2, ev2 := ScanOneChannel(context.Background(), 7, srv.URL, "", false, fp, 0)
	if snap2.Changed {
		t.Fatalf(" repeat scan Changed=true, want false")
	}
	if ev2 != nil {
		t.Fatalf("threshold 0 event = %+v, want nil", ev2)
	}

	// 采集失败：零值快照 + 无事件（只记事件，不阻断）。
	snap3, ev3 := ScanOneChannel(context.Background(), 7, "http://127.0.0.1:1", "", false, "", 5)
	if ev3 != nil {
		t.Fatalf("failed scan event = %+v, want nil", ev3)
	}
	if snap3.Changed || snap3.Remaining != 0 {
		t.Fatalf("failed scan snap = %+v, want zero/unchanged", snap3)
	}
}

// TestJoinBalanceURL 避免双斜杠/缺斜杠。
func TestJoinBalanceURL(t *testing.T) {
	if got := joinBalanceURL("https://x.com/", "/api/user/self"); got != "https://x.com/api/user/self" {
		t.Fatalf("double slash = %s", got)
	}
	if got := joinBalanceURL("https://x.com", "api/user/self"); !strings.HasSuffix(got, "/api/user/self") {
		t.Fatalf("missing slash = %s", got)
	}
}
