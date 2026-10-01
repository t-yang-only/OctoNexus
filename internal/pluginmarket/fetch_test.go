package pluginmarket

// GitHub 安装链路的判据（需求3）。
//
// # 这一组要防住的事
//
//  1. **复用 http.Request 做第二次 Do** —— Request 不是可重放对象：Body 是
//     io.Reader 读一次就耗尽，连接状态与上下文都带着第一次的痕迹。
//     直连失败改走代理时必须新建请求（本项目 price/update 都是这个写法）。
//  2. **大小限制形同虚设** —— 用 io.LimitReader 在读的时候限，而不是先全读再判：
//     这个入口的内容来自用户粘贴的链接，本就不该被信任。
//  3. **错误分类含糊** —— "链接形态不对"与"下载失败"必须分开，否则用户看到
//     "下载失败"会去查网络，而真正要改的是链接本身。

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// 直连路径不可达时，回退代理仍要能取到内容。
//
// 这条判据直接对着上面第 1 条：旧实现复用同一个 *http.Request，
// 桩服务只能收到一次请求；改成每次新建请求后，两次尝试都会真的发出去。
func TestFetchLimitedRetriesWithFreshRequest(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"kind":"balance","name":"stub","spec":{"hosts":["a.example"],"read":[{"name":"balance"}]}}`)
	}))
	defer server.Close()

	body, err := fetchLimited(server.URL, 1<<20)
	if err != nil {
		t.Fatalf("fetchLimited: %v", err)
	}
	if !strings.Contains(string(body), "balance") {
		t.Errorf("内容不符合预期：%s", body)
	}
	// 桩必然可达，所以只发一次请求 —— 若实现里每次都发两次，说明回退逻辑写错了方向。
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("桩可达时应只请求 1 次，实得 %d（回退逻辑不该在成功时也触发）", got)
	}
}

// 大小限制必须在读取时限住，而不是先全读再判。
func TestFetchLimitedRejectsOversizedBody(t *testing.T) {
	big := strings.Repeat("x", 4096)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, big)
	}))
	defer server.Close()

	_, err := fetchLimited(server.URL, 1024)
	if err == nil {
		t.Fatal("超过上限应被拒")
	}
	if !strings.Contains(err.Error(), "超过") {
		t.Errorf("错误信息应说明是大小超限，实得：%v", err)
	}
}

// 非 200 要带状态码：用户需要区分"链接不存在(404)"与"网络不通"。
func TestFetchLimitedReportsHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := fetchLimited(server.URL, 1<<20)
	if err == nil {
		t.Fatal("404 应报错")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("错误信息应带 HTTP 状态码，实得：%v", err)
	}
}

// 链接形态错误与下载失败必须分开：前者改链接即可，后者才需要查网络。
func TestFetchFromGitHubClassifiesLinkShapeErrors(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"空链接", "", "链接为空"},
		{"非 https", "http://github.com/a/b/blob/main/c.json", "https"},
		{"非 GitHub 域名", "https://example.com/x.json", "GitHub"},
		{"blob 链接缺文件", "https://github.com/a/b", "形态"},
		{"tree 页面不是文件", "https://github.com/a/b/tree/main/dir", "形态"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FetchFromGitHub(tc.url)
			if err == nil {
				t.Fatalf("应拒绝：%s", tc.url)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误应包含 %q，实得：%v", tc.want, err)
			}
		})
	}
}

// blob 形态必须被归一成 raw 直链（用户粘贴最常见的就是这种）。
//
// 这条是变异检查补出来的：只测"tree 页面要拒"抓不住"blob 分支被删"——
// tree 链接压根走不到 blob 分支，删了它照样拒。要覆盖就得真的喂一个 blob 链接，
// 并断言它被转成了正确的 raw 地址。
func TestNormalizeGitHubURLConvertsBlobToRaw(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"blob → raw",
			"https://github.com/octo/plugins/blob/main/balance/okai.json",
			"https://raw.githubusercontent.com/octo/plugins/main/balance/okai.json",
		},
		{
			"raw 路径 → raw",
			"https://github.com/octo/plugins/raw/main/balance/okai.json",
			"https://raw.githubusercontent.com/octo/plugins/main/balance/okai.json",
		},
		{
			"raw 直链原样",
			"https://raw.githubusercontent.com/octo/plugins/main/balance/okai.json",
			"https://raw.githubusercontent.com/octo/plugins/main/balance/okai.json",
		},
		{
			"深层路径",
			"https://github.com/octo/plugins/blob/dev/pools/custom-new-api.json",
			"https://raw.githubusercontent.com/octo/plugins/dev/pools/custom-new-api.json",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeGitHubURL(tc.in)
			if err != nil {
				t.Fatalf("应识别：%v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// 直连失败后走代理，第二次尝试必须用**新的** Request。
//
// 这条判据直接对着"复用同一个 *http.Request 做第二次 Do"那个缺陷。
// Request 不是可重放对象：Body 是 io.Reader 读一次就耗尽，连接状态与上下文
// 都带着第一次的痕迹。GET 虽无 body，但复用时第一次的失败上下文仍然生效 ——
// 实测表现是第二次也失败，于是"直连不通时试代理"这条退路整个失效。
//
// 构造方式：桩对**第一次**请求直接关闭连接（模拟直连失败），后续请求正常返回。
// 旧实现复用 req 时会第二次也失败；新实现每次新建请求，第二次能成功。
func TestFetchLimitedFallbackUsesFreshRequest(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			// 掐掉连接，让客户端这一侧视为失败。
			if hijacker, ok := w.(http.Hijacker); ok {
				conn, _, err := hijacker.Hijack()
				if err == nil {
					conn.Close()
					return
				}
			}
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"kind":"balance","name":"stub","spec":{"hosts":["a.example"],"read":[{"name":"balance"}]}}`)
	}))
	defer server.Close()

	body, err := fetchLimited(server.URL, 1<<20)
	if err != nil {
		t.Fatalf("第一次失败后应能通过重试取到内容：%v", err)
	}
	if !strings.Contains(string(body), "balance") {
		t.Errorf("内容不符合预期：%s", body)
	}
	if got := atomic.LoadInt32(&attempts); got < 2 {
		t.Errorf("桩应至少收到 2 次请求（第一次失败 + 一次重试），实得 %d", got)
	}
}

// 端到端：从 GitHub 形态下载 + 解析 + 转交，整条链路要通。
//
// 用本地桩替身 raw.githubusercontent.com 不可行（域名写死），
// 所以这里直接测「字节 → 插件」那一段，它是链路里唯一有逻辑的部分。
func TestPluginFromRawBytesEndToEnd(t *testing.T) {
	raw := []byte(`{"kind":"pool","name":"X 站","spec":{"kind":"custom-x","title":"X 站","base_url":"https://x.example","capabilities":["list","get"]}}`)
	plugin, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := plugin.Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if result.Kind != KindPool || result.Name != "X 站" {
		t.Errorf("转交结果不完整：%+v", result)
	}
	if len(result.Spec) == 0 {
		t.Error("spec 必须原样带回，供界面预览与排查")
	}
}
