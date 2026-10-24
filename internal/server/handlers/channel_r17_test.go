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

// R17-F1: fetchModel 接口在渠道配置了 ProxyNodeID 时，必须优先走该出口节点；
// 若代理节点不存在或未就绪，必须严格 fail-closed 报错（502），绝不能静默直连泄露真实 IP。
func TestFetchModelRespectsProxyNodeID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := conn.AutoMigrate(&model.ProxyNode{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	// 1. 测试未就绪/不存在的 ProxyNodeID (ID=999) -> 必须 fail-closed 返回 502 Bad Gateway
	// 并且返回的错误信息必须来自节点出口解析错误（即 "代理节点不存在"），证明真实进入了 ProxyNodeEndpoint 解析分支
	reqBody := model.ChannelFetchModelRequest{
		Channel: model.ChannelConfig{
			Name:        "test-node-channel",
			BaseURL:     "https://api.example.com",
			ProxyNodeID: 999, // 节点不存在
		},
		Key: "sk-test-key",
	}
	jsonBytes, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/channel/fetch_model", bytes.NewReader(jsonBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	fetchModel(c)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("绑定了不存在节点时应返回 502 BadGateway (fail-closed), 实际状态码: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "代理节点不存在") {
		t.Fatalf("返回内容应明确指出代理节点不存在 (证明优先走节点解析), 实际 body: %s", w.Body.String())
	}
}
