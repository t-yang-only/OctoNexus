import type { RelayHistoryItem, RelayLogOverview } from '@/api/log';

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
    actualModel: string;
    clientProtocol: number;
    targetProtocol: number;
    apiKeyName: string;
    error: string;
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

    const targetModel = source.target_model || '';

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
        actualModel: targetModel || source.model || '',
        clientProtocol: live ? source.protocol : source.target_protocol,
        targetProtocol: source.target_protocol,
        apiKeyName: source.api_key_name || '',
        error: source.error || '',
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
