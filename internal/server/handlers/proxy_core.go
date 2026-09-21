package handlers

import (
	"net/http"
	"strconv"

	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/proxycore"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// R-proxy-001 内核控制面：把节点池里的节点变成可以真正使用的本地出口。
//
// 为什么放在这一层：内核是**进程级资源**（一个进程、一套端口），管理动作（启/停/同步端口）
// 必须能被人看见与干预；而"哪个账号走哪个节点"是数据，走 /proxy/nodes 的绑定字段。
func init() {
	router.NewGroupRouter("/api/v1/proxy/core").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/status", http.MethodGet).Handle(proxyCoreStatus),
		).
		AddRoute(
			router.NewRoute("/start", http.MethodPost).Handle(proxyCoreStart),
		).
		AddRoute(
			router.NewRoute("/stop", http.MethodPost).Handle(proxyCoreStop),
		).
		AddRoute(
			router.NewRoute("/sync", http.MethodPost).Handle(proxyCoreSync),
		).
		AddRoute(
			router.NewRoute("/ports", http.MethodPost).Handle(proxyCorePorts),
		)
}

func proxyCoreStatus(c *gin.Context) {
	resp.Success(c, proxycore.Default().Status())
}

func proxyCoreStart(c *gin.Context) {
	if err := proxycore.Default().Start(c.Request.Context()); err != nil {
		resp.Error(c, http.StatusBadGateway, err.Error())
		return
	}
	resp.Success(c, proxycore.Default().Status())
}

func proxyCoreStop(c *gin.Context) {
	proxycore.Default().Stop()
	resp.Success(c, proxycore.Default().Status())
}

// proxyCoreSync 按节点池现状重建配置并重启内核（导入/启停节点后调它）。
func proxyCoreSync(c *gin.Context) {
	if err := proxycore.Default().Sync(c.Request.Context()); err != nil {
		resp.Error(c, http.StatusBadGateway, err.Error())
		return
	}
	resp.Success(c, proxycore.Default().Status())
}

// proxyCorePorts 只做端口分配/回收，不起内核（想先看"分到哪些端口"时用）。
func proxyCorePorts(c *gin.Context) {
	assigned, err := proxycore.AllocatePorts()
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	released, err := proxycore.ReleaseDisabledPorts()
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	start, end := proxycore.PortRange()
	resp.Success(c, gin.H{"assigned": assigned, "released": released, "port_start": start, "port_end": end})
}

// proxyNodeProbeExit 经某个节点的本地出口探测出口 IP —— 这是"两个账号是不是真的走不同出口"的可见证据。
func proxyNodeProbeExit(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid node id")
		return
	}
	ip, err := proxycore.ProbeNode(c.Request.Context(), id)
	if err != nil {
		resp.Error(c, http.StatusBadGateway, err.Error())
		return
	}
	_ = op.ProxyNodeMarkProbe(nil, id, ip, true, "")
	resp.Success(c, gin.H{"exit_ip": ip, "probe_url": proxycore.ProbeURL()})
}
