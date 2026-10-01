import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Scale } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { useSettingList, useSetSetting, SettingKey, type Setting } from '@/api/setting';
import { toast } from 'sonner';
import { WeightSettingField } from './WeightSettingField';

// SettingRouting 是「路由策略」功能区：选路开关、加权维度、额度分压、速度应对与入站转发上限。
// 这些设置全部由选路层与转发层读取（T-smart/allocate/speed/bodylimit），面板只负责写值。
export function SettingRouting() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const [routeBalanceEnabled, setRouteBalanceEnabled] = useState(false);
    const [routeProbeEnabled, setRouteProbeEnabled] = useState(false);
    const [routeProbeInterval, setRouteProbeInterval] = useState('');

    const initialRouteBalanceEnabled = useRef(false);
    const initialRouteProbeEnabled = useRef(false);
    const initialRouteProbeInterval = useRef('');

    useEffect(() => {
        if (!settings) return;
        const list = settings as Setting[];
        const balance = list.find(s => s.key === SettingKey.RouteBalanceEnabled);
        const probe = list.find(s => s.key === SettingKey.RouteProbeEnabled);
        const probeInterval = list.find(s => s.key === SettingKey.RouteProbeInterval);
        if (balance) {
            const enabled = balance.value === 'true';
            queueMicrotask(() => setRouteBalanceEnabled(enabled));
            initialRouteBalanceEnabled.current = enabled;
        }
        if (probe) {
            const enabled = probe.value === 'true';
            queueMicrotask(() => setRouteProbeEnabled(enabled));
            initialRouteProbeEnabled.current = enabled;
        }
        if (probeInterval) {
            queueMicrotask(() => setRouteProbeInterval(probeInterval.value));
            initialRouteProbeInterval.current = probeInterval.value;
        }
    }, [settings]);

    // save 只在值真的变了时才落库，成功后就地推进 initial，避免二次 onBlur 重复提交。
    const save = (key: string, value: string, onSaved: () => void) => {
        setSetting.mutate({ key, value }, {
            onSuccess: () => {
                onSaved();
                toast.success(t('saved'));
            },
            onError: (error) => toast.error(error instanceof Error ? error.message : String(error)),
        });
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 space-y-5">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Scale className="h-5 w-5" />
                {t('routing.title')}
            </h2>

            {/* 均衡开关 */}
            <div className="flex items-center justify-between gap-4">
                <div>
                    <p className="text-sm font-medium">{t('routing.balance')}</p>
                    <p className="text-xs text-muted-foreground">{t('routing.balanceHint')}</p>
                </div>
                <Switch
                    checked={routeBalanceEnabled}
                    onCheckedChange={(checked) => {
                        setRouteBalanceEnabled(checked);
                        if (String(checked) === String(initialRouteBalanceEnabled.current)) return;
                        save(SettingKey.RouteBalanceEnabled, String(checked), () => { initialRouteBalanceEnabled.current = checked; });
                    }}
                />
            </div>

            {/* 冷却成员主动探活 */}
            <div className="flex items-center justify-between gap-4">
                <div>
                    <p className="text-sm font-medium">{t('routing.probe')}</p>
                    <p className="text-xs text-muted-foreground">{t('routing.probeHint')}</p>
                </div>
                <Switch
                    checked={routeProbeEnabled}
                    onCheckedChange={(checked) => {
                        setRouteProbeEnabled(checked);
                        if (String(checked) === String(initialRouteProbeEnabled.current)) return;
                        save(SettingKey.RouteProbeEnabled, String(checked), () => { initialRouteProbeEnabled.current = checked; });
                    }}
                />
            </div>
            <label className="grid gap-1 text-xs text-muted-foreground">
                {t('routing.probeInterval')}
                <Input
                    type="number"
                    min="0"
                    value={routeProbeInterval}
                    onChange={(e) => setRouteProbeInterval(e.target.value)}
                    onBlur={() => {
                        if (routeProbeInterval === initialRouteProbeInterval.current) return;
                        save(SettingKey.RouteProbeInterval, routeProbeInterval, () => { initialRouteProbeInterval.current = routeProbeInterval; });
                    }}
                    className="rounded-xl"
                />
            </label>

            {/* 加权综合选路（weighted 模式）的维度权重: 0 表示该维度不参与, 9 个维度合计不必凑满 100。 */}
            <div id="weighted-weights" className="grid gap-3 rounded-2xl border border-border/50 bg-muted/20 p-3">
                <div>
                    <p className="text-sm font-semibold">{t('routing.weights')}</p>
                    <p className="text-xs text-muted-foreground">{t('routing.weightsHint')}</p>
                </div>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                    <WeightSettingField settingKey="route_weight_cost" label={t('routing.weightCost')} kind="number" />
                    <WeightSettingField settingKey="route_weight_quality" label={t('routing.weightQuality')} kind="number" />
                    <WeightSettingField settingKey="route_weight_latency" label={t('routing.weightLatency')} kind="number" />
                    <WeightSettingField settingKey="route_weight_busy" label={t('routing.weightBusy')} kind="number" />
                    <WeightSettingField settingKey="route_weight_load" label={t('routing.weightLoad')} kind="number" />
                    <WeightSettingField settingKey="route_weight_multiplier" label={t('routing.weightMultiplier')} kind="number" />
                    <WeightSettingField settingKey="route_weight_per_call" label={t('routing.weightPerCall')} kind="number" />
                    <WeightSettingField settingKey="route_weight_balance" label={t('routing.weightBalance')} kind="number" />
                    <WeightSettingField settingKey="route_weight_monthly" label={t('routing.weightMonthly')} kind="number" />
                    <WeightSettingField settingKey="route_monthly_exhausted_action" label={t('routing.monthlyAction')} kind="select"
                        options={[{ value: 'demote', label: t('routing.monthlyDemote') }, { value: 'exclude', label: t('routing.monthlyExclude') }]} />
                    {/* 上游判定"请求本身非法"（400 一类）时的取向: 换成员再试 / 立刻回上游原文 */}
                    <WeightSettingField settingKey="relay_request_fault_action" label={t('routing.requestFaultAction')} kind="select"
                        options={[{ value: 'failover', label: t('routing.requestFaultFailover') }, { value: 'failfast', label: t('routing.requestFaultFailfast') }]} />
                </div>
            </div>

            {/* 额度分压（allocate 模式, T-allocate-001）：剩余请求数的折算、健康折扣与自限流。 */}
            <div id="allocate-settings" className="grid gap-3 rounded-2xl border border-border/50 bg-muted/20 p-3">
                <div>
                    <p className="text-sm font-semibold">{t('routing.allocateTitle')}</p>
                    <p className="text-xs text-muted-foreground">{t('routing.allocateHint')}</p>
                </div>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                    <WeightSettingField settingKey="route_allocate_estimate_tokens" label={t('routing.allocateTokens')} hint={t('routing.allocateTokensHint')} kind="number" max="2000000" />
                    <WeightSettingField settingKey="route_allocate_health_weight" label={t('routing.allocateHealth')} hint={t('routing.allocateHealthHint')} kind="number" />
                    <WeightSettingField settingKey="route_allocate_slow_latency_ms" label={t('routing.allocateSlowMs')} hint={t('routing.allocateSlowMsHint')} kind="number" max="600000" />
                    <WeightSettingField settingKey="route_allocate_min_requests" label={t('routing.allocateMinRequests')} hint={t('routing.allocateMinRequestsHint')} kind="number" max="1000000" />
                    <WeightSettingField settingKey="route_member_rpm_limit" label={t('routing.memberRpmLimit')} hint={t('routing.memberRpmLimitHint')} kind="number" max="1000000" />
                    <WeightSettingField settingKey="route_member_tpm_limit" label={t('routing.memberTpmLimit')} hint={t('routing.memberTpmLimitHint')} kind="number" max="1000000000" />
                    <WeightSettingField settingKey="route_ratelimit_cooldown_max_seconds" label={t('routing.throttleCap')} hint={t('routing.throttleCapHint')} kind="number" max="86400" />
                </div>
                {/* 速度维度（T-speed-001）：把"速度不好"变成两个可调的量——谁算慢、慢成员少拿多少流量,
                    外加一条"慢成员更早被放弃"的自适应首帧看门狗。 */}
                <div className="rounded-xl border border-border/60 p-3">
                    <p className="text-sm font-medium">{t('routing.speedTitle')}</p>
                    <p className="text-xs text-muted-foreground">{t('routing.speedHint')}</p>
                    <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-3">
                        <WeightSettingField settingKey="route_allocate_speed_weight" label={t('routing.speedWeight')} hint={t('routing.speedWeightHint')} kind="number" />
                        <WeightSettingField settingKey="route_speed_slow_ttfb_ms" label={t('routing.speedSlowTtfb')} hint={t('routing.speedSlowTtfbHint')} kind="number" max="600000" />
                        <WeightSettingField settingKey="route_speed_slow_tps" label={t('routing.speedSlowTps')} hint={t('routing.speedSlowTpsHint')} kind="number" max="100000" />
                        <WeightSettingField settingKey="route_speed_first_event_multiple" label={t('routing.speedFeMultiple')} hint={t('routing.speedFeMultipleHint')} kind="number" max="100" />
                        <WeightSettingField settingKey="route_speed_first_event_floor_ms" label={t('routing.speedFeFloor')} hint={t('routing.speedFeFloorHint')} kind="number" max="600000" />
                    </div>
                </div>
                {/* 入站正文上限（T-bodylimit-001）：依赖库把上限写死成 64 MiB，Codex 的 remote compact
                    这类大正文会被自家网关先拒掉（报错还长得像上游错误）。这里把它变成可调的一项：
                    0 = 不限制，默认 256 MiB。提示里要写明"开大上限=单请求内存上升"的代价。 */}
                <div className="rounded-xl border border-border/60 p-3">
                    <p className="text-sm font-medium">{t('routing.bodyLimitTitle')}</p>
                    <p className="text-xs text-muted-foreground">{t('routing.bodyLimitHint')}</p>
                    <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-3">
                        <WeightSettingField settingKey="relay_max_request_body_bytes" label={t('routing.bodyLimit')} hint={t('routing.bodyLimitHint2')} kind="number" max="4294967296" />
                    </div>
                </div>
            </div>
        </div>
    );
}
