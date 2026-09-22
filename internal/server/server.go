package server

import (
	"fmt"
	"net/http"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/op"
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
// 返回错误的原因是受信代理配置非法时必须拒绝启动, 不能静默降级。
func newEngine() (*gin.Engine, error) {
	if conf.IsDebug() {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	// 受信代理必须在路由之前配置：它决定 c.ClientIP() 到底读 TCP 对端还是读
	// X-Forwarded-For。默认（未配置）时 gin 会打印一条警告并信任所有代理，
	// 那等于让任何客户端用转发头伪造来源 IP —— API Key 的 IP 白名单会形同虚设。
	//
	// 这里的口径：设置项为空 → 显式设为 nil，表示「不信任任何代理」，
	// 来源 IP 一律取 TCP 对端；只有用户确实把网关挂在可信反代后面时，
	// 才把该反代的地址填进 trusted_proxies。
	if trusted := op.TrustedProxies(); len(trusted) > 0 {
		if err := r.SetTrustedProxies(trusted); err != nil {
			// 配错就拒绝启动，而不是退化成"信任所有"：后者会把一个配置笔误
			// 变成静默的安全降级。
			return nil, fmt.Errorf("invalid trusted_proxies setting: %w", err)
		}
	} else if err := r.SetTrustedProxies(nil); err != nil {
		return nil, fmt.Errorf("disable trusted proxies: %w", err)
	}
	r.Use(gin.CustomRecovery(func(c *gin.Context, _ any) {
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		c.Abort()
	}))

	if conf.IsDebug() {
		r.Use(middleware.Logger())
	}
	r.Use(middleware.Cors())
	r.Use(middleware.StaticEmbed("/", static.StaticFS))
	return r, nil
}

func Start() error {
	admin, err := newEngine()
	if err != nil {
		return err
	}
	if err := router.RegisterOn(admin, router.ServerAdmin); err != nil {
		return err
	}
	relay, err := newEngine()
	if err != nil {
		return err
	}
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
