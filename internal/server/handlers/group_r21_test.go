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

// R21-F1: updateGroup 更新不存在的分组时必须返回 404 (NotFound)，杜绝误报 500 服务器内部错误。
func TestUpdateGroupReturns404WhenNotFound(t *testing.T) {
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

	// 试图更新不存在的分组 (ID=77777)
	name := "non-existent-group"
	reqBody := model.GroupUpdateRequest{
		Name: &name,
	}
	jsonBytes, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "77777"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/group/update/77777", bytes.NewReader(jsonBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	updateGroup(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("更新不存在的分组应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "77777") || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应包含 group ID 和 not found, 实际 body: %s", w.Body.String())
	}
}
