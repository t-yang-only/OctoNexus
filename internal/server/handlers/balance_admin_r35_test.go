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

// R35-F1: balanceChannelSet 人工设置不存在渠道的余额时，必须返回 404 (NotFound)，杜绝误报 500 InternalServerError。
func TestBalanceChannelSetReturns404WhenNotFound(t *testing.T) {
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

	// 试图为不存在的渠道设置人工余额 (ID=99999)
	reqBody := balanceChannelSetRequest{
		Points: 1000,
		Note:   "test-manual-balance",
	}
	jsonBytes, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "99999"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/balance/channel/99999", bytes.NewReader(jsonBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	balanceChannelSet(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("为不存在的渠道设置人工余额应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "99999") || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应包含 channel ID 和 not found, 实际 body: %s", w.Body.String())
	}
}
