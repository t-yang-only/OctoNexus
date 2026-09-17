package cmd

import (
	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/notify"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
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
		shutdown.Listen()
	},
}

func init() {
	startCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ./data/config.json)")
	rootCmd.AddCommand(startCmd)
}
