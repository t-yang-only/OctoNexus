package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubS2Keys 起一个只带 /api/keys 的桩：返回 payload 原样，供两种形状分支测试。
func stubS2Keys(t *testing.T, payload string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSub2APIKeysShapes(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		wantLen int
		firstSk string
	}{
		{"data-object", `{"data":[{"id":7,"name":"k1","sk":"sk-1","status":"enabled"}]}`, 1, "sk-1"},
		{"top-level-array", `[{"id":1,"name":"n","sk":"s"}]`, 1, "s"},
		{"key-alias", `{"data":[{"id":"9","name":"k2","key":"sk-2","status":"disabled"}]}`, 1, "sk-2"},
	}
	for _, tc := range cases {
		srv := stubS2Keys(t, tc.payload)
		keys, ok := Sub2APIKeys(context.Background(), nil, srv.URL, "t")
		if !ok || len(keys) != tc.wantLen {
			t.Fatalf("%s: keys=%+v ok=%v, want %d keys", tc.name, keys, ok, tc.wantLen)
		}
		if keys[0].Sk != tc.firstSk {
			t.Fatalf("%s: sk=%q, want %q", tc.name, keys[0].Sk, tc.firstSk)
		}
	}
}

func TestSub2APIKeysBadJSON(t *testing.T) {
	srv := stubS2Keys(t, `not-json`)
	if _, ok := Sub2APIKeys(context.Background(), nil, srv.URL, "t"); ok {
		t.Fatalf("bad json: want fail")
	}
}
