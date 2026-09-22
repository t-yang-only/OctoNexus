package task

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// T-backup-001 WebDAV 云备份的单测。
//
// 用 httptest 起一个够用的 WebDAV 桩（PUT / PROPFIND / DELETE 三个动词），
// 既能断言"我们发了什么"，又能断言"我们怎么处理服务端的各种回法"。
// 这几条都是真实 WebDAV 服务上极易踩的点，桩里可以精确复现。
//
// WebDAVUpload 内部要调 op.DBExportAll，所以需要真库（内存库即可）：
// 不接库的话测试只能覆盖到纯函数，传上去的内容是不是可解析的转储就验不了。

func openWebDAVTestDB(t *testing.T) {
	t.Helper()
	dsn := fmt.Sprintf("file:webdav-%s?mode=memory&cache=shared", t.Name())
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(
		&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{}, &model.ChannelGrant{},
		&model.Group{}, &model.GroupItem{}, &model.APIKey{}, &model.Setting{}, &model.LLMInfo{},
		&model.StatsTotal{}, &model.StatsDaily{}, &model.StatsHourly{}, &model.StatsAPIKey{},
		&model.ProxyNode{}, &model.ProxySubscription{}, &model.OfficialAccount{},
		&model.ManualSubscription{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(db.SetDBForTest(conn))
}

// webdavStub 是一个最小的 WebDAV 服务端桩。
type webdavStub struct {
	mu       sync.Mutex
	files    map[string][]byte
	putCode  int    // 非 0 时强制返回该状态码（模拟失败）
	listCode int    // 非 0 时强制 PROPFIND 返回该状态码
	authSeen string // 记录收到的 Authorization 头
	hrefCase string // "lower" / "upper" / "bare"：PROPFIND 响应里 href 的前缀写法
	putCalls int
}

func newWebDAVStub() *webdavStub {
	return &webdavStub{files: map[string][]byte{}, hrefCase: "lower"}
}

func (s *webdavStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.authSeen = r.Header.Get("Authorization")
	s.mu.Unlock()

	name := strings.TrimPrefix(r.URL.Path, "/dav/")
	switch r.Method {
	case http.MethodPut:
		s.mu.Lock()
		defer s.mu.Unlock()
		s.putCalls++
		if s.putCode != 0 {
			w.WriteHeader(s.putCode)
			return
		}
		body := make([]byte, 0)
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			body = append(body, buf[:n]...)
			if err != nil {
				break
			}
		}
		s.files[name] = body
		w.WriteHeader(http.StatusCreated)
	case http.MethodGet:
		// 恢复路径要下载备份，桩必须支持 GET；否则"下载"这条链路的用例
		// 全部会因为桩不支持而失败（看起来像产品坏了）。
		s.mu.Lock()
		body, ok := s.files[name]
		s.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	case "PROPFIND":
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.listCode != 0 {
			w.WriteHeader(s.listCode)
			return
		}
		var sb strings.Builder
		sb.WriteString(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">`)
		for f := range s.files {
			switch s.hrefCase {
			case "upper":
				fmt.Fprintf(&sb, "<D:response><D:href>/dav/%s</D:href></D:response>", f)
			case "bare":
				fmt.Fprintf(&sb, "<response><href>/dav/%s</href></response>", f)
			default:
				fmt.Fprintf(&sb, "<d:response><d:href>/dav/%s</d:href></d:response>", f)
			}
		}
		sb.WriteString(`</d:multistatus>`)
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(207)
		_, _ = w.Write([]byte(sb.String()))
	case http.MethodDelete:
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.files, name)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *webdavStub) names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.files))
	for k := range s.files {
		out = append(out, k)
	}
	return out
}

func (s *webdavStub) file(name string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.files[name]
}

// 远端文件名必须固定前缀 + 时间戳，且不同时刻不同名（否则后一份会覆盖前一份）。
func TestWebDAVRemoteNameShape(t *testing.T) {
	a := webDAVRemoteName(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	b := webDAVRemoteName(time.Date(2026, 9, 22, 13, 0, 0, 0, time.UTC))
	if a == b {
		t.Fatalf("不同时刻应生成不同文件名，实得 %q", a)
	}
	for _, name := range []string{a, b} {
		if !strings.HasPrefix(name, "octopus-backup-") || !strings.HasSuffix(name, ".json") {
			t.Fatalf("文件名形态不对: %q", name)
		}
	}
	if a != "octopus-backup-20260922-120000.json" {
		t.Fatalf("文件名应为 UTC 时间戳: %q", a)
	}
}

// 上传成功：桩里应有该文件，且带 Basic 认证。
func TestWebDAVUploadSucceeds(t *testing.T) {
	openWebDAVTestDB(t)
	stub := newWebDAVStub()
	srv := httptest.NewServer(stub)
	defer srv.Close()

	cfg := WebDAVConfig{URL: srv.URL + "/dav", Username: "u", Password: "p"}
	name, err := WebDAVUpload(context.Background(), cfg, time.Now())
	if err != nil {
		t.Fatalf("上传应成功: %v", err)
	}
	if got := stub.names(); len(got) != 1 || got[0] != name {
		t.Fatalf("桩里应有 %q，实得 %v", name, got)
	}
	if !strings.HasPrefix(stub.authSeen, "Basic ") {
		t.Fatalf("应带 Basic 认证，实得 %q", stub.authSeen)
	}
}

// 上传内容必须是合法 JSON（恢复时直接喂给导入接口）。
func TestWebDAVUploadBodyIsParseableDump(t *testing.T) {
	openWebDAVTestDB(t)
	stub := newWebDAVStub()
	srv := httptest.NewServer(stub)
	defer srv.Close()

	cfg := WebDAVConfig{URL: srv.URL + "/dav"}
	name, err := WebDAVUpload(context.Background(), cfg, time.Now())
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	var dump model.DBDump
	if err := json.Unmarshal(stub.file(name), &dump); err != nil {
		t.Fatalf("上传内容应是可解析的转储 JSON: %v", err)
	}
	if dump.Version == 0 {
		t.Fatalf("转储应带版本号（导入端靠它判断格式）")
	}
}

// 服务端返回非 2xx 时必须报错，不能当成功。
//
// 反例：把 4xx 当成功会让"备份一直在跑、实际一份没传上去"变成看不见的故障。
func TestWebDAVUploadFailsOnNon2xx(t *testing.T) {
	openWebDAVTestDB(t)
	stub := newWebDAVStub()
	stub.putCode = http.StatusInsufficientStorage
	srv := httptest.NewServer(stub)
	defer srv.Close()

	cfg := WebDAVConfig{URL: srv.URL + "/dav"}
	name, err := WebDAVUpload(context.Background(), cfg, time.Now())
	if err == nil {
		t.Fatalf("非 2xx 应报错，实得 name=%q", name)
	}
	if name != "" {
		t.Fatalf("失败时不该返回文件名，实得 %q", name)
	}
	if !strings.Contains(err.Error(), "507") {
		t.Fatalf("错误信息应带上状态码，实得 %v", err)
	}
}

// 列出远端备份：只认本程序的文件，不误伤用户放在同目录的其它文件。
func TestWebDAVListFiltersForeignFiles(t *testing.T) {
	stub := newWebDAVStub()
	stub.files["octopus-backup-20260922-120000.json"] = []byte("{}")
	stub.files["octopus-backup-20260922-130000.json"] = []byte("{}")
	stub.files["my-notes.txt"] = []byte("keep me")
	stub.files["other-backup-20260922.json"] = []byte("{}")
	srv := httptest.NewServer(stub)
	defer srv.Close()

	cfg := WebDAVConfig{URL: srv.URL + "/dav"}
	names, err := WebDAVList(context.Background(), cfg)
	if err != nil {
		t.Fatalf("列出失败: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("应只认出 2 个本程序产生的备份，实得 %v", names)
	}
	if names[0] > names[1] {
		t.Fatalf("应按名字升序（最旧在前），实得 %v", names)
	}
}

// href 前缀的三种写法都要能解析（不同服务端写法不同）。
func TestWebDAVListHandlesHrefPrefixVariants(t *testing.T) {
	for _, variant := range []string{"lower", "upper", "bare"} {
		stub := newWebDAVStub()
		stub.hrefCase = variant
		stub.files["octopus-backup-20260922-120000.json"] = []byte("{}")
		srv := httptest.NewServer(stub)

		cfg := WebDAVConfig{URL: srv.URL + "/dav"}
		names, err := WebDAVList(context.Background(), cfg)
		srv.Close()
		if err != nil {
			t.Fatalf("%s: 列出失败: %v", variant, err)
		}
		if len(names) != 1 {
			t.Fatalf("%s: 应认出 1 个备份，实得 %v", variant, names)
		}
	}
}

// 保留份数生效：超出部分按最旧优先删除。
func TestWebDAVPruneKeepsNewest(t *testing.T) {
	stub := newWebDAVStub()
	for _, n := range []string{
		"octopus-backup-20260920-120000.json",
		"octopus-backup-20260921-120000.json",
		"octopus-backup-20260922-120000.json",
	} {
		stub.files[n] = []byte("{}")
	}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	cfg := WebDAVConfig{URL: srv.URL + "/dav", Keep: 2}
	removed, err := WebDAVPrune(context.Background(), cfg)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if len(removed) != 1 || removed[0] != "octopus-backup-20260920-120000.json" {
		t.Fatalf("应删掉最旧的那份，实得 %v", removed)
	}
	if got := stub.names(); len(got) != 2 {
		t.Fatalf("应剩 2 份，实得 %v", got)
	}
}

// keep<=0 表示不清理（不替用户决定删东西），而不是"删光"。
func TestWebDAVPruneDisabledWhenKeepIsZero(t *testing.T) {
	stub := newWebDAVStub()
	for i := 0; i < 5; i++ {
		stub.files[fmt.Sprintf("octopus-backup-2026092%d-120000.json", i)] = []byte("{}")
	}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	for _, keep := range []int{0, -1} {
		cfg := WebDAVConfig{URL: srv.URL + "/dav", Keep: keep}
		removed, err := WebDAVPrune(context.Background(), cfg)
		if err != nil {
			t.Fatalf("keep=%d 不该报错: %v", keep, err)
		}
		if len(removed) != 0 {
			t.Fatalf("keep=%d 不该删任何文件，实得 %v", keep, removed)
		}
	}
	if got := stub.names(); len(got) != 5 {
		t.Fatalf("文件应一份不少，实得 %v", got)
	}
}

// 清理失败必须作为**错误**返回（供调用方告警），而不是静默吞掉。
func TestWebDAVPruneReportsListFailure(t *testing.T) {
	stub := newWebDAVStub()
	stub.listCode = http.StatusUnauthorized
	srv := httptest.NewServer(stub)
	defer srv.Close()

	cfg := WebDAVConfig{URL: srv.URL + "/dav", Keep: 1}
	if _, err := WebDAVPrune(context.Background(), cfg); err == nil {
		t.Fatalf("PROPFIND 失败时应返回错误")
	}
}

// 端到端一轮：上传 + 清理，且备份本身成功时清理失败不该被当成整体失败。
func TestWebDAVBackupOnceUploadThenPrune(t *testing.T) {
	openWebDAVTestDB(t)
	stub := newWebDAVStub()
	for i := 0; i < 3; i++ {
		stub.files[fmt.Sprintf("octopus-backup-2026092%d-120000.json", i)] = []byte("{}")
	}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	cfg := WebDAVConfig{URL: srv.URL + "/dav", Keep: 2}
	name, removed, err := WebDAVBackupOnce(context.Background(), cfg, time.Now())
	if err != nil {
		t.Fatalf("整体应成功: %v", err)
	}
	if name == "" {
		t.Fatalf("应返回新上传的文件名")
	}
	if len(removed) != 2 {
		t.Fatalf("原有 3 份 + 新 1 份 = 4，保留 2 应删 2，实得 %v", removed)
	}
	if got := stub.names(); len(got) != 2 {
		t.Fatalf("最终应剩 2 份，实得 %v", got)
	}
}

// 上传失败时不该去动远端的旧备份（旧备份是仅存的那份）。
func TestWebDAVBackupOnceSkipsPruneWhenUploadFails(t *testing.T) {
	openWebDAVTestDB(t)
	stub := newWebDAVStub()
	stub.putCode = http.StatusForbidden
	stub.files["octopus-backup-20260920-120000.json"] = []byte("{}")
	srv := httptest.NewServer(stub)
	defer srv.Close()

	cfg := WebDAVConfig{URL: srv.URL + "/dav", Keep: 1}
	name, removed, err := WebDAVBackupOnce(context.Background(), cfg, time.Now())
	if err == nil {
		t.Fatalf("上传失败应返回错误")
	}
	if name != "" || len(removed) != 0 {
		t.Fatalf("上传失败时不该有产出，实得 name=%q removed=%v", name, removed)
	}
	if got := stub.names(); len(got) != 1 {
		t.Fatalf("旧备份必须原样保留，实得 %v", got)
	}
}

// URL 结尾有无斜杠都要能拼对（用户手填，两种写法都会出现）。
func TestWebDAVUploadToleratesTrailingSlash(t *testing.T) {
	openWebDAVTestDB(t)
	stub := newWebDAVStub()
	srv := httptest.NewServer(stub)
	defer srv.Close()

	cfg := WebDAVConfig{URL: srv.URL + "/dav/"}
	if _, err := WebDAVUpload(context.Background(), cfg, time.Now()); err != nil {
		t.Fatalf("带尾斜杠也该成功: %v", err)
	}
	if got := stub.names(); len(got) != 1 {
		t.Fatalf("应上传 1 个文件，实得 %v", got)
	}
}
