import { useState } from 'react';
import { toast } from 'sonner';
import { useTranslations } from 'use-intl';
import { useFetchModel } from '@/api/channel';
import { grantKey, toChannelConfig, type ChannelFormState } from './state';
import { recordProbe } from './grants';

// useModelProbe 按凭据探测上游模型列表, 供模型页与凭据页共用。
// 两侧并发探测, 逐模型三协议实测与协议位判定都在后端完成, 此处只负责转圈状态, 结果并入表单和结果提示。
// 探测一律带 probe=true: 协议位以实测结论为准, 谁讲得通就自动启用谁, 不再按 /models 侧猜测。
export function useModelProbe() {
    const t = useTranslations('channel.form');
    const fetchModel = useFetchModel();
    const [pendingKey, setPendingKey] = useState<string | null>(null); // 正在探测的凭据名称, 只转动该行的图标。

    // probe 探测指定凭据可用的模型, 结果并入模型集合与授权表。
    // 上游未返回但本地已有的模型保留: 静默删除会打断正在使用该模型的路由。
    // 已有授权不受实测结论降级: 探测只自动启用新证实的协议位, 人工勾选的位不会因本次上游偶发失败被撤销。
    const probe = async (
        state: ChannelFormState,
        setState: (next: ChannelFormState) => void,
        keyName: string,
    ) => {
        // 模型页的刷新按钮不按凭据禁用, 选中的凭据可能还没填 Key, 在此挡掉空请求。
        const channelKey = state.keys.find((k) => k.name === keyName);
        if (!channelKey || channelKey.key.trim() === '') return;

        setPendingKey(keyName);
        try {
            // 探测用的地址, 路径, 代理和过滤表达式必须和保存后生效的完全一致, 否则这里探到的模型
            // 与实际转发时能用的模型会不一样; 故与提交共用同一份配置, 探测用不上的字段由后端忽略。
            const fetched = await fetchModel.mutateAsync({
                channel: toChannelConfig(state),
                key: channelKey.key.trim(),
                probe: true,
            });
            if (fetched.length === 0) {
                toast.warning(t('modelRefreshEmpty'));
                return;
            }
            const models = [...state.models];
            const grants = new Map(state.grants);
            for (const { name, protocols } of fetched) {
                if (!models.includes(name)) models.push(name);
                const mapKey = grantKey(name, channelKey.name);
                grants.set(mapKey, (grants.get(mapKey) ?? 0) | protocols);
            }
            // 探测结论同时成为该凭据的支持清单, 授权矩阵据此收敛: 它供不了的模型既勾不上也不会被存下。
            const keyModels = recordProbe(state.keyModels, channelKey.name, fetched.map((m) => m.name));
            setState({ ...state, models, grants, keyModels });
            // 实测结论的模型数与 /models 返回一致, 协议位只多不少; 明细留待人工展开核对。
            toast.success(t('modelRefreshProbed', { count: fetched.length }));
        } catch (error) {
            toast.error(t('modelRefreshFailed'), { description: String(error) });
        } finally {
            setPendingKey(null);
        }
    };

    return { probe, pendingKey };
}
