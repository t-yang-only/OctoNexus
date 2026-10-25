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

// R38-F1: setSetting 对不存在的设置项，必须返回 404 (NotFound)，杜绝误报 500 InternalServerError。
func TestSetSettingReturns404WhenKeyNotFound(t *testing.T) {
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

	// 试图设置不存在的配置项 (key="unknown_test_key_r38")
	settingReq := model.Setting{
		Key:   model.SettingKey("unknown_test_key_r38"),
		Value: "some_value",
	}
	jsonBytes, _ := json.Marshal(settingReq)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/setting/set", bytes.NewReader(jsonBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	setSetting(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("设置不存在的配置项应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unknown_test_key_r38") || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应包含 key 名称和 not found, 实际 body: %s", w.Body.String())
	}
}
