package cmd

import (
	"context"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/notify"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/plugin"
	"github.com/bestruirui/octopus/internal/poolstore"
	"github.com/bestruirui/octopus/internal/proxycore"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/secret"
	"github.com/bestruirui/octopus/internal/server"
	"github.com/bestruirui/octopus/internal/task"
	"github.com/bestruirui/octopus/internal/utils/shutdown"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
)

var cfgFile string

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start " + conf.APP_NAME,
	PreRun: func(cmd *cobra.Command, args []string) {
		conf.PrintBanner()
		conf.Load(cfgFile)
		if level, err := log.ParseLevel(conf.AppConfig.Log.Level); err == nil {
			log.SetLevel(level)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		shutdown.Init(log.Default())
		if err := db.InitDB(conf.AppConfig.Database.Type, conf.AppConfig.Database.Path, conf.IsDebug()); err != nil {
			log.Errorf("database init error: %v", err)
			return
		}
		shutdown.Register(db.Close)

		if err := op.InitCache(); err != nil {
			log.Errorf("cache init error: %v", err)
			return
		}
		shutdown.Register(op.SaveCache)

		// 恢复上一次运行遗留的路由冷却（T-route-003）：冷却是"上游说别来了"的记账，
		// 原先只存在进程内存里，每次重启都会把全部冷却中的成员一次性放行。
		// 放在 InitCache 之后（分组缓存已就绪）、启动转发之前。
		relay.RestoreRouteCooldowns(context.Background())

		// 凭据静态加密的状态说清楚（R-sec-001）：启用时给出来源（环境变量/密钥文件），
		// 未启用时明确告诉运维"当前是明文"，免得以为已经加密了。
		if source := op.CredentialKeySource(); source != "" {
			log.Infof("渠道凭据静态加密已启用（密钥来源：%s）", source)
		} else {
			log.Warnf("渠道凭据静态加密未启用：channel_keys.key 以明文落库。设置 OCTOPUS_OFFICIAL_KEY 或允许在数据目录写入 %s 即可启用",
				secret.KeyFileName)
		}
		// 备份导入可能带回来旧版本的明文凭据：落地即加密，不等下一次启动的迁移。
		if sealed, err := secret.SealLegacyChannelKeys(db.GetDB()); err != nil {
			log.Warnf("渠道凭据补加密失败（不影响启动）：%v", err)
		} else if sealed > 0 {
			log.Infof("渠道凭据补加密：%d 行明文已转为密文", sealed)
		}

		// 加权轮询热路径开关按设置注入 relay (T-route-002 L5 装配层): 默认关闭,
		// 设置缺失/解析失败一律按关闭处理; 运行期变更由 setting 接口热注入。
		if enabled, err := op.SettingGetBool(model.SettingKeyRouteBalanceEnabled); err == nil && enabled {
			relay.SetRouteBalanceEnabled(true)
		}

		// 上游判定"请求本身非法"时的取向按设置注入 relay (T-retry-003): 读不到/取值异常时
		// relay 内部回落 failover（与既有行为一致），运行期变更由 setting 接口热注入。
		if action, err := op.SettingGetString(model.SettingKeyRequestFaultAction); err == nil {
			relay.SetRequestFaultAction(action)
		}

		// 告警 webhook 的设置读取源注入 notify 包 (避免 notify→op 导入环)。
		notify.SetSettingSource(op.SettingGetString)

		// 号池声明式适配器 (R-pool-ext-001 第三批): 先按设置注入域名白名单, 再把已存的适配器装回注册表。
		// 单个适配器失效（白名单被收紧、站点改了路径）只记日志, 不阻塞启动——号池是排查现场的地方,
		// 一个接不上的第三方工具包不该让整个实例起不来。
		if loaded, failures := poolstore.LoadAll(); len(failures) > 0 {
			log.Warnf("pool declarative adapters: 已装载 %d 个, %d 个失败", loaded, len(failures))
			for _, failure := range failures {
				log.Warnf("pool declarative adapter: %v", failure)
			}
		} else if loaded > 0 {
			log.Infof("pool declarative adapters: 已装载 %d 个", loaded)
		}

		if err := op.UserInit(); err != nil {
			log.Errorf("user init error: %v", err)
			return
		}

		if err := server.Start(); err != nil {
			log.Errorf("server start error: %v", err)
			return
		}
		shutdown.Register(server.Close)

		task.Init()
		go task.RUN()

		// R-proxy-001 代理内核自启：有启用节点且开关打开时把 mihomo 拉起来（后台做，失败只告警不阻断启动）。
		// 缺二进制、端口冲突这类问题不该让整个服务起不来，但必须在日志里说清楚。
		go func() {
			status := proxycore.Default().Status()
			if !status.BinaryOK {
				log.Warnf("proxy core: 未启动（%s）", status.LastError)
				return
			}
			if !proxycore.AutostartEnabled() {
				log.Infof("proxy core: 自动启动已关闭（proxy_core_autostart=false）")
				return
			}
			if status.Listeners == 0 {
				log.Infof("proxy core: 没有启用中的出口节点，未启动")
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := proxycore.Default().Start(ctx); err != nil {
				log.Warnf("proxy core: 启动失败：%v", err)
				return
			}
			after := proxycore.Default().Status()
			log.Infof("proxy core: 已启动 pid=%d 出口入站=%d 端口池=%v", after.PID, after.Listeners, after.Ports)
		}()

		shutdown.Register(func() error {
			proxycore.Default().Stop()
			return nil
		})

		// R-plugin-001 社区反代插件自启：标记了 auto_start 的插件随实例一起拉起。
		// 与内核同一取向：缺出口、清单坏掉只告警（插件没起来不影响 octopus 本身提供服务）。
		// 注意顺序——插件的出网要向内核要出口，所以放在内核自启之后（同步等待内核起来再拉插件，
		// 否则"启动瞬间内核还没分配端口"会被判成出口未就绪，插件白白启动失败一次）。
		go func() {
			time.Sleep(3 * time.Second)
			plugin.Autostart(context.Background())
		}()

		shutdown.Register(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			rows, err := plugin.List(ctx)
			if err != nil {
				return nil
			}
			for _, row := range rows {
				if running, _ := plugin.IsRunning(row.Slug); running {
					_ = plugin.Stop(ctx, row.Slug)
				}
			}
			return nil
		})
		shutdown.Listen()
	},
}

func init() {
	startCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ./data/config.json)")
	rootCmd.AddCommand(startCmd)
}
