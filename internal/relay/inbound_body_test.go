package relay

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/looplj/axonhub/llm/httpclient"
)

// 入站正文上限判据（T-bodylimit-001）。
//
// 这组用例要挡住的是一类"自己人挡掉自己人"的故障: 依赖库把上限写死成 64 MiB,
// 于是 Codex 的 remote compact 这类大正文请求先被自家网关拒掉, 报错却是上游风格的
// "failed to read request body: request body too large", 排查时极易误判成上游问题。
// 因此判据分三层: ① 上限由我们控制（不再卡在 64 MiB）; ② 超限的错误明确可执行;
// ③ 除上限外的语义与原实现**逐字段一致**（否则换掉读取实现会引入看不见的偏差）。

func inboundRequest(t *testing.T, body []byte, headers map[string]string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://relay.local/v1/chat/completions?x=1&y=2", bytes.NewReader(body))
	req.RemoteAddr = "10.1.2.3:4567"
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	return req
}

// TestReadInboundRequestMatchesDependency 是本文件的核心判据: 同一个请求分别过我们的实现与依赖库的实现,
// 逐字段比对。任何"我抄漏了"的偏差都会在这里变红, 而不是等到线上某个头部/客户端 IP 丢了才发现。
func TestReadInboundRequestMatchesDependency(t *testing.T) {
	body := []byte(`{"model":"gpt-x","messages":[{"role":"user","content":"hi"}]}`)
	cases := []struct {
		name    string
		body    []byte
		headers map[string]string
	}{
		{"无编码", body, nil},
		{"内容编码 identity", body, map[string]string{"Content-Encoding": "identity"}},
		{"gzip", gzipBytes(t, body), map[string]string{"Content-Encoding": "gzip"}},
		{"gzip 大小写混合", gzipBytes(t, body), map[string]string{"Content-Encoding": "GZip"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mine, mineErr := readInboundRequest(inboundRequest(t, tc.body, tc.headers))
			theirs, theirsErr := httpclient.ReadHTTPRequest(inboundRequest(t, tc.body, tc.headers))
			if (mineErr == nil) != (theirsErr == nil) {
				t.Fatalf("错误行为不一致: 我们=%v 依赖=%v", mineErr, theirsErr)
			}
			if mineErr != nil {
				return
			}
			if mine.Method != theirs.Method || mine.URL != theirs.URL || mine.Path != theirs.Path {
				t.Fatalf("方法/URL/路径不一致: %+v vs %+v", mine, theirs)
			}
			if mine.Query.Encode() != theirs.Query.Encode() {
				t.Fatalf("查询串不一致: %v vs %v", mine.Query, theirs.Query)
			}
			if mine.ClientIP != theirs.ClientIP {
				t.Fatalf("客户端 IP 不一致: %q vs %q", mine.ClientIP, theirs.ClientIP)
			}
			if mine.RequestID != theirs.RequestID {
				t.Fatalf("RequestID 不一致: %q vs %q", mine.RequestID, theirs.RequestID)
			}
			if string(mine.Body) != string(theirs.Body) {
				t.Fatalf("正文不一致: 我们 %d 字节, 依赖 %d 字节", len(mine.Body), len(theirs.Body))
			}
			if mine.ContentType != theirs.ContentType || string(mine.JSONBody) != string(theirs.JSONBody) {
				t.Fatalf("ContentType/JSONBody 不一致")
			}
			if (mine.RawRequest == nil) != (theirs.RawRequest == nil) || (mine.Auth == nil) != (theirs.Auth == nil) {
				t.Fatalf("RawRequest/Auth 是否为空不一致")
			}
			// 内容编码被解码后, 两边的头集合必须同样被改写（删 Content-Encoding/Content-Length）。
			if len(mine.Headers) != len(theirs.Headers) {
				t.Fatalf("处理后头部数量不一致: 我们 %d, 依赖 %d", len(mine.Headers), len(theirs.Headers))
			}
			for name, values := range theirs.Headers {
				if strings.Join(mine.Headers[name], ",") != strings.Join(values, ",") {
					t.Fatalf("头部 %s 处理结果不一致: %v vs %v", name, mine.Headers[name], values)
				}
			}
		})
	}
}

func TestInboundRequestRejectsUnsupportedEncodingSameWay(t *testing.T) {
	// 依赖库里不认识的编码会报 "unsupported content encoding", 我们的实现必须一样（不是静默当明文收下）。
	mine, mineErr := readInboundRequest(inboundRequest(t, []byte("{}"), map[string]string{"Content-Encoding": "br"}))
	if mineErr == nil || mine != nil {
		t.Fatalf("br 编码应报错, got %+v / %v", mine, mineErr)
	}
	if !strings.Contains(mineErr.Error(), "unsupported content encoding") {
		t.Fatalf("报错文案应说明编码不支持, got %q", mineErr.Error())
	}
	_, theirsErr := httpclient.ReadHTTPRequest(inboundRequest(t, []byte("{}"), map[string]string{"Content-Encoding": "br"}))
	if theirsErr == nil || !strings.Contains(theirsErr.Error(), "unsupported content encoding") {
		t.Fatalf("依赖库对 br 的行为已变: %v", theirsErr)
	}
}

// TestInboundBodyLimitIsConfigurableAndExceedsOldCap 是本问题的回归判据。
//
// 旧行为: 依赖库上限写死 64 MiB, 65 MiB 正文必然被拒。
// 新行为: 上限由设置项决定, 默认 256 MiB —— 65 MiB 必须被接受; 且把设置项调小后同一份正文必须被拒
// （证明"能收大正文"不是把限制整个删掉, 而是换成一个可调、可关的闸）。
func TestInboundBodyLimitIsConfigurableAndExceedsOldCap(t *testing.T) {
	const oldDependencyCap = 64 * 1024 * 1024
	oversize := oldDependencyCap + 1024*1024 // 65 MiB: 旧上限下必然被拒

	// ① 设置项缺失（库还没补行 / 单元测试没有数据库）时用默认值 256 MiB —— 65 MiB 必须能收。
	savedLimit := inboundBodyLimitOverride
	defer func() { inboundBodyLimitOverride = savedLimit }()
	inboundBodyLimitOverride = 0 // 0 = 用默认值

	payload := append([]byte(`{"pad":"`), bytes.Repeat([]byte("A"), oversize)...)
	payload = append(payload, '"', '}')
	request, err := readInboundRequest(inboundRequest(t, payload, nil))
	if err != nil {
		t.Fatalf("%d 字节正文本该被接受（默认上限 256 MiB）, got %v", len(payload), err)
	}
	if len(request.Body) != len(payload) {
		t.Fatalf("正文长度变了: %d → %d", len(payload), len(request.Body))
	}

	// ② 把上限调到 1 MiB 后, 同一份正文必须被拒, 且错误是依赖库的哨兵（调用方据此回 413）。
	inboundBodyLimitOverride = 1 << 20
	_, err = readInboundRequest(inboundRequest(t, payload, nil))
	if err == nil {
		t.Fatalf("上限调到 1 MiB 后 %d 字节正文必须被拒", len(payload))
	}
	if !errors.Is(err, httpclient.ErrRequestBodyTooLarge) {
		t.Fatalf("超限必须是 ErrRequestBodyTooLarge（否则 handler 无法翻译成 413）, got %v", err)
	}
	if !strings.Contains(err.Error(), "request body too large") {
		t.Fatalf("报错文案丢了原话, 既有告警关键词会失效: %q", err.Error())
	}

	// ③ 上限 0 = 不限制（显式选择）: 同一份正文必须通过。
	inboundBodyLimitOverride = -1 // 负数表示"显式不限制"
	request, err = readInboundRequest(inboundRequest(t, payload, nil))
	if err != nil {
		t.Fatalf("上限为 0（不限制）时不该拒, got %v", err)
	}
	if len(request.Body) != len(payload) {
		t.Fatalf("不限制时正文长度变了: %d → %d", len(payload), len(request.Body))
	}
}

// TestLimitAppliesToDecompressedBody 压缩正文也要按**解压后**的大小判: 否则 gzip 一个 1 GB 的说谎包
// 就能绕过上限（依赖库原实现对 gzip/deflate/zstd 分支同样传的是同一个上限）。
func TestLimitAppliesToDecompressedBody(t *testing.T) {
	savedLimit := inboundBodyLimitOverride
	defer func() { inboundBodyLimitOverride = savedLimit }()

	inner := bytes.Repeat([]byte("A"), 3<<20) // 解压后 3 MiB
	compressed := gzipBytes(t, inner)
	if len(compressed) >= len(inner) {
		t.Fatalf("测试前提不成立: gzip 没起到压缩作用")
	}

	inboundBodyLimitOverride = 1 << 20 // 1 MiB
	_, err := readInboundRequest(inboundRequest(t, compressed, map[string]string{"Content-Encoding": "gzip"}))
	if err == nil {
		t.Fatalf("压缩包解压后超过上限时必须被拒")
	}
	if !errors.Is(err, httpclient.ErrRequestBodyTooLarge) {
		t.Fatalf("压缩分支的超限也要是 ErrRequestBodyTooLarge, got %v", err)
	}

	inboundBodyLimitOverride = 8 << 20 // 8 MiB: 现在放得下
	request, err := readInboundRequest(inboundRequest(t, compressed, map[string]string{"Content-Encoding": "gzip"}))
	if err != nil {
		t.Fatalf("上限 8 MiB 时 3 MiB 的解压正文该通过, got %v", err)
	}
	if !bytes.Equal(request.Body, inner) {
		t.Fatalf("解压正文与原文不一致")
	}
}

func TestInboundClientIPParsing(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		want       string
	}{
		{"XFF 第一跳", "10.0.0.1:1", map[string]string{"X-Forwarded-For": " 203.0.113.9 , 10.0.0.5"}, "203.0.113.9"},
		{"XFF 单值", "10.0.0.1:1", map[string]string{"X-Forwarded-For": "203.0.113.7"}, "203.0.113.7"},
		{"X-Real-IP", "10.0.0.1:1", map[string]string{"X-Real-IP": "198.51.100.4"}, "198.51.100.4"},
		{"RemoteAddr 去端口", "192.0.2.8:5555", nil, "192.0.2.8"},
		{"RemoteAddr 无端口", "192.0.2.9", nil, "192.0.2.9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := inboundRequest(t, []byte("{}"), tc.headers)
			req.RemoteAddr = tc.remoteAddr
			got := readInboundRequestMust(t, req).ClientIP
			if got != tc.want {
				t.Fatalf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func readInboundRequestMust(t *testing.T, req *http.Request) *httpclient.Request {
	t.Helper()
	out, err := readInboundRequest(req)
	if err != nil {
		t.Fatalf("读取入站请求失败: %v", err)
	}
	return out
}

func gzipBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(raw); err != nil {
		t.Fatalf("gzip 写入失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip 关闭失败: %v", err)
	}
	return buf.Bytes()
}

// 保证随机大正文路径也被覆盖（bytes.Repeat 之外的真实内存布局）。
func TestReadInboundRequestLargeRandomBody(t *testing.T) {
	savedLimit := inboundBodyLimitOverride
	defer func() { inboundBodyLimitOverride = savedLimit }()
	inboundBodyLimitOverride = 0

	raw := make([]byte, 4<<20)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("随机数据失败: %v", err)
	}
	request, err := readInboundRequest(inboundRequest(t, raw, nil))
	if err != nil {
		t.Fatalf("4 MiB 随机正文该通过, got %v", err)
	}
	if len(request.Body) != len(raw) || !bytes.Equal(request.Body, raw) {
		t.Fatalf("随机正文读回来不一致")
	}
	if _, err := url.Parse(request.URL); err != nil {
		t.Fatalf("URL 字段不是合法 URL: %q", request.URL)
	}
}
