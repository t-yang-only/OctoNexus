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

// R25-F1: enableChannel 切换不存在渠道的启停状态时，必须返回 404 (NotFound)，杜绝误报 500 服务器内部错误。
func TestEnableChannelReturns404WhenNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.Channel{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	// 试图切换不存在的渠道 (ID=66666) 启用状态
	reqBody := struct {
		ID      int  `json:"id"`
		Enabled bool `json:"enabled"`
	}{
		ID:      66666,
		Enabled: true,
	}
	jsonBytes, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/channel/enable", bytes.NewReader(jsonBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	enableChannel(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("启停不存在的渠道应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "66666") || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应包含 channel ID 和 not found, 实际 body: %s", w.Body.String())
	}
}
