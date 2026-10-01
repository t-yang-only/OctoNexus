package router

import (
	"github.com/gin-gonic/gin"
)

// ServerKind 区分同一进程内的两个监听端口:
// admin 承载管理面板与 /api 管理接口, relay 只承载 /v1 模型转发。
type ServerKind int

const (
	ServerAdmin ServerKind = iota
	ServerRelay
)

// GroupRouter represents a group of routes with shared path prefix and middlewares
type GroupRouter struct {
	Path        string
	Routes      []*Route
	Middlewares []gin.HandlerFunc
	Servers     []ServerKind
}

// Global registry for route groups
var registeredRouters []*GroupRouter

// NewGroupRouter creates a new GroupRouter with the given path and automatically registers it.
// 默认挂载到双端口: 管理路由与转发路由调用方按需改 Servers。
func NewGroupRouter(path string) *GroupRouter {
	router := &GroupRouter{
		Path:    path,
		Routes:  make([]*Route, 0),
		Servers: []ServerKind{ServerAdmin, ServerRelay},
	}
	registeredRouters = append(registeredRouters, router)
	return router
}

// ServeOn 限定该路由组只挂载到指定端口, 未调用则保持默认双端口。
func (g *GroupRouter) ServeOn(kinds ...ServerKind) *GroupRouter {
	g.Servers = kinds
	return g
}

// Use adds middlewares to the group.
func (g *GroupRouter) Use(middlewares ...gin.HandlerFunc) *GroupRouter {
	g.Middlewares = append(g.Middlewares, middlewares...)
	return g
}
