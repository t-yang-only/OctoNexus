package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/t-yang-only/OctoNexus/internal/db"
	"github.com/t-yang-only/OctoNexus/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// R34-F1: pluginUpdate / pluginRemove 操作不存在的插件时，必须返回 404 (NotFound)，杜绝误报 400 Bad Request 或空删除误报成功。
func TestPluginUpdateReturns404WhenNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.Plugin{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	// 试图更新不存在的插件 (slug="non-existent-plugin")
	enabled := true
	reqBody := model.PluginUpdateRequest{
		Enabled: &enabled,
	}
	jsonBytes, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "slug", Value: "non-existent-plugin"}}
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/plugin/non-existent-plugin", bytes.NewReader(jsonBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	pluginUpdate(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("更新不存在的插件应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "non-existent-plugin") || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应包含 plugin slug 和 not found, 实际 body: %s", w.Body.String())
	}
}

func TestPluginRemoveReturns404WhenNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.Plugin{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	// 试图删除不存在的插件
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "slug", Value: "ghost-plugin"}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/plugin/ghost-plugin", nil)

	pluginRemove(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("删除不存在的插件应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ghost-plugin") || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应包含 plugin slug 和 not found, 实际 body: %s", w.Body.String())
	}
}
