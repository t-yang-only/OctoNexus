package op

import (
	"context"
	"fmt"
	"time"
)

func InitCache() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := settingRefreshCache(ctx); err != nil {
		return fmt.Errorf("setting refresh cache error: %v", err)
	}
	if err := channelRefreshCache(ctx); err != nil {
		return fmt.Errorf("channel refresh cache error: %v", err)
	}
	if err := groupRefreshCache(ctx); err != nil {
		return fmt.Errorf("group refresh cache error: %v", err)
	}
	if err := apiKeyRefreshCache(ctx); err != nil {
		return fmt.Errorf("api key refresh cache error: %v", err)
	}
	if err := llmRefreshCache(ctx); err != nil {
		return fmt.Errorf("llm refresh cache error: %v", err)
	}
	if err := statsRefreshCache(ctx); err != nil {
		return fmt.Errorf("stats refresh cache error: %v", err)
	}
	// 手动订阅（R-acct-004）：它是总余额的一部分，必须与其它缓存一起就位，
	// 否则重启后的第一份快照会少算用户手录的余额。
	if err := ManualSubscriptionRefresh(ctx); err != nil {
		return fmt.Errorf("manual subscription refresh cache error: %v", err)
	}
	// 模型名智能重写：转发链路上按请求名找分组之前要读它，因此必须与其它缓存一起就位；
	// 未配规则时缓存为空，行为与没有本机制时逐字一致。
	if err := ModelMappingRefresh(ctx); err != nil {
		return fmt.Errorf("model mapping refresh cache error: %v", err)
	}
	return nil
}

func SaveCache() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := StatsSaveDB(ctx); err != nil {
		return err
	}
	return nil
}
