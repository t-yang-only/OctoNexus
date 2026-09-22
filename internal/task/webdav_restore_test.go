package task

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// T-backup-002 从 WebDAV 恢复的守门逻辑。
//
// 恢复是本模块唯一会改动线上数据的操作，所以判据集中在"什么情况下必须拒绝"：
// 文件名越界、内容不是转储、缺版本号 —— 每一条都对应一种真实的误操作或攻击面。

// 只接受本程序产生的备份文件名，防止一个拼错的参数去下载目录里的别的东西。
func TestIsOwnBackupNameRejectsForeign(t *testing.T) {
	allowed := []string{
		"octopus-backup-20260922-120000.json",
		"octopus-backup-20200101-000000.json",
	}
	for _, name := range allowed {
		if !isOwnBackupName(name) {
			t.Fatalf("%q 应被接受", name)
		}
	}
	rejected := []string{
		"",
		"other.json",
		"octopus-backup-20260922-120000.txt",
		"octopus-backup-20260922-120000.json.bak",
		// 路径穿越：必须挡住
		"../../../etc/passwd",
		"octopus-backup-../../secret.json",
		"octopus-backup-x/../y.json",
		"dir/octopus-backup-20260922-120000.json",
		"octopus-backup-20260922-120000.json/../../x",
	}
	for _, name := range rejected {
		if isOwnBackupName(name) {
			t.Fatalf("%q 应被拒绝（不得逃出备份目录）", name)
		}
	}
}

// 下载时对越界文件名必须直接拒绝，不发请求。
func TestWebDAVDownloadRejectsForeignName(t *testing.T) {
	stub := newWebDAVStub()
	stub.files["../../../etc/passwd"] = []byte("root:x:0:0")
	srv := newStubServer(t, stub)

	cfg := WebDAVConfig{URL: srv + "/dav"}
	for _, name := range []string{"../../../etc/passwd", "random.json", ""} {
		if _, err := WebDAVDownload(context.Background(), cfg, name); err == nil {
			t.Fatalf("%q 应被拒绝", name)
		}
	}
}

// 正常下载：内容应与上传时一致（往返无损）。
func TestWebDAVDownloadRoundTrip(t *testing.T) {
	openWebDAVTestDB(t)
	stub := newWebDAVStub()
	srv := newStubServer(t, stub)

	cfg := WebDAVConfig{URL: srv + "/dav"}
	name, err := WebDAVUpload(context.Background(), cfg, time.Now())
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	raw, err := WebDAVDownload(context.Background(), cfg, name)
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	if string(raw) != string(stub.file(name)) {
		t.Fatalf("下载内容与远端不一致（长度 %d vs %d）", len(raw), len(stub.file(name)))
	}
	var dump model.DBDump
	if err := json.Unmarshal(raw, &dump); err != nil {
		t.Fatalf("下载内容应是合法转储: %v", err)
	}
}

// 远端 404 要如实报错，不能当成"空备份"继续走恢复。
func TestWebDAVDownloadFailsOn404(t *testing.T) {
	stub := newWebDAVStub()
	srv := newStubServer(t, stub)

	cfg := WebDAVConfig{URL: srv + "/dav"}
	if _, err := WebDAVDownload(context.Background(), cfg, "octopus-backup-20200101-000000.json"); err == nil {
		t.Fatalf("远端不存在时应报错")
	} else if !contains(err.Error(), "404") {
		t.Fatalf("错误信息应带状态码，实得: %v", err)
	}
}

// **内容不是转储 JSON 时必须拒绝**：这个函数在恢复路径上，
// 放行一个坏文件等于让用户在不知情的情况下继续往下走。
func TestWebDAVRestoreRejectsNonDumpContent(t *testing.T) {
	openWebDAVTestDB(t)
	stub := newWebDAVStub()
	stub.files["octopus-backup-20200101-000000.json"] = []byte("this is not json")
	srv := newStubServer(t, stub)

	cfg := WebDAVConfig{URL: srv + "/dav"}
	if _, err := WebDAVRestore(context.Background(), cfg, "octopus-backup-20200101-000000.json"); err == nil {
		t.Fatalf("非 JSON 内容应被拒绝")
	}
}

// **缺版本号时必须拒绝**：版本号是格式判据，没有它无法确认这份备份的字段含义。
//
// 反例：放行会让"字段含义对不上"的旧备份被静默导入，
// 表现为部分配置丢失，且看不出是导入造成的。
func TestWebDAVRestoreRejectsMissingVersion(t *testing.T) {
	openWebDAVTestDB(t)
	payload, _ := json.Marshal(map[string]any{"channels": []any{}})
	stub := newWebDAVStub()
	stub.files["octopus-backup-20200101-000000.json"] = payload
	srv := newStubServer(t, stub)

	cfg := WebDAVConfig{URL: srv + "/dav"}
	_, err := WebDAVRestore(context.Background(), cfg, "octopus-backup-20200101-000000.json")
	if err == nil {
		t.Fatalf("缺版本号应被拒绝")
	}
	if !contains(err.Error(), "版本号") {
		t.Fatalf("错误信息应说明缺版本号，实得: %v", err)
	}
}

// 合法备份能恢复成功，并返回导入结果（这是正向路径的兜底断言）。
func TestWebDAVRestoreSucceedsOnValidBackup(t *testing.T) {
	openWebDAVTestDB(t)
	stub := newWebDAVStub()
	srv := newStubServer(t, stub)

	cfg := WebDAVConfig{URL: srv + "/dav"}
	name, err := WebDAVUpload(context.Background(), cfg, time.Now())
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	result, err := WebDAVRestore(context.Background(), cfg, name)
	if err != nil {
		t.Fatalf("恢复应成功: %v", err)
	}
	if result == nil {
		t.Fatalf("应返回导入结果")
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

// newStubServer 起一个 WebDAV 桩并返回它的 URL。
func newStubServer(t *testing.T, stub *webdavStub) string {
	t.Helper()
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)
	return srv.URL
}
