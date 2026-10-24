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

// R22-F1: createGroup 创建分组时若引用了不存在的子分组，必须返回 404 (NotFound)，杜绝误报 500 服务器内部错误。
func TestCreateGroupReturns404WhenChildGroupNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.Group{}, &model.GroupItem{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	// 试图创建引用不存在子分组 (ID=99999) 的新分组
	reqBody := model.GroupCreateRequest{
		Name: "test-nesting-group",
		Mode: model.GroupModeManual,
		Items: []model.GroupItemInput{
			{
				ChildGroupID: 99999,
			},
		},
	}
	jsonBytes, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/group/create", bytes.NewReader(jsonBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	createGroup(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("创建引用不存在子分组的新分组应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "99999") || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应包含 child group ID 和 not found, 实际 body: %s", w.Body.String())
	}
}
