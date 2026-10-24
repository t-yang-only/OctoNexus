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

// R31-F1: sub2apiAccountProbe 探测不存在的账号时，必须返回 404 (NotFound)，杜绝误报 502 BadGateway。
func TestSub2APIAccountProbeReturns404WhenNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.Sub2APIAccount{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	// 试图探测不存在的 Sub2API 账号 (ID=11111)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "11111"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/sub2api/accounts/11111/probe", nil)

	sub2apiAccountProbe(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("探测不存在的 Sub2API 账号应返回 404 StatusNotFound, 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "不存在") {
		t.Fatalf("返回信息应明确提示账号不存在, 实际 body: %s", w.Body.String())
	}
}
