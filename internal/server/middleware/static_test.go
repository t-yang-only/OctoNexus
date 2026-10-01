package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func gzipBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(raw); err != nil {
		t.Fatalf("压缩夹具失败：%v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("压缩夹具失败：%v", err)
	}
	return buffer.Bytes()
}

// readBodyForAssert 取出响应正文；带了 Content-Encoding: gzip 时先解压。
//
// 为什么需要它：httptest.NewRecorder 只是个 buffer，**不会**像真实 HTTP 客户端那样
// 自动解压。客户端发了 Accept-Encoding: gzip，服务端按语义回 gzip 字节 +
// Content-Encoding: gzip —— 这是对的；测试直接读 buffer 却期待明文，
// 于是得到一个永远失败的断言（2026-10-01 在 Linux 上跑 -race 时抓到：
// TestStaticServesSPADeepLink 每次都在 gzip 用例上红，掩盖了其余用例的真实结果）。
//
// 注意它**不是**把断言放宽：gzip 用例仍然要求响应真的带 Content-Encoding: gzip
// （见调用处），否则"服务端回明文"也能蒙混过关，这条用例就白设了。
func readBodyForAssert(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	if !strings.EqualFold(recorder.Header().Get("Content-Encoding"), "gzip") {
		return recorder.Body.String()
	}
	reader, err := gzip.NewReader(bytes.NewReader(recorder.Body.Bytes()))
	if err != nil {
		t.Fatalf("响应带了 Content-Encoding: gzip 但解不开：%v", err)
	}
	defer reader.Close()
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("读取解压后的正文失败：%v", err)
	}
	return string(raw)
}

// TestStaticServesSPADeepLink 钉住 2026-09-20 用户实测的缺陷：
//
// 面板是客户端路由，刷新 /channel、/group 这类子页面时浏览器请求的是同路径，
// 而磁盘上没有对应文件 —— 原来中间件直接 c.Next()，落到 gin 的默认 404，
// 用户看到的就是一行 `404 page not found`。
//
// 夹具刻意只放 **.gz**（生产构建就是这个形态，前端产物全是压缩件）：
// 这样"客户端不收 gzip 时必须自己解压"这条分支才会被真的走到 ——
// 第一版夹具放了明文 index.html，于是漏掉了这条，线上 curl 形态依旧 404。
func TestStaticServesSPADeepLink(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	index := []byte("<!doctype html><html><body><div id=\"root\"></div></body></html>")
	script := []byte("console.log(1)")
	if err := os.WriteFile(filepath.Join(dir, "index.html.gz"), gzipBytes(t, index), 0o600); err != nil {
		t.Fatalf("写夹具失败：%v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "index-abc.js.gz"), gzipBytes(t, script), 0o600); err != nil {
		t.Fatalf("写脚本夹具失败：%v", err)
	}

	engine := gin.New()
	engine.Use(StaticLocal("/", dir))
	engine.GET("/api/v1/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	cases := []struct {
		path     string
		accept   string
		encoding string
		status   int
		want     string
		why      string
	}{
		{"/", "text/html", "", http.StatusOK, "<div id=\"root\">", "根路径给入口页（无 gzip 支持时自己解压）"},
		{"/channel", "text/html", "", http.StatusOK, "<div id=\"root\">", "SPA 深链必须兜底（原来是一行 404）"},
		{"/channel", "text/html", "gzip", http.StatusOK, "<div id=\"root\">", "浏览器形态（收 gzip）同样要兜底"},
		{"/group/617", "text/html", "", http.StatusOK, "<div id=\"root\">", "带参数的深链同样兜底"},
		{"/assets/index-abc.js", "*/*", "", http.StatusOK, "console.log(1)", "真实资源照常返回（缺 gzip 支持时解压）"},
		{"/assets/missing.js", "*/*", "", http.StatusNotFound, "", "缺资源不得被入口页顶替（否则问题被藏起来）"},
		{"/favicon.ico", "image/*", "", http.StatusNotFound, "", "有扩展名的缺失文件不兜底"},
		{"/api/v1/unknown", "application/json", "", http.StatusNotFound, "", "未知接口必须保持真实 404"},
		// 前缀陷阱：/apikey 是面板的「API Key」页，用 HasPrefix(path, "/api") 会把它当成接口路径，
		// 结果这一页 404 —— 用户报障时就在这一页。判据必须是 /api/。
		{"/apikey", "text/html", "", http.StatusOK, "<div id=\"root\">", "「API Key」页必须兜底，不能被 /api 前缀误判"},
		{"/apiary", "text/html", "", http.StatusOK, "<div id=\"root\">", "任何以 /api 开头但不是接口的页面路径都要兜底"},
	}

	for _, item := range cases {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, item.path, nil)
		request.Header.Set("Accept", item.accept)
		if item.encoding != "" {
			request.Header.Set("Accept-Encoding", item.encoding)
		}
		engine.ServeHTTP(recorder, request)
		// 声明了收 gzip 就必须真的回 gzip：少这一条，服务端改成"一律回明文"
		// 也能让上面的 want 断言通过，这条用例就守不住任何东西了。
		if item.encoding == "gzip" && !strings.EqualFold(recorder.Header().Get("Content-Encoding"), "gzip") {
			t.Errorf("%s：客户端发了 Accept-Encoding: gzip，响应却没有 Content-Encoding: gzip（头=%q）",
				item.path, recorder.Header().Get("Content-Encoding"))
		}
		body := readBodyForAssert(t, recorder)
		if recorder.Code != item.status {
			t.Errorf("%s（%s）：期望 %d，实得 %d（body=%q）", item.path, item.why, item.status, recorder.Code, truncate(body, 60))
			continue
		}
		if item.want != "" && !strings.Contains(body, item.want) {
			t.Errorf("%s：响应里应含 %q，实得 %q", item.path, item.want, truncate(body, 80))
		}
	}

	// 接口路由不能被静态中间件抢走
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil))
	if recorder.Body.String() != "pong" {
		t.Errorf("接口路由被静态中间件抢走了：%q", recorder.Body.String())
	}

	// 明确只接受 JSON 的客户端不该收到 HTML 兜底（curl 拉接口的形态）
	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/channel", nil)
	request.Header.Set("Accept", "application/json")
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Errorf("只接受 JSON 的客户端不该拿到 HTML 兜底，实得 %d", recorder.Code)
	}
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit]
}
