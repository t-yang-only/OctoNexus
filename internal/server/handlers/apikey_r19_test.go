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

// R19-F1: updateAPIKey 更新不存在的 Key 时必须返回 404 (NotFound)，杜绝误报 500 服务器内部错误。
func TestUpdateAPIKeyReturns404WhenNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.APIKey{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	// 试图更新不存在的 API Key (ID=99999)
	reqBody := model.APIKey{
		ID:   99999,
		Name: "non-existent-key",
	}
	jsonBytes, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/api_key/update", bytes.NewReader(jsonBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	updateAPIKey(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("更新不存在的 API Key 应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "99999") || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应包含 key ID 和 not found, 实际 body: %s", w.Body.String())
	}
}
