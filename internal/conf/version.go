package conf

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
	Author    = "OctoNexus"
	// Repo 是本项目自己的仓库（T-identity-001 / 需求6）。
	// 它出现在启动横幅、`octopus version` 与面板的版本信息里 —— 指向上游会让用户
	// 按上游的版本号判断"该不该升级"，而本叉的版本号与上游完全不是一套
	//（实测：面板显示"最新版本 v0.14.0"而后端是 v0.78.0，看起来像版本倒退）。
	Repo = "https://github.com/t-yang-only/OctoNexus"
)
