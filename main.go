package main

import "github.com/t-yang-only/OctoNexus/cmd"

// Version v0.79.0
//
// 上面那一行是**版本号的唯一源头**（构建链的两端都读它，不许各写各的）：
//   - 前端：web/vite.config.ts 读这一行注入 VITE_APP_VERSION，面板拿它跟后端版本比对；
//   - 后端：发布构建用 ldflags 把同一个值写进 conf.Version。
//
// 两处不同步的后果实测过：后端已到 v0.78.0 而这里停在 v0.14.0，面板于是报
// "前端版本 (v0.14.0) 与后端版本 (v0.78.0) 不一致，可能是浏览器缓存问题" ——
// 用户可以强制刷新一百次也不会消失，因为那不是缓存问题，是版本号有两个源头。

func main() {
	cmd.Execute()
}
