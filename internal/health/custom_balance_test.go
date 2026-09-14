package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCustomBalanceValidationAndPaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer custom-token" {
			t.Errorf("authorization header missing")
		}
		w.Write([]byte(`{"data":{"total":"100","spent":25,"left":"75"}}`))
	}))
	defer server.Close()
	snap, err := FetchCustomBalance(context.Background(), CustomBalanceConfig{
		Endpoint: server.URL, Token: "custom-token", QuotaPath: "data.total", UsedPath: "data.spent", RemainingPath: "data.left",
	})
	if err != nil || snap.Quota != 100 || snap.Used != 25 || snap.Remaining != 75 {
		t.Fatalf("snapshot=%+v err=%v", snap, err)
	}
	if err := (CustomBalanceConfig{Endpoint: server.URL, QuotaPath: "data..total"}).Validate(); err == nil {
		t.Fatal("invalid JSON path accepted")
	}
}

func TestCustomBalanceDerivesValuesAndLimitsBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"quota":100,"used":40}`))
	}))
	defer server.Close()
	snap, err := FetchCustomBalance(context.Background(), CustomBalanceConfig{Endpoint: server.URL, QuotaPath: "quota", UsedPath: "used"})
	if err != nil || snap.Remaining != 60 {
		t.Fatalf("derived snapshot=%+v err=%v", snap, err)
	}

	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"remaining":"` + strings.Repeat("x", CustomBalanceMaxBodyBytes) + `"}`))
	}))
	defer large.Close()
	if _, err := FetchCustomBalance(context.Background(), CustomBalanceConfig{Endpoint: large.URL, RemainingPath: "remaining"}); err == nil {
		t.Fatal("oversized response accepted")
	}

	if _, err := FetchCustomBalance(context.Background(), CustomBalanceConfig{Endpoint: server.URL, RemainingPath: "remaining", Timeout: time.Nanosecond}); err == nil {
		t.Fatal("expired timeout accepted")
	}
}
