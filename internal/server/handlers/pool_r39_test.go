package handlers

import (
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

// R39-F1: poolEntries 查询未知 kind 时必须返回 404 (NotFound)，杜绝误报 400 Bad Request。
func TestPoolEntriesReturns404WhenKindUnknown(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.Channel{}, &model.Setting{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/pool/entries?kind=unknown_pool_kind_r39", nil)

	poolEntries(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("查询未知 kind 列表应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unknown") && !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应说明未知 kind, 实际 body: %s", w.Body.String())
	}
}

// R39-F2: poolAdapterGet / poolAdapterRemove 对不存在的声明式适配器返回 404。
func TestPoolAdapterGetReturns404WhenNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "kind", Value: "custom-ghost-adapter"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/pool/adapters/custom-ghost-adapter", nil)

	poolAdapterGet(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("查询不存在的声明式适配器应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
}

func TestPoolAdapterRemoveReturns404WhenNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "kind", Value: "custom-ghost-adapter"}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/pool/adapters/custom-ghost-adapter", nil)

	poolAdapterRemove(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("删除不存在的声明式适配器应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
}
