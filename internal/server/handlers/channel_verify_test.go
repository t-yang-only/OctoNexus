package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// T-verify-004 批量实测渠道模型的判据。
//
// ## 为什么必须"实测"而不是"看清单"
//
// T-usability-006 的教训：上游 /v1/models 的清单**不完整** ——
// 实测发现「可茶/MiniMax-M3」不在清单里但实际调用返回 200。
// 清单只能给出下界，回答不了「这个模型到底能不能用」。
//
// 所以这个功能的判据要盯住：
//  1. **请求真的发到了上游**（正确的 URL、认证头、模型名）；
//  2. **判定只看状态码**，不被上游响应体的大小或内容影响；
//  3. 失败时给出**可分辨的原因**（网络失败 vs 上游拒绝）。

// 上游接受 → Usable 为真，请求头与模型名必须正确送达。
func TestVerifyOneModelSendsCorrectRequest(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		buf := make([]byte, 512)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	result := verifyOneModel(
		context.Background(),
		srv.Client(),
		model.ChannelConfig{BaseURL: srv.URL},
		"sk-abc",
		"glm-5.3-flash",
	)

	if !result.Usable {
		t.Fatalf("上游返回 200 应判为可用，实得 usable=%v error=%q", result.Usable, result.Error)
	}
	// 路径必须落到 /v1/chat/completions —— 这是真实调用的入口，
	// 实测走别的路径就没有说服力（那不等于"能用"）。
	if !strings.HasSuffix(gotPath, "/v1/chat/completions") {
		t.Fatalf("应请求 /v1/chat/completions，实得 %q", gotPath)
	}
	if gotAuth != "Bearer sk-abc" {
		t.Fatalf("认证头应为 Bearer sk-abc，实得 %q", gotAuth)
	}
	// 模型名必须原样送出：实测的意义就是验证"上游认不认这个名字"。
	if !strings.Contains(gotBody, `"model":"glm-5.3-flash"`) {
		t.Fatalf("请求体应含被实测的模型名，实得 %q", gotBody)
	}
	// max_tokens=1：只为验证上游收不收，不需要真吐内容（省额度也更快）。
	if !strings.Contains(gotBody, `"max_tokens":1`) {
		t.Fatalf("请求体应限制 max_tokens=1，实得 %q", gotBody)
	}
}

// 上游拒绝 → Usable 为假，且错误里带状态码（可分辨原因）。
func TestVerifyOneModelReportsRejection(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429, 500} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))

		result := verifyOneModel(
			context.Background(),
			srv.Client(),
			model.ChannelConfig{BaseURL: srv.URL},
			"sk-abc",
			"m",
		)
		srv.Close()

		if result.Usable {
			t.Fatalf("HTTP %d 不该判为可用", status)
		}
		if result.Status != status {
			t.Fatalf("状态码应为 %d，实得 %d", status, result.Status)
		}
		if !strings.Contains(result.Error, "HTTP") {
			t.Fatalf("错误信息应带状态码，实得 %q", result.Error)
		}
	}
}

// 网络失败（地址不通）→ Status 为 0，错误里带原因。
//
// 这一条把它与"上游拒绝"区分开：两者的处置完全不同
// （前者查网络/代理，后者查模型名/权限）。
func TestVerifyOneModelNetworkFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // 立刻关掉，让请求打不通

	result := verifyOneModel(
		context.Background(),
		srv.Client(),
		model.ChannelConfig{BaseURL: srv.URL},
		"sk-abc",
		"m",
	)

	if result.Usable {
		t.Fatalf("打不通不该判为可用")
	}
	if result.Status != 0 {
		t.Fatalf("网络失败时状态码应为 0，实得 %d", result.Status)
	}
	if result.Error == "" {
		t.Fatalf("网络失败必须给出原因，否则用户不知道是网络问题还是模型问题")
	}
}

// 没有可用凭据时不发请求，直接给出可执行的原因。
func TestVerifyOneModelWithoutKey(t *testing.T) {
	result := verifyOneModel(
		context.Background(),
		http.DefaultClient,
		model.ChannelConfig{BaseURL: "http://127.0.0.1:1"},
		"",
		"m",
	)
	if result.Usable {
		t.Fatalf("没有凭据不该判为可用")
	}
	if !strings.Contains(result.Error, "凭据") {
		t.Fatalf("应明确说是凭据问题，实得 %q", result.Error)
	}
}

// 2xx 的其它状态码（201/204）也算可用 —— 判据是"上游收下了"，不是"恰好 200"。
func TestVerifyOneModelAcceptsAny2xx(t *testing.T) {
	for _, status := range []int{200, 201, 204} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		result := verifyOneModel(
			context.Background(),
			srv.Client(),
			model.ChannelConfig{BaseURL: srv.URL},
			"sk-abc",
			"m",
		)
		srv.Close()
		if !result.Usable {
			t.Fatalf("HTTP %d 应判为可用", status)
		}
	}
}
