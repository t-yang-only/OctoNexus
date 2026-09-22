package task

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/notify"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/charmbracelet/log"
)

// T-backup-001 WebDAV 云备份的定时入口。
//
// 「上次成功时间」故意**只放内存**，不落库：
// 重启后应该尽快补一次备份，而不是因为"上次是 23 小时前成功的、还差 1 小时"而继续等。
// 本机刚刚重启过本身就是需要尽快备份的信号（可能是崩溃自愈，也可能是刚部署完改了配置）。
// 代价是频繁重启会多传几份，而备份件很小、且按保留份数自动清理，这个代价可以接受。

var (
	webDAVLastSuccess time.Time
	webDAVMu          sync.Mutex
)

// webDAVInterval 读上传间隔；非法或缺失时按默认 24 小时（与设置默认值一致）。
func webDAVInterval() time.Duration {
	raw, err := op.SettingGetString(model.SettingKeyWebDAVInterval)
	if err != nil {
		return 24 * time.Hour
	}
	hours, err := parseIntSetting(raw)
	if err != nil || hours < 1 {
		return 24 * time.Hour
	}
	return time.Duration(hours) * time.Hour
}

// WebDAVBackupDue 判断现在是否该跑一轮备份。
//
// 三层判断缺一不可：
//  1. enabled 开关关着 → 不跑；
//  2. 地址没配 → 不跑（否则会每隔几分钟刷一条"未配置"日志）；
//  3. 距上次成功不足一个间隔 → 不跑。
func WebDAVBackupDue(now time.Time) bool {
	enabled, err := op.SettingGetString(model.SettingKeyWebDAVEnabled)
	if err != nil || enabled != "true" {
		return false
	}
	url, err := op.SettingGetString(model.SettingKeyWebDAVURL)
	if err != nil || url == "" {
		return false
	}

	webDAVMu.Lock()
	last := webDAVLastSuccess
	webDAVMu.Unlock()

	if last.IsZero() {
		return true
	}
	return now.Sub(last) >= webDAVInterval()
}

// webDAVBackupOnce 是注册给定时器的一轮执行体。
//
// 失败时**发通知**：备份是最后一道防线，静默失败等于没有备份。
// 但只在真的失败时发，不做"很久没备份"的二次提醒（那会在配置错误时反复打扰）。
func webDAVBackupOnce() {
	if !WebDAVBackupDue(time.Now()) {
		return
	}
	ctx := context.Background()
	cfg, err := WebDAVConfigFromSettings()
	if err != nil {
		log.Warnf("webdav backup skipped: %v", err)
		return
	}

	// WebDAVBackupOnce 的返回值语义：name 为空表示**上传失败**（此时 err 非空）；
	// name 非空时上传已成功，err 若有值则只代表清理失败。
	name, removed, err := WebDAVBackupOnce(ctx, cfg, time.Now())
	if name == "" {
		if err == nil {
			err = errors.New("上传未返回文件名")
		}
		log.Warnf("webdav backup failed: %v", err)
		notifyFailure(err)
		return
	}
	if err != nil {
		// 备份成功、只是清理失败：记日志，不当成本次失败，
		// 否则用户会以为没有备份（实际有），从而重复排查。
		log.Warnf("webdav prune failed (backup itself succeeded): %v", err)
	}

	webDAVMu.Lock()
	webDAVLastSuccess = time.Now()
	webDAVMu.Unlock()

	log.Infof("webdav backup uploaded: %s (pruned %d old file(s))", name, len(removed))
}

// notifyFailure 把备份失败推给已配置的通知渠道。
func notifyFailure(cause error) {
	if cause == nil {
		cause = errors.New("未知原因")
	}
	notify.Send(context.Background(), notify.Event{
		Type:    "backup_failure",
		Title:   "OctoNexus 云备份失败",
		Message: "WebDAV 备份未成功，请检查地址、凭据与网络：" + cause.Error(),
		At:      time.Now(),
	})
}

// WebDAVBackupNow 立即跑一轮（面板「立即备份」用），并刷新"上次成功时间"。
//
// 与定时路径共用同一套实现：面板上试通了、定时就一定能跑通，
// 避免"手动可以、定时不行"这类只能靠读代码才发现的分歧。
func WebDAVBackupNow(ctx context.Context) (string, []string, error) {
	cfg, err := WebDAVConfigFromSettings()
	if err != nil {
		return "", nil, err
	}
	name, removed, pruneErr := WebDAVBackupOnce(ctx, cfg, time.Now())
	if name != "" {
		webDAVMu.Lock()
		webDAVLastSuccess = time.Now()
		webDAVMu.Unlock()
	}
	return name, removed, pruneErr
}
