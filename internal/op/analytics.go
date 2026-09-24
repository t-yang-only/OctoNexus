package op

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// T-insight-002 模型调用分析（总览监控）。
//
// ## 补的是哪个盲区
//
// 项目已有的统计都是**单一维度**的：故障率按渠道分（fault-stats）、耗时按分组分
// （latency）、尝试链按轮次聚合（attempt-stats）。唯独没有一面"整体现在怎么样"的镜子：
// 一个窗口里发了多少请求、花了多少钱、成功率和延迟的健康线在哪、时间线上谁在吃 token。
//
// 同类的 new-api 把这一页做成"模型调用分析"（指标卡 + 健康条 + 消耗分布堆叠图），
// 本实现在口径上与本项目既有统计**复用同一套判断**，不另起炉灶：
//
//	失败按 fault_kind 分桶     —— 请求非法不计入渠道健康度（T-usability-008）
//	window 取最近 N 条而非 N 天 —— 部署后流量差异极大，按天会出现"最近一天只有 3 条"
//	ratio 返回百分比、分母 0 给 0 —— 不给 NaN
//
// ## RPM/TPM 为什么按"窗口跨度"而不是"窗口长度"
//
// 用户看到的总是"最近 N 条请求"这个切片，它的实际时间跨度由流量决定：
// 5 分钟发满 500 条与 3 天发满 500 条，RPM 相差三个数量级。若硬按 1 小时或 1 天算，
// 得到的数在两种情况下都是错的。因此跨度取**首尾请求的实际时间差**，
// 并在结果里一并给出（SpanSeconds）—— 让"RPM 0.2"这种看起来离谱的数可以自查。

// DimensionUsageStat 是**某一个维度值**在窗口内的用量画像（按模型 / 按客户端 / 按渠道）。
//
// 为什么一个类型服务三个维度而不是各写一个：三者的字段与口径完全一致
// （都是"这个 key 打了多少请求、花了多少钱、平均多慢"），各写一份只会让
// 同一个统计逻辑分叉成三处、日后改一处漏两处。维度之间真正的差异只有
// **空值该显示成什么**（模型未指定的原因与渠道未路由的原因不是一回事），
// 而那由调用方传入，不体现在类型上。
type DimensionUsageStat struct {
	Model string `json:"model"`
	// Requests 是打向该维度的请求条数（含失败）。
	Requests int64 `json:"requests"`
	Success  int64 `json:"success"`
	// SuccessRate 成功 / 全部，百分比；分母为 0 时给 0。
	SuccessRate      float64 `json:"success_rate"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	CachedTokens     int64   `json:"cached_tokens"`
	// ReasoningTokens 是上游回报的思考 token 合计（不报的行贡献 0）。
	ReasoningTokens int64   `json:"reasoning_tokens"`
	Cost            float64 `json:"cost"`
	// AvgDurationMs 是该维度请求的平均总耗时（含失败的），便于横向比"谁快谁慢"。
	AvgDurationMs float64 `json:"avg_duration_ms"`
}

// AnalyticsBucket 是时间线上的一个分桶，用于堆叠图。
type AnalyticsBucket struct {
	// Bucket 是桶标签：小时内为 "HH:00"，跨天为 "MM/DD"。
	Bucket   string `json:"bucket"`
	BucketAt int64  `json:"bucket_at"` // 桶起始的 Unix 秒，供前端排序/本地化。
	Requests int64  `json:"requests"`
	// Success 是桶内成功条数 —— 只给 Requests 的话，图上"量涨了"分不清是好事还是故障。
	Success int64   `json:"success"`
	Tokens  int64   `json:"tokens"`
	Cost    float64 `json:"cost"`
	// ByModel 是桶内按模型拆分的 token 数，堆叠图的每个色块即一项。
	//
	// 只保留窗口内的前若干模型（见 relayAnalyticsTopModels），其余并进 "__other__"，
	// 否则一个几百模型的实例会把图例撑爆、每根柱子变成几百个看不见的碎片。
	ByModel map[string]int64 `json:"by_model"`
	// ByModelCost 是桶内按模型拆分的花费，与 ByModel 同构。
	//
	// 为什么 token 与成本要各拆一份、不能用同一张图：两者回答的问题不同 ——
	// "谁在吃 token" 看的是量，"钱花在哪" 看的是账。同一个模型可能量很大但因为
	// 缓存命中而便宜，也可能量很小但单价高。只给 token 拆分，成本就只能看到一个总数，
	// 而这恰恰是"这钱该不该花"最需要的那一维。
	//
	// 与 ByModel 用同一套收敛（尾部并进 "__other__"），否则切到成本口径时
	// 总量会对不上（图上的柱子加起来不等于 Bucket.Cost）。
	ByModelCost map[string]float64 `json:"by_model_cost"`
}

// AnalyticsOverview 是"模型调用分析"的总览。
type AnalyticsOverview struct {
	// Window 是实际参与统计的条数（可能少于请求的 window，如库里不够）。
	Window int64 `json:"window"`
	// SpanSeconds 是首尾请求的实际时间差，即 RPM/TPM 的分母（见本文件顶部说明）。
	SpanSeconds float64 `json:"span_seconds"`

	RequestCount int64   `json:"request_count"`
	SuccessCount int64   `json:"success_count"`
	SuccessRate  float64 `json:"success_rate"`
	// ChannelRate 排除"请求本身非法"，回答"上游本身健康吗"（口径同 fault-stats）。
	ChannelRate float64 `json:"channel_rate"`
	// Canceled / Fault* / Unclassified 与 fault-stats 同源，便于这一页独立说明成功率的构成。
	Canceled       int64 `json:"canceled"`
	RequestFault   int64 `json:"request_fault"`
	MemberFault    int64 `json:"member_fault"`
	TransientFault int64 `json:"transient_fault"`
	Unclassified   int64 `json:"unclassified"`

	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	CachedTokens     int64 `json:"cached_tokens"`
	ReasoningTokens  int64 `json:"reasoning_tokens"`
	// CacheHitRate 缓存命中占输入的比例（百分比）。无输入时 0。
	CacheHitRate float64 `json:"cache_hit_rate"`

	TotalCost float64 `json:"total_cost"`
	// AvgCostPerRequest 便于与"总花费"分开看：总量受窗口影响，均值才是单次成本。
	AvgCostPerRequest float64 `json:"avg_cost_per_request"`

	AvgDurationMs float64 `json:"avg_duration_ms"`
	// ThroughputTps 是窗口内的整体吞吐（输出 token ÷ 总耗时秒数之和）。
	// 用"耗时之和"而不是"窗口跨度"：后者含用户不发请求的空档，会把吞吐稀释成无意义的低值。
	ThroughputTps float64 `json:"throughput_tps"`
	AvgRpm        float64 `json:"avg_rpm"`
	AvgTpm        float64 `json:"avg_tpm"`

	// Models 按请求数倒序（我常用的排在前面），便于一眼看到主力模型。
	Models []DimensionUsageStat `json:"models"`
	// APIKeys 是按客户端 Key 聚合的同一份用量，按请求数倒序。
	//
	// 与 Models 并列而不是复用：模型维度回答"哪个模型在吃资源"，
	// 客户端维度回答"哪个调用方在花我的钱"——同一批请求的两种切法，
	// 缺任一都答不出对方的问题（一个 Key 可以在很多模型上花钱）。
	APIKeys []DimensionUsageStat `json:"api_keys"`
	// Channels 是按实际上游渠道聚合的同一份用量，按请求数倒序。
	//
	// 注意它用的是**实际命中的渠道**（target_channel），不是客户端请求的分组名：
	// 分组是逻辑入口，渠道才是真正花钱的地方，两者在 failover 后并不相同。
	Channels []DimensionUsageStat `json:"channels"`
	// Series 按时间升序。
	Series []AnalyticsBucket `json:"series"`
	// Truncated 标记统计的条数达到了 window 上限 —— 此时"总数"是最近 N 条而非全部历史。
	Truncated bool `json:"truncated"`
}

// relayAnalyticsTopModels 是堆叠图保留的模型数上限，其余并进 "__other__"。
const relayAnalyticsTopModels = 8

// hourBucketMaxSpanSeconds 是"仍按整点分桶"的跨度上限，达到它改用按天聚合。
//
// 为什么必须自适应：固定按整点切时，一个跨越几天的窗口会让同一天的 24 个桶
// 全部标成同一个 "MM/DD"，横轴出现重复刻度，图上分不清先后（实测踩过）。
// 24 小时以内按整点则天然唯一 —— 一个整点不会在 24 小时内出现两次。
const hourBucketMaxSpanSeconds = 24 * 3600

// analyticsDimensions 把三个维度的聚合容器打包。
//
// 打成结构而不是三个并列参数：finish 的形参已经在往上涨，
// 而三个维度是**同一件事的三种切法**，本该作为一个整体传递。
type analyticsDimensions struct {
	models   map[string]*DimensionUsageStat
	apiKeys  map[string]*DimensionUsageStat
	channels map[string]*DimensionUsageStat
}

// dimensionKey 归一化维度键：空值给一个该维度专用的显示名。
//
// 三个维度传进来的 fallback 刻意不同（见调用处）——"没填模型"是客户端的问题、
// "没有 Key"是未认证调用、"没有渠道"是请求压根没走到上游。这三种情况
// 处置动作完全不同，糊成同一个"(未指定)"会让人查不下去。
func dimensionKey(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// accumulateDimension 把一行日志累加进它在某个维度上的桶（三个维度共用同一份口径）。
func accumulateDimension(store map[string]*DimensionUsageStat, key string, row model.RelayLog) {
	stat, ok := store[key]
	if !ok {
		stat = &DimensionUsageStat{Model: key}
		store[key] = stat
	}
	stat.Requests++
	if row.Status == "success" {
		stat.Success++
	}
	stat.PromptTokens += row.PromptTokens
	stat.CompletionTokens += row.CompletionToks
	stat.TotalTokens += row.PromptTokens + row.CompletionToks
	stat.CachedTokens += row.CachedTokens
	stat.ReasoningTokens += row.ReasoningTokens
	stat.Cost += row.Cost
	// 平均耗时用总量累加、最后再除：逐行算平均会在浮点上反复截断。
	stat.AvgDurationMs += float64(row.DurationMs)
}

// analyticsAccumulator 是遍历日志过程中攒下的标量，避免 finish 的形参越拉越长。
type analyticsAccumulator struct {
	durationSumMs           int64
	completionForThroughput int64
	// firstAt / lastAt 是**真实**的首尾请求时间，用于算窗口跨度。
	// 不能用桶键算：桶键被截断到整点或整天后会把跨度算短（按天聚合时最多短一整天），
	// RPM 会因此偏大。
	firstAt time.Time
	lastAt  time.Time
}

// otherModelKey 是"其余模型"的合并键。用双下划线包起来，
// 与真实模型名区分开（上游模型名不会长这样），前端据此显示本地化的"其他"。
const otherModelKey = "__other__"

// AnalyticsOverviewStats 汇总窗口内的模型调用情况。
func AnalyticsOverviewStats(ctx context.Context, window int) (AnalyticsOverview, error) {
	if window <= 0 {
		window = 500
	}
	conn := db.GetDB()
	var rows []model.RelayLog
	if err := conn.WithContext(ctx).
		Order("id DESC").Limit(window).
		Find(&rows).Error; err != nil {
		return AnalyticsOverview{}, err
	}

	var out AnalyticsOverview
	out.Window = int64(len(rows))
	// 取满 window 条说明"最近 N 条"之外可能还有更多 —— 此时总数是切片不是全量，
	// 界面必须说明，否则用户会把"最近 500 条"读成"历史全部"。
	out.Truncated = len(rows) >= window
	if len(rows) == 0 {
		// 空窗口返回空结果而不是错误：新装的实例本来就一条都没有，
		// 报错会让首页显示成"出问题了"，而事实是"还没有数据"。
		out.Models = []DimensionUsageStat{}
		out.APIKeys = []DimensionUsageStat{}
		out.Channels = []DimensionUsageStat{}
		out.Series = []AnalyticsBucket{}
		return out, nil
	}

	dims := analyticsDimensions{
		models:   make(map[string]*DimensionUsageStat),
		apiKeys:  make(map[string]*DimensionUsageStat),
		channels: make(map[string]*DimensionUsageStat),
	}
	buckets := make(map[int64]*AnalyticsBucket)
	var acc analyticsAccumulator

	for _, row := range rows {
		applyAnalyticsRow(&out, row)
		acc.durationSumMs += row.DurationMs
		acc.completionForThroughput += row.CompletionToks
		if acc.firstAt.IsZero() || row.StartedAt.Before(acc.firstAt) {
			acc.firstAt = row.StartedAt
		}
		if row.StartedAt.After(acc.lastAt) {
			acc.lastAt = row.StartedAt
		}

		// 三个维度各自归档。空值都要有归属（绝不并进某个真实值，否则
		// "模型 X 的用量"会凭空多出别人的量），但**每个维度的空含义不同**，
		// 所以显示名也不同：没填模型是客户端的问题、没有 Key 是未认证调用、
		// 没有渠道是请求压根没走到上游。统一叫"(未指定)"会把三种完全不同的
		// 情况糊成一个，读表的人就查不下去了。
		modelName := dimensionKey(row.Model, "(未指定)")
		apiKeyName := dimensionKey(row.APIKeyName, "(未认证)")
		channelName := dimensionKey(row.TargetChannel, "(未路由)")
		accumulateDimension(dims.models, modelName, row)
		accumulateDimension(dims.apiKeys, apiKeyName, row)
		accumulateDimension(dims.channels, channelName, row)

		// 时间桶：按整点切。
		hourStart := row.StartedAt.Truncate(time.Hour)
		key := hourStart.Unix()
		bucket, ok := buckets[key]
		if !ok {
			bucket = &AnalyticsBucket{
				BucketAt:    key,
				ByModel:     map[string]int64{},
				ByModelCost: map[string]float64{},
			}
			buckets[key] = bucket
		}
		bucket.Requests++
		if row.Status == "success" {
			bucket.Success++
		}
		bucket.Tokens += row.PromptTokens + row.CompletionToks
		bucket.Cost += row.Cost
		bucket.ByModel[modelName] += row.PromptTokens + row.CompletionToks
		bucket.ByModelCost[modelName] += row.Cost
	}

	finishAnalyticsOverview(&out, dims, buckets, acc)
	return out, nil
}

// applyAnalyticsRow 把一行日志累计进全局维度（与 fault-stats 同一套分桶口径）。
func applyAnalyticsRow(out *AnalyticsOverview, row model.RelayLog) {
	var counts RelayLogFaultCounts
	applyFaultCount(&counts, row)
	out.SuccessCount += counts.Success
	out.Canceled += counts.Canceled
	out.RequestFault += counts.RequestFault
	out.MemberFault += counts.MemberFault
	out.TransientFault += counts.TransientFault
	out.Unclassified += counts.Unclassified

	out.PromptTokens += row.PromptTokens
	out.CompletionTokens += row.CompletionToks
	out.TotalTokens += row.PromptTokens + row.CompletionToks
	out.CachedTokens += row.CachedTokens
	out.ReasoningTokens += row.ReasoningTokens
	out.TotalCost += row.Cost
}

// finishAnalyticsOverview 收尾：算比例、截时间跨度、排序、合并尾部模型。
func finishAnalyticsOverview(
	out *AnalyticsOverview,
	dims analyticsDimensions,
	rawBuckets map[int64]*AnalyticsBucket,
	acc analyticsAccumulator,
) {
	total := out.SuccessCount + out.Canceled + out.RequestFault + out.MemberFault + out.TransientFault + out.Unclassified
	out.RequestCount = total
	out.SuccessRate = ratio(out.SuccessCount, total)
	out.ChannelRate = ratio(out.SuccessCount, out.SuccessCount+out.MemberFault+out.TransientFault)
	out.CacheHitRate = ratio(out.CachedTokens, out.PromptTokens)
	if total > 0 {
		out.AvgCostPerRequest = out.TotalCost / float64(total)
	}
	if total > 0 {
		out.AvgDurationMs = float64(acc.durationSumMs) / float64(total)
	}
	// 吞吐的分母是"真正在跑请求的耗时之和"，不是窗口跨度：
	// 后者含用户不发请求的空档，会把吞吐稀释成看起来不可用的低值。
	if acc.durationSumMs > 0 {
		out.ThroughputTps = float64(acc.completionForThroughput) / (float64(acc.durationSumMs) / 1000)
	}

	// 三个维度走**同一套收尾**：先算比率与均值、再按请求数倒序。
	// 各写一份的话，日后改了口径（比如换了排序键）只会改到其中一两个，
	// 界面上就会出现在一个视角排得好、换到另一个视角顺序奇怪的情况。
	out.Models = finishDimension(dims.models)
	out.APIKeys = finishDimension(dims.apiKeys)
	out.Channels = finishDimension(dims.channels)

	// 时间线粒度自适应：桶键跨度 24 小时以内按整点（一个整点不会在 24 小时内
	// 出现两次，标签天然唯一），达到 24 小时就按天聚合 —— 否则同一天的 24 个桶
	// 会全部标成同一个 "MM/DD"，横轴出现重复刻度。
	rawKeys := sortedAnalyticsKeys(rawBuckets)
	daily := len(rawKeys) > 1 && rawKeys[len(rawKeys)-1]-rawKeys[0] >= hourBucketMaxSpanSeconds
	buckets := rawBuckets
	if daily {
		buckets = rollUpAnalyticsBuckets(rawBuckets, rawKeys)
	}
	keys := sortedAnalyticsKeys(buckets)
	for _, k := range keys {
		b := buckets[k]
		at := time.Unix(k, 0)
		if daily {
			b.Bucket = fmt.Sprintf("%02d/%02d", int(at.Month()), at.Day())
		} else {
			b.Bucket = fmt.Sprintf("%02d:00", at.Hour())
		}
		out.Series = append(out.Series, *b)
	}

	// RPM/TPM 的跨度取**真实的首尾请求时间差**，不是桶键差：
	// 桶键被截断到整点或整天后会把跨度算短（按天聚合时最多短一整天），RPM 会偏大。
	if !acc.firstAt.IsZero() {
		spanSeconds := acc.lastAt.Sub(acc.firstAt).Seconds() + 3600 // 至少覆盖最后一个桶
		out.SpanSeconds = spanSeconds
		minutes := spanSeconds / 60
		if minutes > 0 {
			out.AvgRpm = float64(out.RequestCount) / minutes
			out.AvgTpm = float64(out.TotalTokens) / minutes
		}
	}

	trimAnalyticsSeries(out.Series, out.Models)
}

// sortedAnalyticsKeys 返回升序排列的桶键。
func sortedAnalyticsKeys(buckets map[int64]*AnalyticsBucket) []int64 {
	keys := make([]int64, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// rollUpAnalyticsBuckets 把整点桶按自然日合并。
//
// 只在整点桶跨度达到 24 小时时调用：此时继续按整点画图，横轴上会连续出现
// 二十几个同名日期刻度。合并时两个按模型拆分的 map 都要一并累加，
// 保证堆叠图在任一维度（token / 成本）上的总量都仍对得上。
func rollUpAnalyticsBuckets(raw map[int64]*AnalyticsBucket, rawKeys []int64) map[int64]*AnalyticsBucket {
	rolled := make(map[int64]*AnalyticsBucket, len(raw))
	for _, k := range rawKeys {
		src := raw[k]
		at := time.Unix(k, 0)
		dayStart := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, at.Location())
		dayKey := dayStart.Unix()
		target, ok := rolled[dayKey]
		if !ok {
			target = &AnalyticsBucket{
				BucketAt:    dayKey,
				ByModel:     make(map[string]int64, len(src.ByModel)),
				ByModelCost: make(map[string]float64, len(src.ByModelCost)),
			}
			rolled[dayKey] = target
		}
		target.Requests += src.Requests
		target.Success += src.Success
		target.Tokens += src.Tokens
		target.Cost += src.Cost
		for name, tokens := range src.ByModel {
			target.ByModel[name] += tokens
		}
		for name, cost := range src.ByModelCost {
			target.ByModelCost[name] += cost
		}
	}
	return rolled
}

// trimAnalyticsSeries 把堆叠图的模型数收敛到 relayAnalyticsTopModels，其余并进 __other__。
//
// 为什么必须收敛：实例里可能有几百个模型（本项目实测 358 个），
// 允许全部上色会让图例不可读、每根柱子碎成几百条看不见的线，图反而失去信息。
// 合并而不是丢弃：尾部模型的总量仍然要在图上体现，否则"总量对不上"。
//
// **token 与成本两张 map 必须走同一套 keep 集合**：否则切到成本口径时，
// 被保留的模型集合与 token 口径不一致，同一根柱子在两个口径下的构成会不一样，
// 而两条口径的合计又都声称等于同一个桶的总量 —— 那是最难查的一类不一致。
func trimAnalyticsSeries(series []AnalyticsBucket, models []DimensionUsageStat) {
	keep := make(map[string]bool, relayAnalyticsTopModels)
	for i, stat := range models {
		if i >= relayAnalyticsTopModels {
			break
		}
		keep[stat.Model] = true
	}
	for i := range series {
		bucket := &series[i]
		mergedTokens := make(map[string]int64, len(keep)+1)
		mergedCost := make(map[string]float64, len(keep)+1)
		var otherTokens int64
		var otherCost float64
		for name, tokens := range bucket.ByModel {
			if keep[name] {
				mergedTokens[name] = tokens
				continue
			}
			otherTokens += tokens
		}
		for name, cost := range bucket.ByModelCost {
			if keep[name] {
				mergedCost[name] = cost
				continue
			}
			otherCost += cost
		}
		if otherTokens > 0 {
			mergedTokens[otherModelKey] = otherTokens
		}
		if otherCost > 0 {
			mergedCost[otherModelKey] = otherCost
		}
		bucket.ByModel = mergedTokens
		bucket.ByModelCost = mergedCost
	}
}

// finishDimension 把某个维度的桶收尾成可排序的切片。
//
// 排序按请求数倒序、同数按名称升序：请求数回答"谁用得最多"，
// 名称做次序是为了让同数的条目有稳定顺序（否则每次请求返回的顺序都在抖，
// 界面上的行会无端跳位）。
func finishDimension(store map[string]*DimensionUsageStat) []DimensionUsageStat {
	out := make([]DimensionUsageStat, 0, len(store))
	for _, stat := range store {
		if stat.Requests > 0 {
			stat.AvgDurationMs = stat.AvgDurationMs / float64(stat.Requests)
		}
		stat.SuccessRate = ratio(stat.Success, stat.Requests)
		out = append(out, *stat)
	}
	sortDimensionStats(out)
	return out
}

// sortDimensionStats 把某个维度的条目排成展示顺序：请求数倒序，同数按名称升序。
//
// 抽成独立函数而不是内联在 finishDimension 里，是为了让判据能确定性地验证它：
// 内联时输入来自 map 遍历，而 Go 的 map 遍历顺序随机 —— 一个"只按请求数排序"
// 的错误实现在同数条目上会**间歇性**排到正确位置，端到端用例时红时绿，
// 变异检查因此会得出"守卫无效"的错误结论（本轮实测踩过）。
// 直接喂一个顺序确定的切片，才能把这条性质钉死。
//
// 名称做次序不是为了好看：同数条目若没有稳定顺序，每次请求返回的行都可能换位置，
// 界面上的表格会无端跳动。
func sortDimensionStats(out []DimensionUsageStat) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		return out[i].Model < out[j].Model
	})
}
