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

// R26-F1: toggleModelMapping 与 updateModelMapping 操作不存在的模型映射时，必须返回 404 (NotFound)，杜绝误报 400 Bad Request。
func TestToggleModelMappingReturns404WhenNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.ModelMapping{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	// 试图切换不存在的模型映射 (ID=55555) 启用状态
	reqBody := struct {
		Enabled bool `json:"enabled"`
	}{
		Enabled: true,
	}
	jsonBytes, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "55555"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/model-mapping/toggle/55555", bytes.NewReader(jsonBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	toggleModelMapping(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("切换不存在的模型映射应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "55555") || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应包含 mapping ID 和 not found, 实际 body: %s", w.Body.String())
	}
}
