import { useMutation, useQuery } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { apiRequest } from './client';
import type { RelayLogSample } from './analytics';

// RequestState 表示 Relay 请求的实时状态。
export type RequestState = 'running' | 'committed' | 'success' | 'failed' | 'canceled';

// RelayUsage 保存请求结束后确认的统一 Token 用量。
export interface RelayUsage {
    prompt_tokens: number;
    completion_tokens: number;
    total_tokens: number;
    prompt_tokens_details: {
        cached_tokens: number;
        write_cached_tokens?: number;
    } | null;
    // completion_tokens_details.reasoning_tokens 是上游回报的思考 token 数（T-insight-001）。
    // 整个对象可能缺失、字段也可能为 0 —— 两者都表示"上游没报"，不是"没有思考"。
    completion_tokens_details?: {
        reasoning_tokens?: number;
    } | null;
}

// RelayHistoryItem 是历史日志接口返回的单条持久化快照。
export interface RelayHistoryItem {
    id: number;
    request_id: number;
    status: string;
    model: string;
    group_id: number;
    api_key_name: string;
    target_channel: string;
    target_model: string;
    target_protocol: number;
    // request_protocol 是客户端进来时用的协议位（T-trace-004），取值同 model.Protocol。
    // 与 target_protocol 成对看：相同=原样转发，不同=中间做了跨协议转换。
    // 升级前的存量行为 0（未记录），此时界面显示"未知"而不是拿上游协议顶上。
    request_protocol: number;
    // reported_model 是上游响应体里回报的模型名（T-verify-001）。
    // 与 target_model 的区别是关键：后者是「我们请求了什么」，前者是「上游自称用了什么」。
    // 空串表示上游没回报该字段（常见现象），此时不判定。
    reported_model: string;
    // model_mismatch 标记上游回报的模型与请求的不一致。
    // 这是「上游偷换模型」的唯一可见证据——按高价模型收费却用低价模型出货，
    // 只看我们自己的记录永远发现不了。
    model_mismatch: boolean;
    started_at: string;
    first_byte_ms: number;
    duration_ms: number;
    // attempts 是本请求打向上游的轮次数: 1 = 第一次就出结果, >1 = 中途换过成员; 首字竞速的多路并算一轮。
    attempts: number;
    // decision 是这次请求的选路判定（后端 relay.Decision.Text()）: 形如
    // "mode=smart;tier=decision;reason=affinity;slot=1;attempt=2", 回答"为什么走了这个成员"。
    // 未发起上游请求就结束的请求为空串。
    decision: string;
    prompt_tokens: number;
    cached_tokens: number;
    completion_tokens: number;
    cost: number;
    // fault_kind 是失败的归因分类: request / member / transient，空串表示非失败或未分类。
    // 与 Status 的区别是关键: Status 只说"这次失败了"，FaultKind 说"这次失败该算在谁的账上"。
    // 后端在产生错误的那一刻定类并落库（历史）或随 SSE 下发（实时），前端只做展示与筛选，绝不重新分类。
    fault_kind?: FaultKind;
    // stop_reason 是**为什么停下来**的结构化记录（T-trace-003），
    // 形如 "action=stop;reason=attempt_budget_exhausted;source=config"；空串表示升级前的存量行。
    //
    // 与 fault_kind 是补充关系而不是重复：
    //   fault_kind  这次失败**算谁的账**（request/member/transient）
    //   stop_reason **哪条规则**终止了请求、这条规则**从哪来**
    //
    // 为什么缺它不行：同样是失败终态，"试到次数上限才放弃"（去查上游是否大面积故障）
    // 与"全部成员都说这个请求非法"（去改请求）的处置动作完全不同，
    // 而 fault_kind 两者都可能记成 request。只看 error 文本更不行 ——
    // 后端有五个终止出口，都能产生同一句 upstream_error 的 502。
    stop_reason?: string;
    // reasoning_effort 是客户端指定的思考强度（T-insight-001）；空串表示没指定。
    // reasoning_tokens 是上游回报的思考 token 数；0 表示上游没报该字段。
    // reasoning_chars 是响应正文里思考文本的字符数（T-insight-005）：上游普遍不报
    // token（生产实测一条都没有），此时字符数是唯一能说明"思考有多长"的量。
    // 两者各记各的、不互斥，界面按各自有无分别展示。
    reasoning_effort?: string;
    reasoning_tokens?: number;
    reasoning_chars?: number;
    // is_test 标记这条请求由客户端声明为验证/测试（T-trace-006），取自请求头 X-Octopus-Test。
    //
    // 为什么它在日志页可见、却不在画像里：画像默认把测试请求剔除（验证一次不该扰动一次
    // 成功率与延迟分布），而日志页是"我到底发过什么"的地方 —— 它必须完整保留这些行并
    // 明确标出来，否则"我刚发的那条怎么没进统计"会变成新的疑问。
    is_test?: boolean;
    // attempt_detail 是每一轮尝试的明细链（T-trace-001），按轮次顺序。
    //
    // 回答的是既有字段回答不了的问题: Attempts 只说"试了几次"、TargetChannel 只说"最后用了谁"，
    // **中间试过谁、各自为什么失败** 只能从这里看。某个成员每次都失败、每次都要绕开它，
    // 从最终结果上看和"这个分组有点慢"毫无区别。
    //
    // 与实时快照的 attempt_chain 同义（同一份数据的落库形态），命名沿用后端字段。
    // 老数据（升级前落库）没有这个字段，故为可选。
    attempt_detail?: RelayAttemptDetail[];
    // attempts_truncated 标记尝试链是否被截断（只保留最后 RelayAttemptDetailMax 轮）。
    // 界面必须据此提示"前面还有"，否则读的人会以为这就是全部。
    attempts_truncated?: boolean;
    error: string;
}

// RelayAttemptDetail 是单轮尝试的落库快照，与后端 model.RelayAttemptDetail 一一对应。
export interface RelayAttemptDetail {
    // round 是这一轮在请求内的序号，从 1 起，与面板上的"第几轮"同口径。
    round: number;
    channel: string;
    model: string;
    wait_ms: number;
    // fault_kind 空串表示这一轮没有归因：要么它成功了，要么它是被人工中止的（不是渠道故障）。
    fault_kind?: FaultKind;
    // error 是上游返回的错误原文；空串表示本轮没报错。
    error?: string;
}

// FaultKind 是失败归因分类，与后端 model.RelayLog.FaultKind 一一对应。
// request=请求本身非法（不计渠道故障）；member=成员自身问题；transient=可恢复（超时/网络/5xx）。
// undefined 或空串表示未分类：可能非失败，也可能是升级前写入的历史行 —— 两者都不得猜测归类。
export type FaultKind = 'request' | 'member' | 'transient';

// RelayHistoryFilter 是历史查询的筛选条件, 空串表示不过滤。
export interface RelayHistoryFilter {
    status?: string;
    model?: string;
    channel?: string;
    apikey?: string;
    q?: string;
    // is_test 三态过滤测试请求（T-trace-006）：'' 或省略=全部、'true'=仅测试、'false'=仅非测试。
    //
    // 用字符串而不是布尔：布尔只有两种取值，表达不了"不筛"这个状态，硬塞会逼出第二个开关，
    // 而两个开关会产生"都开/都关"的组合歧义。后端对非法值报 400 而不是静默当"不筛"。
    is_test?: string;
    limit?: number;
    offset?: number;
}

// useRelayHistory 按筛选条件查询持久化历史日志（分页）。
export function useRelayHistory(filter: RelayHistoryFilter) {
    const params = new URLSearchParams();
    for (const [key, value] of Object.entries(filter)) {
        if (value !== undefined && value !== '') params.set(key, String(value));
    }
    const queryKey = ['logs', 'history', params.toString()];
    return useQuery({
        queryKey,
        queryFn: () =>
            apiRequest<{ items: RelayHistoryItem[]; total: number }>(
                `/api/v1/log/history?${params.toString()}`
            ),
    });
}

// exportRelayLogs 把当前筛选条件下的请求级明细导出成 CSV（U-key-001 余项）。
// 与历史查询同一套查询参数，但不分页：后端逐行流式写出，前端下载为文件。
export async function exportRelayLogs(filter: RelayHistoryFilter) {
    const params = new URLSearchParams();
    for (const [key, value] of Object.entries(filter)) {
        // limit/offset 属于分页语义，导出不带它们（导出就是"当前筛选的全量"）。
        if (key === 'limit' || key === 'offset') continue;
        if (value !== undefined && value !== '') params.set(key, String(value));
    }
    const response = await fetch(`/api/v1/log/export?${params.toString()}`, { credentials: 'include' });
    if (!response.ok) {
        throw new Error(`export failed: HTTP ${response.status}`);
    }
    const blob = await response.blob();
    const now = new Date();
    const pad = (n: number) => String(n).padStart(2, '0');
    const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`;
    const url = URL.createObjectURL(blob);
    try {
        const anchor = document.createElement('a');
        anchor.href = url;
        anchor.download = `octopus-relay-logs-${stamp}.csv`;
        document.body.appendChild(anchor);
        anchor.click();
    } finally {
        URL.revokeObjectURL(url);
    }
    return blob.size;
}

// RelayLogOverview 是请求状态流发送的完整进程内请求状态。
export interface RelayLogOverview {
    id: number;
    status: RequestState;
    started_at: string;
    first_byte_at?: string; // 首字节写出时间, 未提交时为空; 供卡片派生"首字时间"。
    duration: number;
    model: string;
    protocol: number;
    group_id: number;
    api_key_name: string;
    usage: RelayUsage;
    // is_test 同 RelayHistoryItem 的同名字段：客户端以 X-Octopus-Test 声明的验证请求。
    // 后端 RequestState 用 `is_test,omitempty` 序列化，所以只有 true 才会出现在实时流里。
    is_test?: boolean;
    cost: number;
    round: number;
    round_started_at: string;
    // decision 是本轮的选路判定（后端 relay.Decision.Text()），每轮刷新；空串表示尚未走到选路。
    decision?: string;
    target_channel: string;
    target_model: string;
    target_protocol: number;
    // request_protocol 与 RelayHistoryItem 同义（T-trace-004）。
    // 实时快照用顶层的 protocol 表达同一件事（RequestState.Protocol），落库行才用本字段；
    // 这里声明成可选正是为了让两种来源同构，取值时由 display.ts 按 live 分支选一个。
    request_protocol?: number;
    // reported_model / model_mismatch 与 RelayHistoryItem 同义（T-verify-001）：
    // 前者是上游自称用了什么，后者标记它与请求的模型不一致。实时快照同样带这两个字段。
    reported_model?: string;
    model_mismatch?: boolean;
    sending: boolean;
    // fault_kind 同 RelayHistoryItem：失败归因分类，实时流由后端直接下发（RequestState 带该字段）。
    fault_kind?: FaultKind;
    // stop_reason 同 RelayHistoryItem：为什么停下来（T-trace-003），随实时流下发。
    stop_reason?: string;
    // reasoning_effort 同 RelayHistoryItem：客户端指定的思考强度（T-insight-001）。
    // 实时快照只带它；思考 token 数在嵌套的 usage.completion_tokens_details 里。
    //
    // **这里刻意没有 reasoning_chars**：字符数要遍历聚合后的响应正文，开销只值得在
    // 请求终态（落库那一刻）付一次，实时流上不重复算。display.ts 对进行中的请求
    // 读到 0 就当作"还没有这个数"而不显示 —— 这不是字段缺失，是有意的口径。
    reasoning_effort?: string;
    // attempt_chain 是已结束轮次的尝试明细（T-trace-001）。
    //
    // 实时快照与历史行都给这个字段，但口径一致: 只含**已结束**的轮次，
    // 正在进行的那轮由 target_channel / target_model / round 表达 —— 两者不重叠，
    // 于是"链上全部轮次 + 当前轮"恒等于本次请求打过的全部成员。
    // 实时看它就能看到"刚刚这几次都试了谁、为什么失败"，不必等请求结束落库。
    attempt_chain?: RelayAttemptDetail[];
    // attempts_truncated 标记尝试链是否被截断（只保留最后 RelayAttemptDetailMax 轮）。
    attempts_truncated?: boolean;
    error?: string;
}

// useClearLogs 清空已完成的内存日志。
export function useClearLogs() {
    return useMutation({
        mutationFn: () => apiRequest<null>('/api/v1/log/clear', { method: 'DELETE' }),
    });
}

// useStopRound 中止指定请求当前轮次匹配的上游调用。
export function useStopRound() {
    return useMutation({
        mutationFn: ({ requestId, round }: { requestId: number; round: number }) =>
            apiRequest<null>(`/api/v1/log/${requestId}/${round}/stop`, { method: 'POST' }),
    });
}

// useLogs 订阅进程内日志概览，并按 RequestID 更新同一条记录。
// refresh 供自动刷新偏好调用: 通过 bump 连接键重建 SSE 兜底断线/空闲, SSE 正常时仅重建连接。
export function useLogs() {
    const [logs, setLogs] = useState<RelayLogOverview[]>([]);
    const [isLoading, setIsLoading] = useState(true);
    const [error, setError] = useState<Error | null>(null);
    const [connectKey, setConnectKey] = useState(0);

    const refresh = () => setConnectKey((key) => key + 1);

    useEffect(() => {
        const source = new EventSource('/api/v1/log/overview/stream', { withCredentials: true });

        source.onopen = () => {
            setError(null);
            setIsLoading(false);
        };
        source.addEventListener('log', (event) => {
            let next: RelayLogOverview;
            try {
                next = JSON.parse((event as MessageEvent<string>).data) as RelayLogOverview;
            } catch {
                setError(new Error('Invalid log update'));
                return;
            }
            setIsLoading(false);
            setError(null);
            // 列表始终按 ID 倒序: 命中已有记录时原地替换, 新记录插入到首个更小 ID 之前,
            // 由此避免每条更新重排整个列表, 并保留未变更记录的引用以跳过卡片重渲染。
            setLogs((current) => {
                const index = current.findIndex((item) => item.id === next.id);
                if (index >= 0) {
                    const updated = current.slice();
                    updated[index] = next;
                    return updated;
                }
                const position = current.findIndex((item) => item.id < next.id);
                if (position < 0) return [...current, next];
                return [...current.slice(0, position), next, ...current.slice(position)];
            });
        });
        source.onerror = () => {
            setIsLoading(false);
            setError(new Error('Log stream disconnected'));
        };

        return () => {
            source.close();
        };
    }, [connectKey]);

    return { logs, isLoading, error, refresh };
}

// useLogRequestBody 在调用方启用时按需获取指定日志的请求体。
export function useLogRequestBody(id: number, startedAt: string, enabled: boolean) {
    return useQuery({
        queryKey: ['logs', id, startedAt, 'request-body'],
        queryFn: () => apiRequest<string>(`/api/v1/log/${id}/request-body`),
        enabled,
        staleTime: Infinity,
    });
}

// useLogResponseBody 在调用方启用时获取指定日志的最终响应体。
export function useLogResponseBody(id: number, startedAt: string, enabled: boolean) {
    return useQuery({
        queryKey: ['logs', id, startedAt, 'response-body'],
        queryFn: () => apiRequest<string>(`/api/v1/log/${id}/response-body`),
        enabled,
        staleTime: Infinity,
    });
}

// T-usability-008 真实通过率（失败按归因分桶）。
//
// 两个通过率回答两个不同的问题，只给一个必然误导一半场景：
//   successRate  用户发起的请求有多少成功了 —— 体验视角
//   channelRate  这个渠道本身健康吗       —— 诊断视角（排除了「请求本身非法」）
//
// 实测：senseaudio 的 successRate 只有 31.6%，但 channelRate 接近 100% ——
// 那 25 次失败全是「用 chat 接口调 TTS/图像模型」造成的请求非法，跟渠道无关。
export interface RelayFaultCounts {
    success: number;
    /** 客户端主动断开：既不算成功也不算失败。 */
    canceled: number;
    /** 请求本身非法（400 一类）：任何成员都会同样拒绝，**不计入渠道健康度**。 */
    request_fault: number;
    /** 成员自身问题（凭据无效/无权限/模型不存在）：算渠道故障。 */
    member_fault: number;
    /** 可恢复失败（超时/限流/5xx/网络）：算渠道故障。 */
    transient_fault: number;
    /**
     * 失败但未归类（升级前的存量行没有 fault_kind）。
     *
     * 单独列出而不是并进某一类：**不知道的不能猜** ——
     * 猜成渠道故障会把历史账算到渠道头上，而原因根本不在它身上。
     */
    unclassified: number;
}

export interface ChannelFaults extends RelayFaultCounts {
    channel: string;
    success_rate: number;
    channel_rate: number;
}

export interface RelayFaultStats extends RelayFaultCounts {
    window: number;
    /** 样本来源账（T-trace-006）：窗口内剔除掉的测试请求条数也在这里。 */
    sample: RelayLogSample;
    channels: ChannelFaults[];
}

// useRelayFaultStats 取真实通过率。
// window 用**条数**而不是天数：部署后流量差异极大，
// 按天取会让「最近一天只有 3 条」的渠道得出毫无意义的比例。
export function useRelayFaultStats(window = 500, enabled = true) {
    return useQuery({
        queryKey: ['logs', 'fault-stats', window],
        queryFn: () => apiRequest<RelayFaultStats>(`/api/v1/log/fault-stats?window=${window}`),
        enabled,
    });
}

// T-trace-002 尝试链聚合：谁在被反复试错。
//
// ## 与 fault_stats 的分工（两者刻意不同，都要有）
//
//   fault_stats   按**最终结果**归因 —— 成功率 / 渠道健康度
//   attempt_stats 按**每一次尝试**归因 —— 谁在被反复试错
//
// 关键差异：一次**成功**的请求里 A 失败、B 接手成功 ——
// fault_stats 完全看不到 A 的失败（那条日志 status=success），本接口能看到。
export interface AttemptChannelStat {
    channel: string;
    /** 被尝试的轮数（含成功的轮）。分母是尝试次数，不是请求数。 */
    attempts: number;
    /** 失败的轮数。 */
    failures: number;
    /** 成功的轮数（正常每请求最多 1 次，因为成功即结束）。 */
    successes: number;
    /** 失败里「请求本身非法」：换个成员也一样结局。 */
    request_fault: number;
    /** 失败里「成员自身问题」：凭据/权限/模型不存在。 */
    member_fault: number;
    /** 失败里「可恢复」：超时/限流/5xx/网络。 */
    transient_fault: number;
    /** 失败但没带归因（升级前的存量轮）：不知道的不能猜。 */
    unclassified_fault: number;
    /** 最近一次失败的原文（后端已按字符截断）。 */
    last_error?: string;
    /** 最近一次失败所在日志的 id，便于回溯到具体那条。 */
    last_error_log_id?: number;
}

export interface AttemptChainStats {
    /** 扫过的日志条数。 */
    window: number;
    /** 其中带尝试链的条数。window - scanned 是升级前的存量行。 */
    scanned: number;
    /** 链被截断过的日志条数：这类日志轮次不完整，聚合值会偏低。 */
    truncated: number;
    /**
     * 样本来源账（T-trace-006）。
     *
     * 与上面那个 truncated 是两回事：那个说的是"链被截断"，这个说的是"窗口内剔除掉
     * 几条测试请求"。正因为两者重名容易看错，才把窗口账整体嵌进 sample 而不平铺。
     */
    sample: RelayLogSample;
    /** 换过人（>1 轮）的请求数。 */
    multi_round_requests: number;
    /**
     * 有过**失败轮**的请求数。
     *
     * 与 fault_stats 的失败数刻意不同：这里的请求最终可能是**成功**的。
     * 两者之差就是「试错但最终成功」的请求量 —— 正是本视图存在的理由。
     */
    affected_requests: number;
    channels: AttemptChannelStat[];
}

export function useAttemptChainStats(window = 500, enabled = true) {
    return useQuery({
        queryKey: ['logs', 'attempt-stats', window],
        queryFn: () => apiRequest<AttemptChainStats>(`/api/v1/log/attempt-stats?window=${window}`),
        enabled,
    });
}