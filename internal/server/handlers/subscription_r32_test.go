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

// R32-F1: updateManualSubscription 与 deleteManualSubscription 操作不存在的手动订阅时，必须返回 404 (NotFound)，杜绝误报 400 Bad Request。
func TestUpdateManualSubscriptionReturns404WhenNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.ManualSubscription{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	// 试图更新不存在的手动订阅 (ID=88888)
	reqBody := model.ManualSubscription{
		ID:   88888,
		Name: "non-existent-manual-sub",
	}
	jsonBytes, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/subscription/update", bytes.NewReader(jsonBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	updateManualSubscription(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("更新不存在的手动订阅应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "88888") || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应包含 subscription ID 和 not found, 实际 body: %s", w.Body.String())
	}
}

func TestDeleteManualSubscriptionReturns404WhenNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.ManualSubscription{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	// 试图删除不存在的手动订阅 (ID=77777)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "77777"}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/subscription/delete/77777", nil)

	deleteManualSubscription(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("删除不存在的手动订阅应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "77777") || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应包含 subscription ID 和 not found, 实际 body: %s", w.Body.String())
	}
}
