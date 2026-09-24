import type { RelayAttemptDetail, RelayHistoryItem, RelayLogOverview } from '@/api/log';

// 日志卡片的容错字段解析（NM-DS-008；语义借鉴 fork 的 display.ts，实现按本地数据结构重写）。
//
// 目的：日志有两个来源，字段形状不同，且实时快照会在中途缺字段——
//   · 实时快照 RelayLogOverview：用量是嵌套的 usage，耗时是纳秒 duration，首字时间靠两个时间戳相减；
//   · 持久化历史 RelayHistoryItem：用量与耗时都是扁平字段（prompt_tokens/duration_ms/first_byte_ms）。
// 卡片此前直接读 log.usage.* / log.duration，只能渲染实时快照，且回退逻辑散落在各处。
// 这里把"同一件事的不同写法"收敛成一次解析：调用方只面对一套字段，缺哪一项就沿回退链取下一个。
//
// 回退链（每项都只在"确实没有"时才回退，绝不凭空造值）：
//   身份   ：id / request_id
//   首字   ：first_byte_ms → first_byte_at - started_at → -1（未提交，界面显示占位）
//   耗时   ：duration_ms → duration(纳秒) → 进行中按 now - started_at 估算
//   输入量 ：usage.prompt_tokens → prompt_tokens
//   缓存量 ：usage.prompt_tokens_details.cached_tokens → cached_tokens
//   总量   ：usage.total_tokens → 输入量 + 输出量（服务端可能在结束前给 0）
//   实际模型：target_model → 客户端请求的 model（未选出成员时，界面至少显示请求的模型名）
//   费用   ：cost（两个来源都直接给；缺失即 0，不在前端凭价格表推算）

export type LogDisplaySource = RelayLogOverview | RelayHistoryItem;

export interface LogDisplayFields {
    requestId: number;
    groupId: number;
    status: string;
    startedAt: string;
    startedAtMs: number;
    firstByteMs: number; // 未提交为 -1。
    durationMs: number;
    // attempts 是本请求打向上游的轮次数: 1 = 第一次就出结果, >1 = 中途换过成员(重试/换人),
    // 0 = 还没发起过上游请求就结束了。首字竞速的多路并发算一轮。
    attempts: number;
    // decision 是这次请求的选路判定: 形态与含义见 relay/decision.go（与响应头 X-Octopus-Route 同一份文本）。
    // 空串表示该请求没走到选路（例如分组不存在、Key 不允许该模型）。
    decision: string;
    elapsedMs: number; // 速率类指标（TPS）的除数。
    promptTokens: number;
    cachedTokens: number;
    cacheWriteTokens: number;
    completionTokens: number;
    totalTokens: number;
    cost: number;
    model: string;
    targetChannel: string;
    targetModel: string;
    // actualModel 是**实际跑这次请求的模型**（T-verify-001）。
    //
    // 注意它此前被赋成了 targetModel —— 那是「我们请求了什么」，不是「上游用了什么」。
    // 两者在上游偷换模型时不同，把请求名当实际名显示等于让面板说假话：
    // 用户以为看到的是真实模型，其实只是自己填的名字。
    // 现在的取值顺序：上游回报的 → 请求的 → 客户端原始 model。
    actualModel: string;
    // reportedModel 是上游响应体里回报的模型名原值；空串表示上游没回报（常见现象）。
    // 单独保留原值是为了让界面能区分「上游说它用了 X」与「上游没说话」——
    // 前者可用于比对，后者只能显示占位。
    reportedModel: string;
    // modelMismatch 标记上游回报的模型与请求的不一致，即「上游可能偷换了模型」的唯一可见证据。
    modelMismatch: boolean;
    clientProtocol: number;
    targetProtocol: number;
    apiKeyName: string;
    error: string;
    // attemptChain 是每一轮尝试的明细链（T-trace-001），按轮次升序；老数据为空数组。
    //
    // 两个来源都给这个字段且口径一致（见 api/log.ts 的 attempt_chain 注释）：
    // 链上只含已结束的轮次，正在进行的那轮由 targetChannel/round 表达。
    attemptChain: RelayAttemptDetail[];
    // attemptChainTruncated 标记链被截断过，界面必须提示"前面还有"。
    attemptChainTruncated: boolean;
    // stopReason 是**为什么停下来**的结构化记录（T-trace-003），形如
    // "action=stop;reason=X;source=Y"；空串表示正常结束或升级前的存量行。
    stopReason: string;
    // reasoningEffort 是客户端指定的思考强度（T-insight-001），空串表示没指定。
    // 它与 reasoningTokens 配对：强度是原因，token 数是结果。
    reasoningEffort: string;
    // reasoningTokens 是上游在 usage 里回报的思考 token 数。
    //
    // 0 表示**上游没报该字段**（非推理模型、未实现该字段的站点都很常见），
    // 不能读作"这次没有思考"—— 界面据此显示占位而不是 0。
    reasoningTokens: number;
    // tps 是输出速度（token/秒）。分母取整段耗时（含首字等待），与卡片上的"总耗时"
    // 同一口径：若只除以生成时间，会把等待上游排队的时间从速度里悄悄抹掉。
    tps: number;
    // cacheHitRate 是缓存命中率（命中缓存 ÷ 输入总量）。
    // 无缓存数据时为 null 而不是 0 —— 0% 会被读成"缓存完全没命中"，那是另一回事。
    cacheHitRate: number | null;
    // realInputTokens 是**真正新算的**输入量（输入 − 命中缓存）。
    // 缓存命中的部分通常按 0.1 倍计价，两者混着看会让"输入 3.7 万"显得远比实际严重。
    realInputTokens: number;
    source: 'live' | 'history';
}

// isLiveOverview 判断来源是实时快照（有嵌套 usage）还是持久化历史行。
function isLiveOverview(source: LogDisplaySource): source is RelayLogOverview {
    return 'usage' in source;
}

function parseTime(value: string | undefined): number {
    if (!value) return 0;
    const parsed = Date.parse(value);
    return Number.isFinite(parsed) ? parsed : 0;
}

export function resolveLogDisplay(source: LogDisplaySource, now: number = Date.now()): LogDisplayFields {
    const live = isLiveOverview(source);
    const startedAtMs = parseTime(source.started_at);
    const status = source.status;
    const running = status === 'running' || status === 'committed';

    // 耗时：历史行直接给毫秒；实时快照结束时给纳秒，进行中按共享时钟估算。
    let durationMs: number;
    if (live) {
        durationMs = running
            ? (startedAtMs > 0 ? Math.max(0, now - startedAtMs) : 0)
            : Math.max(0, Math.round(source.duration / 1_000_000));
    } else {
        durationMs = Math.max(0, source.duration_ms ?? 0);
    }

    // 首字时间：历史行给毫秒（未提交为 -1）；实时快照用首字节时间减请求到达时间。
    let firstByteMs = -1;
    if (live) {
        const firstByteAtMs = parseTime(source.first_byte_at);
        if (firstByteAtMs > 0 && startedAtMs > 0) {
            firstByteMs = Math.max(0, firstByteAtMs - startedAtMs);
        }
    } else if (typeof source.first_byte_ms === 'number' && source.first_byte_ms >= 0) {
        firstByteMs = source.first_byte_ms;
    }

    // 用量：实时快照取嵌套结构，历史行取扁平字段；总量缺失时用输入 + 输出兜底。
    const nested = live ? source.usage : undefined;
    const promptTokens = nested?.prompt_tokens ?? (live ? 0 : source.prompt_tokens ?? 0);
    const completionTokens = nested?.completion_tokens ?? (live ? 0 : source.completion_tokens ?? 0);
    const cachedTokens = nested?.prompt_tokens_details?.cached_tokens ?? (live ? 0 : source.cached_tokens ?? 0);
    const cacheWriteTokens = nested?.prompt_tokens_details?.write_cached_tokens ?? 0;
    const reportedTotal = nested?.total_tokens ?? 0;
    const totalTokens = reportedTotal > 0 ? reportedTotal : promptTokens + completionTokens;

    // 思考强度：两个来源同名字段（实时快照来自 RequestState，历史行来自落库列）。
    const reasoningEffort = source.reasoning_effort ?? '';
    // 思考 token：实时快照在嵌套 usage 的 completion_tokens_details 里，
    // 历史行是落库的扁平列（口径见 model.RelayLog.ReasoningTokens）。
    const reasoningTokens = live
        ? (source.usage.completion_tokens_details?.reasoning_tokens ?? 0)
        : (source.reasoning_tokens ?? 0);

    const targetModel = source.target_model || '';
    // 上游回报的模型名：实时快照与历史行同名字段，故两条路径共用一次读取。
    // 空串是常见情况（不少站点响应里不带 model），此时不判定是否一致。
    const reportedModel = source.reported_model || '';
    // 判定以前端为准还是以后端为准：后端已经在落库时算好（model_mismatch），
    // 前端直接用它，避免两处各写一套比较规则（大小写、空白处理稍有出入就会前后矛盾）。
    // 后端未给（老数据或实时快照早期版本）时按同一口径现算一次兜底。
    const modelMismatch =
        typeof source.model_mismatch === 'boolean'
            ? source.model_mismatch
            : reportedModel !== '' &&
              targetModel !== '' &&
              reportedModel.trim().toLowerCase() !== targetModel.trim().toLowerCase();

    return {
        requestId: live ? source.id : source.request_id,
        groupId: source.group_id,
        status,
        startedAt: source.started_at,
        startedAtMs,
        firstByteMs,
        durationMs,
        // 实时快照还没有落库值, 用它当前的轮次序号(与面板上的"第几轮"同源); 历史行用落库的上游轮次。
        attempts: live ? source.round ?? 0 : source.attempts ?? 0,
        // 判定理由两个来源都直接给（后端同一份文本）: 实时快照是刷新中的最新一轮, 历史行是落库的终态。
        decision: source.decision ?? '',
        elapsedMs: running ? (startedAtMs > 0 ? Math.max(0, now - startedAtMs) : 0) : durationMs,
        promptTokens,
        cachedTokens,
        cacheWriteTokens,
        completionTokens,
        totalTokens,
        cost: source.cost ?? 0,
        model: source.model || '',
        targetChannel: source.target_channel || '',
        targetModel,
        // 上游回报的模型优先：它才是「实际在跑的那个」。上游没回报时退回请求名，
        // 至少显示用户填的东西，但 reportedModel 保持空串以便界面区分这两种情况。
        reportedModel,
        modelMismatch,
        actualModel: reportedModel || targetModel || source.model || '',
        clientProtocol: live ? source.protocol : source.target_protocol,
        targetProtocol: source.target_protocol,
        apiKeyName: source.api_key_name || '',
        error: source.error || '',
        // 尝试链: 实时快照是 attempt_chain（进程内 RequestState），历史行是 attempt_detail（落库字段，
        // 见 model.RelayLog —— 用 AttemptDetail 是为了与 RelayLog.Attempts 计数区分开）。
        // 两者同义同口径，这里收敛成一套；缺字段时给空数组而不是 undefined，调用方不必到处判空。
        attemptChain: (live ? source.attempt_chain : source.attempt_detail) ?? [],
        attemptChainTruncated: source.attempts_truncated === true,
        stopReason: source.stop_reason ?? '',
        reasoningEffort,
        reasoningTokens,
        // TPS 与缓存命中率是纯派生量，在解析层算一次，避免各卡片各写一份除法
        // （分母口径稍有出入，同一个请求在两处就会显示成两个数）。
        tps: durationMs > 0 ? completionTokens / (durationMs / 1000) : 0,
        cacheHitRate: cachedTokens > 0 && promptTokens > 0 ? cachedTokens / promptTokens : null,
        realInputTokens: Math.max(0, promptTokens - cachedTokens),
        source: live ? 'live' : 'history',
    };
}

// formatJsonForCopy 供"复制"按钮使用：能解析成 JSON 时按两空格缩进重排，否则原样去掉首尾空白。
// 上游错误体常常是一行挤在一起的 JSON，直接复制不便阅读；解析失败不抛错，避免复制动作把界面打断。
export function formatJsonForCopy(text: string | null | undefined): string {
    if (!text) return '';
    try {
        return JSON.stringify(JSON.parse(text), null, 2);
    } catch {
        return text.trim();
    }
}
