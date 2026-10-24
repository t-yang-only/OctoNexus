package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/t-yang-only/OctoNexus/internal/db"
	"github.com/t-yang-only/OctoNexus/internal/model"
	"github.com/t-yang-only/OctoNexus/internal/op"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// R23-F1: setSetting 设置 model_info_update_interval 时，前置已通过 Validate，
// 内存分发 switch 必须遵循 fail-safe 语义，不应在 SettingSetString 成功落库后再截断回 400，
// 确保响应 200 且数据与内存分发一致。
func TestSetSettingModelInfoUpdateIntervalSucceeds(t *testing.T) {
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

	// 预设数据库行和缓存，以便 SettingSetString 命中
	k := model.SettingKeyModelInfoUpdateInterval
	conn.Create(&model.Setting{Key: k, Value: "12"})
	op.SettingSetStringForTest(k, "12", true)

	settingReq := model.Setting{
		Key:   k,
		Value: "24",
	}
	jsonBytes, _ := json.Marshal(settingReq)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/setting/set", bytes.NewReader(jsonBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	setSetting(c)

	if w.Code != http.StatusOK {
		t.Fatalf("设置有效更新间隔应返回 200 OK, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
}
