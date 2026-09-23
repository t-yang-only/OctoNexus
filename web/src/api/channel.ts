import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';
import { channelStatsQueryOptions, groupListQueryOptions, modelListQueryOptions } from './queries';
import { formatStatsMetrics, type StatsMetrics, type StatsMetricsFormatted } from './stats';

// Protocol 是渠道支持的上游线协议位掩码，位值与后端 model.Protocol 一致，不可变更。
// 一条授权可同时支持多个协议，按位或组合；1 << 0 由后端保留待用。
export const Protocol = {
    OpenAIChatCompletion: 1 << 1,
    OpenAIResponse: 1 << 2,
    AnthropicMessage: 1 << 3,
} as const;

// Dialect 是上游在标准协议之上的方言，决定出站转换器的厂商特化配置。
// 地址与路径不属于方言范畴，由前端按服务商预填到渠道字段上。
export type Dialect = 'generic';

type CustomHeader = {
    header_key: string;
    header_value: string;
};

// ChannelKey 是渠道下的一份上游凭据；名称在渠道内唯一，读写都按它引用。
export type ChannelKey = {
    name: string;
    key: string;
    enabled: boolean;
    // proxy_node_id 是该凭据的出网节点（代理节点池的主键）；0 = 不单独指定，跟渠道走。
    // 防关联的粒度就在这一行：同一个上游服务商的两个账号可以分别从两个出口出去。
    proxy_node_id: number;
};

// ChannelGrant 是渠道内的一条上游授权：指定模型使用指定凭据时支持的协议集合，也是转发的最小单位。
// 两侧按名称引用，读写同构：名称在渠道内唯一，新增的模型与凭据在后端同一事务内才分配主键，
// 故提交侧只能按名称引用，读侧也随之给名称，页面无需在主键与名称之间翻译。
// 授权本身没有启用状态: 不再授权即删掉该组合, 临时收回则停用凭据或摘掉协议位。
export type ChannelGrant = {
    model_name: string;
    key_name: string;
    protocols: number; // Protocol 位掩码。
};

// ChannelGrantCandidate 是分组页可选取的一条授权，字段与 GroupItem 的展示字段一一对应。
// 分组页由此不必拉整份渠道列表：那里带着统计、路径、代理与凭据明文，与选取成员无关。
// available 与分组成员同一口径，均由后端定稿，前后端不会各判一套。
export type ChannelGrantCandidate = {
    id: number; // 授权主键，分组成员按它引用。
    channel_id: number;
    channel_name: string;
    model_name: string;
    key_name: string;
    protocols: number; // Protocol 位掩码。
    available: boolean;
};

/**
 * 渠道完整配置（与后端 model.ChannelDetail 对齐）
 * 读写同构：编辑表单读到什么形状就提交什么形状，故创建与更新共用此类型，无需另建提交类型。
 * 提交即全量，未列出的凭据与模型会被删除并级联删除其授权；创建时 id 取 0，由后端分配。
 * keys、models、grants 和 custom_header 恒为数组，后端读取侧承诺不为 null。
 */
export type ChannelDetail = {
    id: number;
    name: string;
    dialect: Dialect;
    enabled: boolean;
    base_url: string; // 上游地址，各协议共用。
    openai_chat_completion_path: string;
    openai_response_path: string;
    anthropic_message_path: string;
    keys: ChannelKey[];
    models: string[]; // 上游模型名称；模型除名称外没有界面用得上的字段。
    grants: ChannelGrant[];
    proxy: boolean;
    custom_header: CustomHeader[];
    param_override: string;
    channel_proxy: string;
    // proxy_node_id 是整条渠道的默认出网节点；0 = 直连（或走 channel_proxy 的传统代理设置）。
    proxy_node_id: number;
    match_regex: string;
    // 计费事实（R-weight-001 第二阶段）: 供加权综合选路折算「实际有多贵」; 留空/0 表示未知。
    billing_mode: string;
    multiplier: number;
    per_call_price: number;
    monthly_quota: number;
    monthly_used: number;
};

// ChannelModelStats 是单个渠道模型的累计统计，自带名称。
export type ChannelModelStats = StatsMetrics & {
    model_id: number;
    model_name: string;
};

// ChannelStats 是单个渠道及其模型的累计统计，自带名称与启停状态。
// 这一份同时充当渠道列表项：列表页要展示的名称、启停与模型个数（即 models.length）都在此，
// 故没有单独的渠道概览接口，整份配置在点开编辑时由 useChannelDetail 单独取。
export type ChannelStats = StatsMetrics & {
    channel_id: number;
    channel_name: string;
    enabled: boolean;
    models: ChannelModelStats[];
};

// ChannelModelStatsFormatted 是单个渠道模型的展示用统计。
export type ChannelModelStatsFormatted = {
    model_id: number;
    model_name: string;
    formatted: StatsMetricsFormatted;
};

// ChannelStatsFormatted 是单个渠道及其模型的展示用统计，同时充当渠道列表项。
// 名称与启停随统计一并给出，列表页由此只消费这一条查询：模型个数即 models.length，
// 整份配置在点开编辑时由 useChannelDetail 单独取。
export type ChannelStatsFormatted = {
    channel_id: number;
    channel_name: string;
    enabled: boolean;
    models: ChannelModelStatsFormatted[];
    formatted: StatsMetricsFormatted;
};

// FetchModelRequest 按指定凭据试拉上游模型列表。
// 渠道尚未保存时也可试拉，故随请求携带整份渠道配置：探测用的地址、协议路径、代理、Header 与过滤表达式
// 必须和保存后生效的完全一致，直接给编辑态即可，探测用不上的字段后端忽略。
// 名称可为空：探测常发生在渠道尚未命名时，后端只要求地址非空。
// probe 为真时后端对每个候选模型逐一实测 chat / response / message 三协议，协议位以实测结论为准。
type FetchModelRequest = {
    channel: Omit<ChannelDetail, 'id' | 'keys' | 'models' | 'grants'>;
    key: string;
    probe?: boolean;
};

// ProtocolProbe 是单个协议对单个模型的实测结论，随 FetchModel.probes 下发。
// ok 为真表示上游按该协议完成了最小请求；失败时 error 携带上游原文摘要。
export type ProtocolProbe = {
    protocol: number; // Protocol 位掩码。
    ok: boolean;
    status: number; // 上游 HTTP 状态码；网络错误或超时未及响应为 0。
    error?: string;
};

// FetchModel 是探测到的单个上游模型及其支持的协议集合。
// 普通拉取记 /models 所在侧：OpenAI 侧记 Response（Chat 已被官方标记弃用，需手动勾选）。
// 实测拉取（probe=true）只含实测通过的位，并附 probes 明细，可据此了解每个模型各协议的支持情况。
export type FetchModel = {
    name: string;
    protocols: number; // Protocol 位掩码。
    probes?: ProtocolProbe[];
};

// channelGrantListQueryOptions 供分组页查询可选授权。
// 与渠道列表分开: 选取成员只需名称与可用性, 拉整份渠道会连带路径, 代理与凭据明文。
export const channelGrantListQueryOptions = queryOptions({
    queryKey: ['channels', 'grants'],
    queryFn: () => apiRequest<ChannelGrantCandidate[]>('/api/v1/channel/grants'),
});

// useChannelGrantList 获取分组页可选的全部渠道授权。
export function useChannelGrantList(enabled = true) {
    return useQuery({ ...channelGrantListQueryOptions, enabled, refetchOnMount: 'always' });
}

// channelStatsFormattedQueryOptions 统一渠道统计查询, 格式化和刷新策略。
// 与配置分开刷新: 统计每次转发都在变, 配置只在人工改动后由 mutation 失效。
const channelStatsFormattedQueryOptions = queryOptions({
    ...channelStatsQueryOptions,
    select: (data) => data.map((item): ChannelStatsFormatted => ({
        channel_id: item.channel_id,
        channel_name: item.channel_name,
        enabled: item.enabled,
        models: item.models.map((channelModel) => ({
            model_id: channelModel.model_id,
            model_name: channelModel.model_name,
            formatted: formatStatsMetrics(channelModel),
        })),
        formatted: formatStatsMetrics(item),
    })),
    refetchInterval: 30000,
    refetchOnMount: 'always',
});

// useChannelStats 获取全部渠道及其模型的展示用统计, 也是渠道列表页的数据来源。
export function useChannelStats(enabled = true) {
    return useQuery({ ...channelStatsFormattedQueryOptions, enabled });
}

/**
 * 获取单个渠道完整配置 Hook, 供编辑表单打开时读取; id 为空时不发请求。
 * 不随统计一并取回: 整份配置带着路径、代理与凭据明文, 只有正在编辑的那一个渠道用得上。
 *
 * @example
 * const { data: detail } = useChannelDetail(channelId);
 */
export function useChannelDetail(id?: number) {
    return useQuery({
        queryKey: ['channels', 'detail', id],
        queryFn: () => apiRequest<ChannelDetail>(`/api/v1/channel/detail/${id}`),
        enabled: id !== undefined,
        refetchOnMount: 'always',
    });
}

/**
 * 创建渠道 Hook；提交整份配置，id 取 0 由后端分配。
 * 授权与凭据、模型在同一请求提交：授权按名称引用两侧，后端在同一事务内解析为主键。
 *
 * @example
 * const createChannel = useCreateChannel();
 *
 * createChannel.mutate({
 *   id: 0,
 *   name: 'OpenAI',
 *   base_url: 'https://api.openai.com',
 *   keys: [{ name: 'default', key: 'sk-xxx', enabled: true }],
 *   models: ['gpt-4o'],
 *   grants: [{ model_name: 'gpt-4o', key_name: 'default', protocols: Protocol.OpenAIResponse }],
 *   // ...其余配置字段
 * });
 */
export function useCreateChannel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: ChannelDetail) =>
            apiRequest<ChannelDetail>('/api/v1/channel/create', { method: 'POST', body: data }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['channels'] });
            queryClient.invalidateQueries({ queryKey: modelListQueryOptions.queryKey });
        },
    });
}

/**
 * 更新渠道 Hook；提交整份配置，整体替换。
 * keys、models 和 grants 提交即覆盖：未列出的凭据和模型会被删除并级联删除其授权。
 * grants 的 protocols 不能为 0 或含未定义位，名称也必须属于同一渠道，否则后端整单拒绝。
 *
 * @example
 * const updateChannel = useUpdateChannel();
 *
 * updateChannel.mutate({ ...detail, enabled: false });
 */
export function useUpdateChannel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: ChannelDetail) =>
            apiRequest<ChannelDetail>('/api/v1/channel/update', { method: 'POST', body: data }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['channels'] });
            queryClient.invalidateQueries({ queryKey: modelListQueryOptions.queryKey });
            queryClient.invalidateQueries({ queryKey: groupListQueryOptions.queryKey });
        },
    });
}

/**
 * 删除渠道 Hook
 * 
 * @example
 * const deleteChannel = useDeleteChannel();
 * 
 * deleteChannel.mutate(1); // 删除 ID 为 1 的渠道
 */
export function useDeleteChannel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<null>(`/api/v1/channel/delete/${id}`, { method: 'DELETE' }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['channels'] });
            queryClient.invalidateQueries({ queryKey: modelListQueryOptions.queryKey });
            queryClient.invalidateQueries({ queryKey: groupListQueryOptions.queryKey });
        },
    });
}

/**
 * 启用/禁用渠道 Hook
 * 
 * @example
 * const enableChannel = useEnableChannel();
 * 
 * enableChannel.mutate({ id: 1, enabled: true }); // 启用 ID 为 1 的渠道
 * enableChannel.mutate({ id: 1, enabled: false }); // 禁用 ID 为 1 的渠道
 */
export function useEnableChannel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: { id: number; enabled: boolean }) =>
            apiRequest<null>('/api/v1/channel/enable', { method: 'POST', body: data }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['channels'] });
            // 渠道启停会改变其授权在分组内的可用性，成员列表要跟着刷新。
            queryClient.invalidateQueries({ queryKey: groupListQueryOptions.queryKey });
        },
    });
}

/**
 * 获取渠道模型列表 Hook
 *
 * @example
 * const fetchModel = useFetchModel();
 *
 * // probe 为真时后端逐模型实测三协议，协议位以实测结论为准
 * fetchModel.mutate({
 *   channel: { base_url: 'https://api.openai.com', openai_response_path: '/v1/responses', ... },
 *   key: 'sk-xxx',
 *   probe: true,
 * });
 *
 * // 在 onSuccess 中获取模型列表，实测模式附 probes 明细
 * fetchModel.data // [{ name: 'gpt-4o', protocols: 6, probes: [...] }, ...]
 */
export function useFetchModel() {
    return useMutation({
        mutationFn: (data: FetchModelRequest) =>
            apiRequest<FetchModel[]>('/api/v1/channel/fetch-model', { method: 'POST', body: data }),
    });
}

// T-usability-001 渠道可用性诊断。
//
// 回答的是「我配好的模型为什么用不上」：一个模型要能被客户端调用，
// 必须三层齐全（模型 → 凭据授权 → 分组），任何一层断掉的表现都是
// 客户端报 model not found，而界面上完全看不出是哪一层。
// 这里把断点与可执行的原因一并取回来展示。
export interface ChannelDiagnoseModel {
    model_name: string;
    /** 被授权使用该模型的凭据名；为空即「没有任何凭据被授权」。 */
    grant_keys: string[];
    /** 对应的自动分组名；空串表示分组不存在。 */
    group_name: string;
    usable: boolean;
    /** 不可用时的可执行原因（不是「不可用」三个字）。 */
    reason?: string;
}

export interface ChannelDiagnose {
    channel_id: number;
    channel_name: string;
    enabled: boolean;
    key_count: number;
    usable_count: number;
    total_count: number;
    models: ChannelDiagnoseModel[];
    /** 只含不可用的，供界面直接渲染「待处理清单」。 */
    broken: ChannelDiagnoseModel[];
}

export interface ChannelDiagnoseSummary {
    channels: number;
    total_models: number;
    usable_models: number;
    broken_models: number;
    channels_with_gap: number;
}

export interface ChannelDiagnoseResponse {
    summary: ChannelDiagnoseSummary;
    /** 原因归类（原因文本 → 模型数），让用户知道该优先修哪一类。 */
    reasons: Record<string, number>;
    channels: ChannelDiagnose[];
}

// useChannelDiagnose 取回全部渠道的可用性诊断。
// 按需开启：它要遍历全部渠道的模型与授权，不在渠道列表首屏就拉。
export function useChannelDiagnose(enabled = true) {
    return useQuery({
        queryKey: ['channels', 'diagnose'],
        queryFn: () => apiRequest<ChannelDiagnoseResponse>('/api/v1/channel/diagnose'),
        enabled,
    });
}
// T-usability-006 渠道「配置 vs 上游」对比。
//
// 回答的是「客户端列表里那些模型，上游到底有没有」——
// 渠道诊断（看配置层）答不了这个：配置齐全不代表上游提供。
export interface UpstreamCheckResult {
    channel_id: number;
    channel_name: string;
    probe_ok: boolean;
    /** 探测失败的原因（凭据/网络问题）。此时下面的差异字段无意义。 */
    probe_error?: string;
    configured_count: number;
    upstream_count: number;
    /**
     * 配置里**同时出现在上游清单中**的数量。
     *
     * 刻意不叫 effective_count（有效数）：上游 /v1/models 并不完整，
     * 未列出的模型仍可能正常响应，所以这个数只能读作「上游主动声明的下界」。
     */
    listed_count?: number;
    upstream_models?: string[];
    /** 配置里写了、但上游清单里没列出的模型。**不等于调不通**，见 warning。 */
    missing_upstream?: string[];
    /** 上游有、配置里没写的模型。 */
    not_configured?: string[];
    /** 语义警告：提醒「清单未列出」不等于「调不通」。 */
    warning?: string;
    /** 无需对比时的说明（如渠道没有启用的凭据）。 */
    note?: string;
}

// useUpstreamCheck 对单个渠道做上游对比。
// 按渠道 id 缓存并手动触发：它会真的打向上游，不该在列表渲染时逐个发请求。
export function useUpstreamCheck(channelId?: number, enabled = false) {
    return useQuery({
        queryKey: ['channels', 'upstream-check', channelId],
        queryFn: () => apiRequest<UpstreamCheckResult>(`/api/v1/channel/${channelId}/upstream-check`),
        enabled: enabled && channelId !== undefined,
        // 探测结果有时效性（上游随时可能变），但不该在窗口聚焦时重炸一遍上游。
        refetchOnWindowFocus: false,
        staleTime: 5 * 60 * 1000,
    });
}
// T-verify-004 渠道模型的批量可用性实测。
//
// 回答「上游到底认不认这个名字」——三层诊断都答不了：
//   配置层诊断：看的是"我们自己这边的账"，齐全也不代表上游认
//   上游清单对比：清单本身不完整（实测有模型不在清单里却能调通）
// 唯一可靠的判据是**发一次真实请求**。
export interface ModelVerifyResult {
    model: string;
    /** 上游接受了这次请求（HTTP 2xx）。 */
    usable: boolean;
    /** 上游 HTTP 状态码；**0 表示请求没发出去**（网络/代理问题）。 */
    status: number;
    error?: string;
    latency_ms: number;
}

export interface ModelVerifyResponse {
    channel_id: number;
    channel_name: string;
    total: number;
    usable: number;
    unusable: number;
    /** 模型数超过上限被截断 —— 用户有权知道实测没覆盖全部。 */
    truncated?: boolean;
    max_targets?: number;
    results: ModelVerifyResult[];
    /** 无需实测时的说明（如没有「已启用凭据+已授权」的组合）。 */
    note?: string;
}

// verifyChannelModels 对一个渠道做批量实测。
//
// **这是个会真打上游的操作**（每个模型一次请求），所以做成命令式而非 query：
// 绝不挂在任何自动渲染路径上。并发在后端固定为 3（防风控）。
export async function verifyChannelModels(channelId: number): Promise<ModelVerifyResponse> {
    return apiRequest<ModelVerifyResponse>(`/api/v1/channel/${channelId}/verify-models`, {
        method: 'POST',
    });
}
// T-perf-001 渠道延迟画像。
//
// 实测发现 17 个渠道的上游 TLS 握手从 1ms 到 1177ms（差三个数量级），
// 慢的上游会拖慢每一次转发。relay_logs 里早就有首字节耗时，这里把它变成画像。
//
// 用中位数而不是平均值：首字节分布是长尾的，偶发卡顿会把均值拉高，
// 让一个平时很快的渠道看起来很差 —— 用户感知到的"典型速度"是中位数。
export interface ChannelLatencyRow {
    channel: string;
    /** 样本数。**太少时下面的数字不可信**，界面据此决定是否展示。 */
    samples: number;
    /** 真的记到首字节的样本数。为 0 时首字节那两个数字无意义（应显示为「—」）。 */
    first_byte_samples: number;
    first_byte_p50_ms: number;
    first_byte_p90_ms: number;
    duration_p50_ms: number;
    /** 首字节超过阈值的次数，用来分辨「慢是常态还是偶发」。 */
    slow_count: number;
}

export interface ChannelLatencyStats {
    /** 判定「慢」的阈值，随结果返回 —— 界面上的「慢」必须与它对应。 */
    slow_threshold_ms: number;
    /** 扫描到的日志条数（含未走到渠道的，它们的落差本身是信号）。 */
    window: number;
    /** 按首字节中位数升序：**快的在前**（看这个表是为了挑快的用）。 */
    channels: ChannelLatencyRow[];
}

export function useChannelLatency(window = 500, enabled = true) {
    return useQuery({
        queryKey: ['channels', 'latency', window],
        queryFn: () => apiRequest<ChannelLatencyStats>(`/api/v1/channel/latency?window=${window}`),
        enabled,
    });
}