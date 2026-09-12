package server

import (
	"fmt"
	"net/http"

	"github.com/bestruirui/octopus/internal/conf"
	_ "github.com/bestruirui/octopus/internal/server/handlers"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/static"
	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
)

var adminSrv http.Server
var relaySrv http.Server

// newEngine 建一个 gin 引擎: 双端口共用同一套中间件与静态面板,
// 区别只在 router.RegisterOn 按 ServerKind 挂载不同的路由组。
func newEngine() *gin.Engine {
	if conf.IsDebug() {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.CustomRecovery(func(c *gin.Context, _ any) {
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		c.Abort()
	}))

	if conf.IsDebug() {
		r.Use(middleware.Logger())
	}
	r.Use(middleware.Cors())
	r.Use(middleware.StaticEmbed("/", static.StaticFS))
	return r
}

func Start() error {
	admin := newEngine()
	if err := router.RegisterOn(admin, router.ServerAdmin); err != nil {
		return err
	}
	relay := newEngine()
	if err := router.RegisterOn(relay, router.ServerRelay); err != nil {
		return err
	}

	adminSrv.Addr = fmt.Sprintf("%s:%d", conf.AppConfig.Server.Host, conf.AppConfig.Server.AdminPort)
	adminSrv.Handler = admin
	go func() {
		if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("admin server listen and serve error: %v", err)
		}
	}()

	relaySrv.Addr = fmt.Sprintf("%s:%d", conf.AppConfig.Server.Host, conf.AppConfig.Server.RelayPort)
	relaySrv.Handler = relay
	go func() {
		if err := relaySrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("relay server listen and serve error: %v", err)
		}
	}()
	return nil
}

func Close() error {
	if err := adminSrv.Close(); err != nil {
		return err
	}
	return relaySrv.Close()
}
