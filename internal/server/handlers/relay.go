package handlers

import (
	"net/http"

	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/looplj/axonhub/llm"
)

func init() {
	router.NewGroupRouter("/v1").
		ServeOn(router.ServerRelay).
		Use(middleware.APIKeyAuth()).
		AddRoute(
			router.NewRoute("/chat/completions", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAIChatCompletion)),
		).
		AddRoute(
			router.NewRoute("/responses", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAIResponse)),
		).
		AddRoute(
			router.NewRoute("/messages", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatAnthropicMessage)),
		).
		// TypeSafe AI「System One」评估接口的兼容转发（T-verify-002）。
		// 它是自定义形态（state + typed questions → 结构化答案），过不了协议转换层，
		// 因此单独一条路由：复用选路与日志，body 原样透传。
		AddRoute(
			router.NewRoute("/systemone", http.MethodPost).
				Handle(relay.ForwardSystemOne()),
		)
}
