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

// R20-F1: updateChannel 更新不存在的渠道时必须返回 404 (NotFound)，杜绝误报 500 服务器内部错误。
func TestUpdateChannelReturns404WhenNotFound(t *testing.T) {
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

	// 试图更新不存在的渠道 (ID=88888)
	var reqBody model.ChannelDetail
	reqBody.ID = 88888
	reqBody.Name = "non-existent-channel"
	reqBody.BaseURL = "https://api.example.com"

	jsonBytes, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/channel/update", bytes.NewReader(jsonBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	updateChannel(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("更新不存在的渠道应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "88888") || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应包含 channel ID 和 not found, 实际 body: %s", w.Body.String())
	}
}
