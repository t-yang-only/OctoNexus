package task

import (
	"context"
	"time"

	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/charmbracelet/log"
)

// routeProbeTimeout 是单轮探活的整体上限: 单次探测自带 20s 上游超时, 这里再兜一层,
// 避免冷却成员很多时后台协程被整轮任务长期占用。
const routeProbeTimeout = 2 * time.Minute

// routeProbeOnce 跑一轮冷却成员主动探活 (R-probe-001)。
// 开关默认关闭且每轮重新读设置: 用户在设置里打开后无需重启,
// 关掉时也只是每轮空转一次, 探活任务本身按周期注册 (0 才摘掉任务)。
func routeProbeOnce() {
	if !op.RouteProbeEnabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), routeProbeTimeout)
	defer cancel()
	probed, recovered := relay.ProbeCoolingMembers(ctx)
	if probed > 0 {
		log.Infof("route probe: probed=%d recovered=%d", probed, recovered)
	}
}
