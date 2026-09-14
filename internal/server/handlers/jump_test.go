package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openJumpRouter 构造带 create/list/go 三路由的 gin 引擎（跳过 Auth 权限门，
// 权限门归属 middleware.Auth 组合，op 层单测已覆盖令牌语义）。
func openJumpRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/api/v1/account/jump")
	group.POST("/create", createJumpToken)
	group.GET("/list", listJumpTokens)
	group.GET("/go/:token", goJumpToken)
	return engine
}

// plainFromCreate 从 create 响应 JSON 提取一次性明文令牌。
func plainFromCreate(t *testing.T, body string) string {
	t.Helper()
	var ack struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &ack); err != nil || ack.Data.Token == "" {
		t.Fatalf("parse create response: %v body=%s", err, body)
	}
	return ack.Data.Token
}

// swapJumpTestDB 替换全局库为独立内存库并注册恢复，供走 db.GetDB() 的 handler 用。
func swapJumpTestDB(t *testing.T) {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(&model.JumpToken{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	restore := db.SetDBForTest(conn)
	t.Cleanup(restore)
}

func TestCreateJumpTokenAndConsumeViaPage(t *testing.T) {
	swapJumpTestDB(t)
	engine := openJumpRouter(t)
	body := `{"kind":"na","target_url":"https://na.example.com/login"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/account/jump/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	plain := plainFromCreate(t, rec.Body.String())

	goReq := httptest.NewRequest(http.MethodGet, "/api/v1/account/jump/go/"+plain, nil)
	goRec := httptest.NewRecorder()
	engine.ServeHTTP(goRec, goReq)
	if goRec.Code != http.StatusOK {
		t.Fatalf("go status = %d", goRec.Code)
	}
	if !strings.Contains(goRec.Body.String(), `url=https://na.example.com/login`) {
		t.Fatalf("target url missing in page: %s", goRec.Body.String())
	}
	if goRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("jump page must be no-store")
	}
	if goRec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("jump page must forbid referrer (token in URL path)")
	}
	if !strings.Contains(goRec.Body.String(), `name="referrer" content="no-referrer"`) {
		t.Fatalf("jump page must carry no-referrer meta before meta-refresh: %s", goRec.Body.String())
	}

	replay := httptest.NewRequest(http.MethodGet, "/api/v1/account/jump/go/"+plain, nil)
	replayRec := httptest.NewRecorder()
	engine.ServeHTTP(replayRec, replay)
	if replayRec.Code != http.StatusGone {
		t.Fatalf("replay status = %d, want 410", replayRec.Code)
	}
}

func TestCreateJumpTokenRejectsBadTarget(t *testing.T) {
	swapJumpTestDB(t)
	engine := openJumpRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/account/jump/create",
		strings.NewReader(`{"kind":"na","target_url":"javascript:alert(1)"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad target status = %d, want 400", rec.Code)
	}
}

func TestGoJumpTokenNotFoundUnifies(t *testing.T) {
	swapJumpTestDB(t)
	engine := openJumpRouter(t)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/account/jump/go/deadbeef", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token status = %d, want 404", rec.Code)
	}
}

func TestGoJumpTokenEscapesTarget(t *testing.T) {
	swapJumpTestDB(t)
	plain, _, err := op.NewJumpToken(nil, model.JumpTokenKindNA, `https://na.example.com/?a=1&b=<script>`, "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/go/:token", goJumpToken)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/go/"+plain, nil))
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Fatal("target url not escaped")
	}
}
