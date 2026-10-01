package plugin

// 运行时常量：清单与运转逻辑共用，避免两处各写一遍字面量。
const (
	RuntimeExec = "exec" // octopus 拉起插件进程（唯一能强制其出网的形态）
	RuntimeHTTP = "http" // 指向已经跑着的服务（出口由对方负责，octopus 只能如实说明）

	ProtocolOpenAI    = "openai"
	ProtocolAnthropic = "anthropic"

	EgressPool     = "pool"     // 强制走节点池出口（默认）
	EgressDirect   = "direct"   // 显式声明允许直连真实出口
	EgressExternal = "external" // 只对 runtime=http：出口不在 octopus 手上

	StatusStopped = "stopped"
	StatusRunning = "running"
	StatusExited  = "exited" // 进程自己退了（崩溃或正常结束），下次启动前一直停在这里
	StatusError   = "error"
)
