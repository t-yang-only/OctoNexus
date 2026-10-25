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

// R33-F1: updateAlertRule 与 deleteAlertRule 操作不存在的告警规则时，必须返回 404 (NotFound)，杜绝误报 400 Bad Request 或静默成功。
func TestUpdateAlertRuleReturns404WhenNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.AlertRule{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	// 试图更新不存在的告警规则 (ID=66666)
	name := "non-existent-alert-rule"
	reqBody := model.AlertRuleUpdateRequest{
		Name: &name,
	}
	jsonBytes, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "66666"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/alert-rule/update/66666", bytes.NewReader(jsonBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	updateAlertRule(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("更新不存在的告警规则应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "66666") || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应包含 rule ID 和 not found, 实际 body: %s", w.Body.String())
	}
}

func TestDeleteAlertRuleReturns404WhenNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.AlertRule{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	// 试图删除不存在的告警规则 (ID=55555)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "55555"}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/alert-rule/delete/55555", nil)

	deleteAlertRule(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("删除不存在的告警规则应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "55555") || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应包含 rule ID 和 not found, 实际 body: %s", w.Body.String())
	}
}
