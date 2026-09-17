import type { ChannelDetail } from '@/api/channel';
import { unsupportedPairs, type KeyModelIndex } from './grants';

// ChannelFormState 是渠道表单的全部可编辑内容。
// 全按名称组织而不存主键: 后端凭据与模型都按名称匹配增删改, 而新建渠道和新加模型时主键尚不存在。
export type ChannelFormState = {
    name: string;
    dialect: ChannelDetail['dialect'];
    base_url: string;
    enabled: boolean;
    proxy: boolean;
    openai_chat_completion_path: string;
    openai_response_path: string;
    anthropic_message_path: string;
    keys: { name: string; key: string; enabled: boolean }[];
    models: string[];
    grants: Map<string, number>; // 键为 grantKey(模型名, 凭据名), 值为 Protocol 位掩码。
    // 各凭据经探测确认支持的模型, 只用于收敛授权范围(全选跳过, 不可勾选, 保存前裁剪), 不提交给后端。
    // 编辑既有渠道时为空: 探测结论不随渠道保存, 旧表单里的授权也不足以反推某个凭据支持哪些模型。
    keyModels: KeyModelIndex;
    custom_header: ChannelDetail['custom_header'];
    channel_proxy: string;
    param_override: string;
    match_regex: string;
    // 计费事实: 数字字段留 0 表示未知（后端同样按零值=未知处理）。
    billing_mode: string;
    multiplier: number;
    per_call_price: number;
    monthly_quota: number;
    monthly_used: number;
};

// grantKey 生成授权在状态里的键; 分隔符取 \0, 模型名与凭据名都不会含它。
export function grantKey(modelName: string, keyName: string) {
    return `${modelName}\0${keyName}`;
}

export const emptyFormState: ChannelFormState = {
    name: '',
    dialect: 'generic',
    base_url: '',
    enabled: true,
    proxy: false,
    openai_chat_completion_path: '/v1/chat/completions',
    openai_response_path: '/v1/responses',
    anthropic_message_path: '/v1/messages',
    keys: [],
    models: [],
    grants: new Map(),
    keyModels: new Map(),
    custom_header: [],
    channel_proxy: '',
    param_override: '',
    match_regex: '',
    billing_mode: '',
    multiplier: 0,
    per_call_price: 0,
    monthly_quota: 0,
    monthly_used: 0,
};

// fromChannel 把渠道完整配置还原为表单状态; 授权读写都按名称, 直接建索引即可。
export function fromChannel(channel: ChannelDetail): ChannelFormState {
    return {
        name: channel.name,
        dialect: channel.dialect,
        base_url: channel.base_url,
        enabled: channel.enabled,
        proxy: channel.proxy,
        openai_chat_completion_path: channel.openai_chat_completion_path,
        openai_response_path: channel.openai_response_path,
        anthropic_message_path: channel.anthropic_message_path,
        keys: channel.keys.map(({ name, key, enabled }) => ({ name, key, enabled })),
        models: [...channel.models],
        grants: new Map(channel.grants.map((g) => [grantKey(g.model_name, g.key_name), g.protocols])),
        keyModels: new Map(),
        custom_header: channel.custom_header,
        channel_proxy: channel.channel_proxy,
        param_override: channel.param_override,
        match_regex: channel.match_regex,
        billing_mode: channel.billing_mode ?? '',
        multiplier: channel.multiplier ?? 0,
        per_call_price: channel.per_call_price ?? 0,
        monthly_quota: channel.monthly_quota ?? 0,
        monthly_used: channel.monthly_used ?? 0,
    };
}

// toChannelConfig 生成渠道自身的配置字段, 提交与探测共用。
// 探测只用得上其中的地址, 路径, 代理与过滤表达式, 但必须与保存后生效的完全一致, 故由同一处给出。
export function toChannelConfig(state: ChannelFormState) {
    return {
        name: state.name.trim(),
        dialect: state.dialect,
        enabled: state.enabled,
        base_url: state.base_url.trim(),
        openai_chat_completion_path: state.openai_chat_completion_path.trim(),
        openai_response_path: state.openai_response_path.trim(),
        anthropic_message_path: state.anthropic_message_path.trim(),
        proxy: state.proxy,
        custom_header: state.custom_header.filter((h) => h.header_key.trim() && h.header_value !== ''),
        channel_proxy: state.channel_proxy.trim(),
        param_override: state.param_override.trim(),
        match_regex: state.match_regex.trim(),
        billing_mode: state.billing_mode,
        multiplier: state.multiplier,
        per_call_price: state.per_call_price,
        monthly_quota: state.monthly_quota,
        monthly_used: state.monthly_used,
    };
}

// toChannelDetail 把表单状态还原为提交用的完整配置; 创建时 id 取 0, 由后端分配。
// 读写同构, 提交即全量: 无需与原渠道逐字段比对, 表单本就一次给出完整配置。
// 协议位为空的条目不是授权, 在此丢弃。
export function toChannelDetail(state: ChannelFormState, id: number): ChannelDetail {
    return {
        ...toChannelConfig(state),
        id,
        keys: state.keys.map(({ name, key, enabled }) => ({ name: name.trim(), key: key.trim(), enabled })),
        models: [...state.models],
        grants: [...state.grants]
            .filter(([, protocols]) => protocols !== 0)
            .map(([mapKey, protocols]) => {
                const [model_name, key_name] = mapKey.split('\0');
                return { model_name, key_name, protocols };
            }),
    };
}

// pruneUnsupportedGrants 是提交前的最后一道收敛: 删掉已知不可用的 (模型, 凭据) 授权。
// 与界面禁用同一份判据(见 grants.ts): 界面挡的是新勾选, 这里兜住界面之外就已存在的旧组合——
// 此前全量置位存下的授权正是这一类, 不删就会持续把请求派给供不了该模型的凭据。
// 只删有探测结论的组合; 一条都没删时原样返回, 调用方据此决定是否提示用户。
export function pruneUnsupportedGrants(state: ChannelFormState): { next: ChannelFormState; removed: number } {
    const pairs = unsupportedPairs(state.keyModels, state.models, state.keys.map((k) => k.name));
    if (pairs.length === 0) return { next: state, removed: 0 };
    const grants = new Map(state.grants);
    let removed = 0;
    for (const [modelName, keyName] of pairs) {
        const mapKey = grantKey(modelName, keyName);
        if (!grants.has(mapKey)) continue;
        grants.delete(mapKey);
        removed += 1;
    }
    if (removed === 0) return { next: state, removed: 0 };
    return { next: { ...state, grants }, removed };
}
