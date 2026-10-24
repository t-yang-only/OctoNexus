package handlers

import (
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

// R27-F1: deleteModelMapping 删除不存在的模型映射时，必须返回 404 (NotFound)，杜绝误报 400 Bad Request。
func TestDeleteModelMappingReturns404WhenNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.ModelMapping{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "44444"}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/model-mapping/delete/44444", nil)

	deleteModelMapping(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("删除不存在的模型映射应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "44444") || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("返回信息应包含 mapping ID 和 not found, 实际 body: %s", w.Body.String())
	}
}
