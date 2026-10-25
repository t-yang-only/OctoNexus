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
	"github.com/t-yang-only/OctoNexus/internal/op"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// R37-F1: groupQuickCreate 在引用的渠道不存在时，必须返回 404 (NotFound)，杜绝误报 400 Bad Request。
func TestGroupQuickCreateReturns404WhenChannelNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.Channel{}, &model.Group{}, &model.GroupItem{}, &model.ChannelGrant{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	// 试图为不存在的渠道 (ChannelID=66666) 一键创建分组
	reqBody := op.QuickGroupRequest{
		ChannelID: 66666,
		ModelName: "gpt-4o",
	}
	jsonBytes, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/group/quick-create", bytes.NewReader(jsonBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	groupQuickCreate(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("引用不存在的渠道一键建组应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "66666") || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应包含 channel ID 和 not found, 实际 body: %s", w.Body.String())
	}
}

// R37-F2: getGroup / verifyChannelModels / checkChannelUpstream 查询或实测不存在资源时返回 404。
func TestGetGroupReturns404WhenNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.Group{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "88888"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/group/get/88888", nil)

	getGroup(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("查询不存在的分组应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
}

func TestVerifyChannelModelsReturns404WhenChannelNotFound(t *testing.T) {
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

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "99999"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/channel/99999/verify-models", nil)

	verifyChannelModels(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("实测不存在的渠道模型应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
}
