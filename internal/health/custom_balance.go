package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	CustomBalanceDefaultTimeout = 30 * time.Second
	CustomBalanceMaxBodyBytes   = 1 << 20
)

// CustomBalanceConfig describes a read-only JSON balance endpoint. Paths use dot
// notation (for example, "data.remaining"); credentials are sent as a bearer token.
type CustomBalanceConfig struct {
	Endpoint      string
	Token         string
	Timeout       time.Duration
	QuotaPath     string
	UsedPath      string
	RemainingPath string
}

func (c CustomBalanceConfig) Validate() error {
	if strings.TrimSpace(c.Endpoint) == "" {
		return errors.New("balance endpoint is required")
	}
	if c.Timeout < 0 {
		return errors.New("balance timeout cannot be negative")
	}
	if strings.TrimSpace(c.QuotaPath) == "" && strings.TrimSpace(c.UsedPath) == "" && strings.TrimSpace(c.RemainingPath) == "" {
		return errors.New("at least one balance JSON path is required")
	}
	for name, path := range map[string]string{"quota": c.QuotaPath, "used": c.UsedPath, "remaining": c.RemainingPath} {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, err := splitJSONPath(path); err != nil {
			return fmt.Errorf("%s path: %w", name, err)
		}
	}
	return nil
}

// FetchCustomBalance fetches and parses a configured JSON endpoint. It never
// follows an unbounded response body and does not mutate channel state.
func FetchCustomBalance(ctx context.Context, cfg CustomBalanceConfig) (BalanceSnapshot, error) {
	if err := cfg.Validate(); err != nil {
		return BalanceSnapshot{}, err
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = CustomBalanceDefaultTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, cfg.Endpoint, nil)
	if err != nil {
		return BalanceSnapshot{}, err
	}
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return BalanceSnapshot{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return BalanceSnapshot{}, fmt.Errorf("balance endpoint returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, CustomBalanceMaxBodyBytes+1))
	if err != nil {
		return BalanceSnapshot{}, err
	}
	if len(body) > CustomBalanceMaxBodyBytes {
		return BalanceSnapshot{}, fmt.Errorf("balance response exceeds %d bytes", CustomBalanceMaxBodyBytes)
	}
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return BalanceSnapshot{}, fmt.Errorf("invalid balance JSON: %w", err)
	}
	snap := BalanceSnapshot{}
	var ok bool
	if snap.Quota, ok, err = numberAtPath(raw, cfg.QuotaPath); err != nil {
		return BalanceSnapshot{}, fmt.Errorf("quota path: %w", err)
	}
	if cfg.QuotaPath != "" && !ok {
		return BalanceSnapshot{}, errors.New("quota path did not contain a number")
	}
	if snap.Used, ok, err = numberAtPath(raw, cfg.UsedPath); err != nil {
		return BalanceSnapshot{}, fmt.Errorf("used path: %w", err)
	}
	if cfg.UsedPath != "" && !ok {
		return BalanceSnapshot{}, errors.New("used path did not contain a number")
	}
	if snap.Remaining, ok, err = numberAtPath(raw, cfg.RemainingPath); err != nil {
		return BalanceSnapshot{}, fmt.Errorf("remaining path: %w", err)
	}
	if cfg.RemainingPath != "" && !ok {
		return BalanceSnapshot{}, errors.New("remaining path did not contain a number")
	}
	if cfg.RemainingPath == "" && cfg.QuotaPath == "" {
		// 只剩"已用"单字段时无从推知总额/剩余（推导会得到负剩余，
		// 下游归零停用会误判），与 ParseBalancePayload 同口径视为采集失败。
		return BalanceSnapshot{}, errors.New("cannot infer remaining from used-only paths")
	}
	if cfg.RemainingPath == "" {
		snap.Remaining = snap.Quota - snap.Used
	}
	if cfg.QuotaPath == "" {
		snap.Quota = snap.Used + snap.Remaining
	}
	if snap.Remaining < 0 {
		// 上游把已用报得比总额还大（超额/脏数据）时归零而非透传负数，
		// 负剩余会让阈值与归零停用判定语义漂移；按"已耗尽"处理。
		snap.Remaining = 0
	}
	return snap, nil
}

func splitJSONPath(path string) ([]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	parts := strings.Split(path, ".")
	for _, part := range parts {
		if part == "" || part == ".." {
			return nil, errors.New("must be dot-separated non-empty fields")
		}
	}
	return parts, nil
}

func numberAtPath(root any, path string) (float64, bool, error) {
	parts, err := splitJSONPath(path)
	if err != nil {
		return 0, false, err
	}
	if len(parts) == 0 {
		return 0, false, nil
	}
	value := root
	for _, part := range parts {
		object, ok := value.(map[string]any)
		if !ok {
			return 0, false, nil
		}
		value, ok = object[part]
		if !ok {
			return 0, false, nil
		}
	}
	switch number := value.(type) {
	case float64:
		return number, true, nil
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil, err
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
		return parsed, err == nil, err
	default:
		return 0, false, nil
	}
}
