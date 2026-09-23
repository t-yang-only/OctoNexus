import { useMutation, useQuery } from '@tanstack/react-query';
import { useEffect } from 'react';
import { apiRequest } from './client';
import { queryClient } from './client';
import { groupListQueryOptions } from './queries';

// GroupMode 表示分组的手动或故障转移路由模式。
export type GroupMode = 'manual' | 'failover' | 'lowest_cost' | 'quality_first' | 'lowest_latency' | 'least_busy' | 'lowest_tpm_rpm' | 'weighted' | 'smart' | 'allocate';
// smart 是智能路由（对齐阶跃 Step Router 的用法）：按请求特征（轮数/输入量/工具数）判复杂度，
// 复杂走靠前的成员、简单走靠后的成员；阈值见 relay_config.smart_route_threshold。
// allocate 是额度分压（T-allocate-001）：按成员各自的剩余请求数按比例分配流量——
// 每个 key 只有一点钱时，几条小余额的 key 一起顶上一个大的，而不是把流量全压在同一个成员上。

// GroupRelayConfig 保存分组 Relay 配置。
export interface GroupRelayConfig {
    member_max_attempts: number;
    member_retry_interval_seconds: number;
    member_non_stream_response_timeout_seconds: number;
    member_stream_first_event_timeout_seconds: number;
    // 流式「无进展」上限（T-timeout-001）：首个事件之后允许的最长静默秒数，0 = 关闭。
    member_stream_idle_timeout_seconds: number;
    member_cooldown_seconds: number;
    member_affinity_seconds: number;
    // 首字竞速 (T-hedge-001): 提交首字节之前并发请求排序靠前的多个成员, 取最快给出有效响应者。
    hedge_enabled: boolean;
    hedge_width: number;
    hedge_after_ms: number;
    hedge_peak_in_flight: number;
    // 智能路由（mode = smart）的复杂度阈值 1..100，缺省 50：评分 ≥ 阈值走靠前的成员（决策引擎档）。
    smart_route_threshold: number;
}

// GroupItem 是分组内一条可路由的成员：或引用一条渠道授权，或引用一个子分组，二者互斥。
// 名称、所属渠道与可用性由后端补齐：授权成员是 (模型, 凭据) 的组合，界面只需展示与排序，无需再按主键回查。
// 嵌套口径（T-group-002）：子分组成员在选路时展平，available 为真表示其下至少一个成员可转发。
export interface GroupItem {
    id: number;
    group_id: number;
    channel_grant_id?: number; // 引用的渠道授权 ID；子分组成员缺省。
    child_group_id?: number; // 引用的子分组 ID；授权成员缺省。
    priority: number;
    channel_id: number;
    channel_name: string;
    model_name: string;
    key_name: string;
    protocols: number; // 该授权支持的 Protocol 位掩码。
    child_group_name: string; // 子分组成员的目标分组名称；授权成员为空。
    available: boolean; // 为假表示该成员当前无法转发，但仍会列出以便移除。
    // smart_tier 是智能路由（mode=smart）的显式档位：空 = 按成员顺序自动对半切分（既有行为），
    // 'decision' = 决策引擎档（强/贵），'execution' = 执行引擎档（快/便宜）。
    // 顶层成员的档位会随展平下传给整条链（子分组整条链归档），因此标在子分组上也生效。
    smart_tier?: string;
}

// GroupRuntime 是分组的实时路由状态。
// current_item_id 两种模式共用：手动模式下即人工指定的成员，故障转移模式下由 Relay 的路由决定。
export interface GroupRuntime {
    group_id: number;
    current_item_id: number;
    probe_item_id: number;
    affinity_until: number;
    cooldowns: Record<number, number>;
}

// Group 是客户端模型名称对应的渠道分组。
export interface Group {
    id: number;
    name: string;
    mode: GroupMode;
    relay_config: GroupRelayConfig;
    items: GroupItem[]; // 恒为数组，后端读取侧承诺不为 null。
    runtime: GroupRuntime; // 随分组一并返回；当前成员一律读 runtime.current_item_id。
}

// GroupItemInput 是提交的成员，按渠道授权主键或子分组主键引用，二者互斥由后端校验；提交顺序即优先级顺序。
export interface GroupItemInput {
    // smart_tier 只在智能路由下有值：空/缺省 = 按成员顺序自动对半切分，'decision'/'execution' = 显式档位。
    smart_tier?: string;
    channel_grant_id: number; // 待引用的渠道授权 ID；引用子分组时为 0。
    child_group_id: number; // 待引用的子分组 ID；引用渠道授权时为 0。
}

// GroupCreateRequest 是创建分组的请求。
export interface GroupCreateRequest {
    name: string;
    mode: GroupMode;
    relay_config: GroupRelayConfig;
    items: GroupItemInput[];
}

// GroupUpdateRequest 是分组配置、成员与当前成员的变更；items 为整体替换，按授权主键匹配保留已有成员。
// 只提交发生变化的字段；当前成员是分组的普通字段，与其余变更共用本请求。
export interface GroupUpdateRequest {
    name?: string;
    mode?: GroupMode;
    relay_config?: GroupRelayConfig;
    items?: GroupItemInput[];
    active_item_id?: number; // 手动模式指定的当前成员，0 表示取消选择。
}

// writeGroupCache 把一份分组写回列表与详情两处缓存，已存在则替换，不存在则插入。
// 写操作的响应与事件流都经由此处收敛，两处读的是同一份数据，无需再重新拉取列表。
function writeGroupCache(group: Group) {
    queryClient.setQueryData(groupListQueryOptions.queryKey, (current: Group[] | undefined) => {
        if (!current) return current;
        const next = current.filter((item) => item.id !== group.id);
        next.push(group);
        // 与后端 op.GroupList 的定序保持一致：API Key 表单的模型选择器没有排序开关，依赖列表自带的名称顺序。
        next.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
        return next;
    });
    queryClient.setQueryData(['groups', 'detail', group.id], group);
}

// removeGroupCache 从列表与详情两处缓存移除一个分组。
function removeGroupCache(id: number) {
    queryClient.setQueryData(groupListQueryOptions.queryKey, (current: Group[] | undefined) =>
        current?.filter((item) => item.id !== id)
    );
    queryClient.removeQueries({ queryKey: ['groups', 'detail', id] });
}

// useGroupList 获取全部分组，并由明确需要实时状态的页面控制是否订阅事件流。
export function useGroupList(enabled = true, eventsEnabled = false) {
    const query = useQuery({ ...groupListQueryOptions, enabled });

    useGroupEventStream(enabled && eventsEnabled);

    return query;
}

// useGroup 获取单个分组，供只关心一个分组的页面使用；实时状态按需订阅。
export function useGroup(id: number | undefined, enabled = true, eventsEnabled = false) {
    const query = useQuery({
        queryKey: ['groups', 'detail', id],
        queryFn: () => apiRequest<Group>(`/api/v1/group/get/${id}`),
        enabled: enabled && id !== undefined,
    });

    useGroupEventStream(enabled && eventsEnabled);

    return query;
}

// groupEventSource 是全应用共享的一条分组事件连接，由订阅方引用计数维持。
// 列表页与日志详情可能同时订阅，各自建连会白占浏览器对同域的连接数。
let groupEventSource: EventSource | null = null;
let groupEventRefCount = 0; // 当前订阅该连接的组件数量，归零时关闭连接。

// useGroupEventStream 订阅分组的变更事件与运行状态增量，并写回列表与详情两处缓存。
function useGroupEventStream(enabled: boolean) {
    useEffect(() => {
        if (!enabled) return;

        groupEventRefCount++;
        if (!groupEventSource) {
            const source = new EventSource('/api/v1/group/events', { withCredentials: true });
            groupEventSource = source;
            source.addEventListener('changed', (event) => {
                writeGroupCache(JSON.parse((event as MessageEvent<string>).data) as Group);
            });
            source.addEventListener('deleted', (event) => {
                removeGroupCache(Number((event as MessageEvent<string>).data));
            });
            source.addEventListener('runtime', (event) => {
                const update = JSON.parse((event as MessageEvent<string>).data) as GroupRuntime;
                queryClient.setQueryData(groupListQueryOptions.queryKey, (current: Group[] | undefined) =>
                    current?.map((group) => group.id === update.group_id ? { ...group, runtime: update } : group)
                );
                queryClient.setQueryData(['groups', 'detail', update.group_id], (current: Group | undefined) =>
                    current && { ...current, runtime: update }
                );
            });
            // 后端不留事件历史，连接断开期间的变更无从补发，故每次连上都重新拉取一次对齐。
            // 拉取放在 onopen 而非 onerror: EventSource 自行重连，onerror 在每次失败时都会触发，
            // 写在那里会让断网期间反复全量重拉。
            source.onopen = () => {
                queryClient.invalidateQueries({ queryKey: groupListQueryOptions.queryKey });
                queryClient.invalidateQueries({ queryKey: ['groups', 'detail'] });
            };
        }

        return () => {
            groupEventRefCount--;
            if (groupEventRefCount > 0) return;
            groupEventSource?.close();
            groupEventSource = null;
        };
    }, [enabled]);
}

// useCreateGroup 创建分组。
export function useCreateGroup() {
    return useMutation({
        mutationFn: (data: GroupCreateRequest) =>
            apiRequest<Group>('/api/v1/group/create', { method: 'POST', body: data }),
        onSuccess: writeGroupCache,
    });
}

// useUpdateGroup 更新分组配置、成员或当前成员，响应即变更后的完整分组。
export function useUpdateGroup() {
    return useMutation({
        mutationFn: ({ id, ...data }: GroupUpdateRequest & { id: number }) =>
            apiRequest<Group>(`/api/v1/group/update/${id}`, { method: 'POST', body: data }),
        onSuccess: writeGroupCache,
    });
}

// useDeleteGroup 删除分组。
export function useDeleteGroup() {
    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<null>(`/api/v1/group/delete/${id}`, { method: 'DELETE' }),
        onSuccess: (_, id) => removeGroupCache(id),
    });
}

// T-usability-009 分组使用情况。
//
// 生产实测：418 个分组里只有 41 个被调用过（94% 从未使用），
// 而它们**全部**是客户端可直接调用的模型名 —— 用户在 AI 客户端里会看到 418 个条目。
export interface GroupUsageItem {
    group_id: number;
    name: string;
    /** 选路模式（manual/allocate/smart）—— 比「是否启用」更能说明这个分组是干什么的。 */
    mode: string;
    item_count: number;
    /** 名字形态是「渠道名/模型名」—— 保存渠道时自动生成的分组。 */
    is_auto: boolean;
    /** 窗口内调用次数；0 表示从未被调用过。 */
    call_count: number;
    /** 最后一次调用的时间；零值表示从未调用。 */
    last_call_at: string;
}

export interface GroupUsageStats {
    total: number;
    used: number;
    unused: number;
    auto: number;
    manual: number;
    window: number;
    groups: GroupUsageItem[];
}

// useGroupUsage 取分组使用情况。
//
// 注意这个接口的定位：**只陈述事实，不给「该删哪个」的结论**。
// 未被调用不等于该删（备用分组、待启用分组合法地没有流量），
// 删不删由掌握上下文的人决定。
export function useGroupUsage(enabled = true) {
    return useQuery({
        queryKey: ['groups', 'usage'],
        queryFn: () => apiRequest<GroupUsageStats>('/api/v1/group/usage'),
        enabled,
    });
}