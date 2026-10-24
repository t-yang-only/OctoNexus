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

// R29-F1: proxyNodeUpdate 与 proxyNodeDelete 操作不存在的代理节点时，必须返回 404 (NotFound)，杜绝误报 400 Bad Request。
func TestProxyNodeUpdateReturns404WhenNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.ProxyNode{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	// 试图更新不存在的代理节点 (ID=33333)，填充完整必填项
	reqBody := model.ProxyNodeInput{
		Name:   "non-existent-node",
		Type:   "ss",
		Server: "1.1.1.1",
		Port:   8388,
	}
	jsonBytes, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "33333"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/proxy/nodes/update/33333", bytes.NewReader(jsonBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	proxyNodeUpdate(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("更新不存在的代理节点应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "33333") || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应包含 node ID 和 not found, 实际 body: %s", w.Body.String())
	}
}
