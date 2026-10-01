package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCustomBalanceRejectsUsedOnly 仅配置已用路径时无从推知剩余，
// 必须失败而非返回负剩余（与 ParseBalancePayload 同口径，防归零停用误判）。
func TestCustomBalanceRejectsUsedOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"used":40}`))
	}))
	defer srv.Close()
	_, err := FetchCustomBalance(context.Background(), CustomBalanceConfig{Endpoint: srv.URL, UsedPath: "used"})
	if err == nil {
		t.Fatal("used-only config must be rejected, not yield negative remaining")
	}
}

// TestCustomBalanceClampsOverspendRemaining 上游已用>总额（超额脏数据）时
// 剩余归零而非透传负数，避免阈值/归零停用语义漂移。
func TestCustomBalanceClampsOverspendRemaining(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"quota":10,"used":40}`))
	}))
	defer srv.Close()
	snap, err := FetchCustomBalance(context.Background(), CustomBalanceConfig{Endpoint: srv.URL, QuotaPath: "quota", UsedPath: "used"})
	if err != nil {
		t.Fatalf("overspend: %v", err)
	}
	if snap.Remaining != 0 {
		t.Fatalf("overspend remaining = %v, want 0 (clamped, not negative)", snap.Remaining)
	}
}

func TestCustomBalanceRejectsHTML(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"html-error-page", "<html><body>200 OK login</body></html>"},
		{"empty-body", ""},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(tc.body))
		}))
		_, err := FetchCustomBalance(context.Background(), CustomBalanceConfig{Endpoint: srv.URL, RemainingPath: "remaining"})
		srv.Close()
		if err == nil {
			t.Fatalf("%s: want error for non-JSON 2xx body, got success", tc.name)
		}
	}
}
