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

// R40-F1: previewUsageReport 传入未知/非法 period 时必须返回 400 Bad Request（杜绝静默降级为 daily）。
func TestPreviewUsageReportRejectsInvalidPeriod(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.Setting{}, &model.UsageHourly{}, &model.StatsDaily{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	// 试图传入非法周期 "invalid_biweekly"
	reqBody := map[string]string{
		"period": "invalid_biweekly",
	}
	jsonBytes, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/usage-report/preview", bytes.NewReader(jsonBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	previewUsageReport(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("传入非法周期应返回 400 StatusBadRequest, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid period") {
		t.Fatalf("返回信息应提示 invalid period, 实际 body: %s", w.Body.String())
	}
}

// R40-F2: usageReportHistory 校验 limit 参数边界。
func TestUsageReportHistoryRejectsInvalidLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.UsageReportState{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/usage-report/history?limit=500", nil)

	usageReportHistory(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("limit 越界(>100)应返回 400 StatusBadRequest, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
}
