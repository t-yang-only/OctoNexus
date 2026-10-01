package router

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSplitPorts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ResetRegistry()

	NewGroupRouter("/api/v1/ping").
		ServeOn(ServerAdmin).
		AddRoute(NewRoute("/ping", http.MethodGet).Handle(func(c *gin.Context) {}))
	NewGroupRouter("/v1").
		ServeOn(ServerRelay).
		AddRoute(NewRoute("/chat/completions", http.MethodPost).Handle(func(c *gin.Context) {}))

	admin := gin.New()
	if err := RegisterOn(admin, ServerAdmin); err != nil {
		t.Fatalf("register admin: %v", err)
	}
	adminRoutes := admin.Routes()
	if len(adminRoutes) != 1 || adminRoutes[0].Path != "/api/v1/ping/ping" {
		t.Fatalf("admin routes = %+v, want only /api/v1/ping/ping", adminRoutes)
	}

	ResetRegistry()
	NewGroupRouter("/api/v1/ping").
		ServeOn(ServerAdmin).
		AddRoute(NewRoute("/ping", http.MethodGet).Handle(func(c *gin.Context) {}))
	NewGroupRouter("/v1").
		ServeOn(ServerRelay).
		AddRoute(NewRoute("/chat/completions", http.MethodPost).Handle(func(c *gin.Context) {}))

	relay := gin.New()
	if err := RegisterOn(relay, ServerRelay); err != nil {
		t.Fatalf("register relay: %v", err)
	}
	relayRoutes := relay.Routes()
	if len(relayRoutes) != 1 || relayRoutes[0].Path != "/v1/chat/completions" {
		t.Fatalf("relay routes = %+v, want only /v1/chat/completions", relayRoutes)
	}
	ResetRegistry()
}
