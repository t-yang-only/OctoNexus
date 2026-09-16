package pool

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// officialKind 是官方账号池的适配器标识（R-acct-001/T-pool-002 已有的那套后端）。
const officialKind = "official"

func init() {
	// 内置适配器在 init 注册：号池开箱就有官方账号这一种后端，外部适配器按同样方式追加。
	// 注册失败只能是我们自己写错了常量，直接 panic 让启动期暴露，不留一个静默少了一种号池后端的进程。
	if err := Register(officialAdapter{}); err != nil {
		panic(fmt.Sprintf("register pool adapter %q: %v", officialKind, err))
	}
}

// officialAdapter 把既有官方账号池投影成统一视图。
//
// 它只读：数据来自 op.OfficialPoolStatusList（账号 → 渠道凭据的既有映射），
// 因此本适配器不新增表、不改选路，纯粹是"既有实现的一种投影"。
type officialAdapter struct{}

func (officialAdapter) Info() AdapterInfo {
	return AdapterInfo{
		Kind:  officialKind,
		Title: "官方账号池",
		// 能力位只声明真的有的：探活是 op.OfficialAccountReadUsage、刷新与物化是 op.OfficialPoolSync、
		// 新建是 authorize/callback 流程。人工启停（toggle）走 op.SetChannelKeyEnabled —— 它把
		// "运维要不要用这条凭据"与"账号当前能不能用"分开记，所以号池同步不会撤销人工停用。
		Capabilities: []Capability{CapList, CapGet, CapProbe, CapRefresh, CapToggle, CapProvision, CapSync},
		Builtin:      true,
		Since:        "R-pool-ext-001",
		Fields: []FieldSpec{
			{Name: "provider", Type: "string", Label: "服务商", Required: true},
			{Name: "external_name", Type: "string", Label: "官方侧账号标识", Required: true},
			{Name: "access_token", Type: "string", Label: "访问凭据", Required: true, Secret: true},
			{Name: "refresh_token", Type: "string", Label: "刷新凭据", Secret: true},
			{Name: "plan_tier", Type: "string", Label: "套餐档位"},
		},
	}
}

func (officialAdapter) Entries(ctx context.Context) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	statuses, err := op.OfficialPoolStatusList(nil)
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, 8)
	for _, status := range statuses {
		for _, member := range status.Members {
			entries = append(entries, Entry{
				Kind:      officialKind,
				ID:        fmt.Sprintf("%s:%d", status.Provider, member.AccountID),
				Name:      member.ExternalName,
				Provider:  string(status.Provider),
				Status:    string(member.Status),
				Enabled:   member.KeyEnabled,
				Healthy:   member.Healthy,
				PlanTier:  member.PlanTier,
				ExpiresAt: member.ExpiresAt,
				LastError: member.LastError,
				Labels: map[string]string{
					"provider": string(status.Provider),
					"channel":  status.ChannelName,
				},
				Detail: map[string]any{
					"account_id":        member.AccountID,
					"channel_id":        status.ChannelID,
					"channel_name":      status.ChannelName,
					"key_name":          member.KeyName,
					"key_exists":        member.KeyExists,
					"key_enabled":       member.KeyEnabled,
					"operator_disabled": member.KeyOperatorDisabled,
					"window_5h":         member.Window5H,
					"window_7d":         member.Window7D,
					"models":            status.Models,
					"grants":            status.Grants,
					"is_active":         member.Status == model.OfficialAccountStatusActive,
					"account_total":     status.Accounts,
					"active_keys":       status.ActiveKeys,
				},
			})
		}
	}
	return entries, nil
}

// ============================ 第二批：可选能力实现 ============================
//
// 全部复用既有 op，不新增表、不改选路：
//   get     ← 与 Entries 同源（统一视图里挑那一条）
//   probe   ← op.OfficialAccountReadUsage（读官方侧套餐/窗口/健康快照）
//   refresh ← op.OfficialPoolSync（临期 token 换新 + 重新物化凭据）
//   sync    ← op.OfficialPoolSync（三个服务商逐个同步）

// officialEntryID 拆解条目 ID（<provider>:<account_id>），三者都要能定位到具体账号。
func officialEntryID(id string) (model.OfficialAccountProvider, int, error) {
	provider, rawID, found := strings.Cut(id, ":")
	if !found {
		return "", 0, fmt.Errorf("%w: %s (want <provider>:<account_id>)", ErrEntryNotFound, id)
	}
	if err := model.ValidateOfficialAccountProvider(model.OfficialAccountProvider(provider)); err != nil {
		return "", 0, fmt.Errorf("%w: %s", ErrEntryNotFound, id)
	}
	accountID, err := strconv.Atoi(rawID)
	if err != nil || accountID <= 0 {
		return "", 0, fmt.Errorf("%w: %s", ErrEntryNotFound, id)
	}
	return model.OfficialAccountProvider(provider), accountID, nil
}

// Get 取单条：与统一视图同源，避免"列表与详情两套口径"。
func (a officialAdapter) Get(ctx context.Context, id string) (Entry, error) {
	if _, _, err := officialEntryID(id); err != nil {
		return Entry{}, err
	}
	entries, err := a.Entries(ctx)
	if err != nil {
		return Entry{}, err
	}
	for _, entry := range entries {
		if entry.ID == id {
			return entry, nil
		}
	}
	return Entry{}, fmt.Errorf("%w: %s", ErrEntryNotFound, id)
}

// Probe 读一次官方侧快照并把结果并回条目。
//
// 失败也回条目：探活的意义就是"告诉你这条现在什么状态"，所以错误与条目一起返回，
// 调用方按 error 判断结论、按 Entry 展示状态（接口层会把它渲染成一条 warning + 当前快照）。
func (a officialAdapter) Probe(ctx context.Context, id string) (Entry, error) {
	provider, accountID, err := officialEntryID(id)
	if err != nil {
		return Entry{}, err
	}
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}

	account, readErr := op.OfficialAccountReadUsage(nil, accountID)
	// 无论成功失败都先取一次当前视图：探活失败时也能给出"现在是什么状态"。
	entry, entryErr := a.Get(ctx, id)
	if entryErr != nil && readErr != nil {
		return Entry{
			Kind: officialKind, ID: id, Name: id, Provider: string(provider),
			Status: "unknown", LastError: readErr.Error(),
		}, readErr
	}
	if readErr != nil {
		entry.LastError = readErr.Error()
		entry.Healthy = false
		return entry, fmt.Errorf("probe %s: %w", id, readErr)
	}
	if entryErr != nil {
		entry = Entry{Kind: officialKind, ID: id, Name: account.ExternalName, Provider: string(account.Provider)}
	}
	// 用读回来的最新快照覆盖：探活的价值就在于"刚读到的"。
	entry.Status = string(account.Status)
	entry.Healthy = account.Healthy
	entry.PlanTier = account.PlanTier
	entry.ExpiresAt = account.ExpiresAt
	entry.LastError = account.LastError
	if entry.Detail == nil {
		entry.Detail = map[string]any{}
	}
	entry.Detail["window_5h"] = account.Window5H
	entry.Detail["window_7d"] = account.Window7D
	entry.Detail["account_id"] = account.ID
	entry.Detail["is_active"] = account.Status == model.OfficialAccountStatusActive
	return entry, nil
}

// Refresh 刷新该账号所属服务商的号池凭据（临期 token 换新并重新物化），再回读这一条。
func (a officialAdapter) Refresh(ctx context.Context, id string) (Entry, error) {
	provider, _, err := officialEntryID(id)
	if err != nil {
		return Entry{}, err
	}
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	result, err := op.OfficialPoolSync(nil, provider)
	if err != nil {
		return Entry{}, fmt.Errorf("refresh %s: %w", id, err)
	}
	entry, getErr := a.Get(ctx, id)
	if getErr != nil {
		// 刷新本身成功了但账号已不在池子里（例如被删）：如实说明，不假装刷到了。
		return Entry{}, fmt.Errorf("refresh %s: %w", id, getErr)
	}
	if entry.Detail == nil {
		entry.Detail = map[string]any{}
	}
	entry.Detail["refreshed"] = result.Refreshed
	entry.Detail["keys"] = result.Keys
	entry.Detail["disabled"] = result.Disabled
	if len(result.Notes) > 0 {
		entry.Detail["notes"] = result.Notes
	}
	return entry, nil
}

// SetEnabled 人工启停某个账号对应的凭据（toggle），再回读这一条。
//
// 与号池同步的分工：同步回答"账号此刻能不能用"，这里回答"运维要不要用"。
// 停用会同时落下"人工停用"标记，所以下一次同步不会把它改回启用（见 op.SetChannelKeyEnabled）。
// 账号存在但还没物化出凭据时回 ErrConflict（409）：等同步跑完再来，而不是当成"这条不存在"。
func (a officialAdapter) SetEnabled(ctx context.Context, id string, enabled bool) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	provider, accountID, err := officialEntryID(id)
	if err != nil {
		return Entry{}, err
	}

	// 账号 → 渠道凭据的映射与列表同源（op.OfficialPoolStatusList），避免启停走另一套口径。
	statuses, err := op.OfficialPoolStatusList(nil)
	if err != nil {
		return Entry{}, err
	}
	for _, status := range statuses {
		if status.Provider != provider {
			continue
		}
		for _, member := range status.Members {
			if member.AccountID != accountID {
				continue
			}
			if status.ChannelID <= 0 || member.KeyName == "" {
				return Entry{}, fmt.Errorf(
					"%w: 账号 %s 尚未物化出渠道凭据，先同步号池再启停", ErrConflict, member.ExternalName)
			}
			found, err := op.SetChannelKeyEnabled(nil, status.ChannelID, member.KeyName, enabled)
			if err != nil {
				return Entry{}, fmt.Errorf("set enabled=%v for %s: %w", enabled, id, err)
			}
			if !found {
				return Entry{}, fmt.Errorf("%w: 凭据 %s", ErrEntryNotFound, member.KeyName)
			}
			return a.Get(ctx, id)
		}
	}
	return Entry{}, fmt.Errorf("%w: %s", ErrEntryNotFound, id)
}

// Sync 把三个服务商的官方账号逐个物化到转发层，汇总成一份结论。
//
// 逐个同步而不是"一次全同步"：某个服务商出问题（例如某个账号凭据解不开）不应该让另外两个也白跑。
func (a officialAdapter) Sync(ctx context.Context) (SyncReport, error) {
	report := SyncReport{Kind: officialKind}
	providers := []model.OfficialAccountProvider{
		model.OfficialAccountProviderOpenAI,
		model.OfficialAccountProviderGemini,
		model.OfficialAccountProviderClaude,
	}
	var firstErr error
	for _, provider := range providers {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		result, err := op.OfficialPoolSync(nil, provider)
		if err != nil {
			report.Notes = append(report.Notes, fmt.Sprintf("%s 同步失败：%v", provider, err))
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		report.Entries += result.Keys
		report.Notes = append(report.Notes, fmt.Sprintf(
			"%s：启用凭据 %d 条，停用 %d 条，刷新 %d 条，模型 %d 个，授权 %d 条",
			provider, result.Keys, result.Disabled, result.Refreshed, result.Models, result.Grants))
		report.Notes = append(report.Notes, result.Notes...)
	}
	// 部分失败不整体失败：结论里已经写明哪个服务商失败，调用方据此判断（与"一个后端坏了不打没整张表"同一考虑）。
	return report, firstErr
}
