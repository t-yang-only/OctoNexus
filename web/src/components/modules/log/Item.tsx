import { memo, useEffect, useMemo, useState, type CSSProperties } from 'react';
import { AlertCircle, ArrowDownToLine, ArrowLeftRight, ArrowRight, ArrowUpFromLine, Brain, BrainCircuit, Clock, Cpu, Database, DollarSign, Gauge, KeyRound, Loader2, Percent, Repeat2, Route, Square, Type, Zap } from 'lucide-react';
import { useTranslations } from 'use-intl';
import JsonView from '@uiw/react-json-view';
import { githubDarkTheme } from '@uiw/react-json-view/githubDark';
import { githubLightTheme } from '@uiw/react-json-view/githubLight';
import { useTheme } from '@/provider/theme';
import { type RelayLogOverview, useLogRequestBody, useLogResponseBody, useStopRound } from '@/api/log';
import { useGroup, useUpdateGroup } from '@/api/group';
import { Protocol } from '@/api/channel';
import { getModelIcon } from '@/lib/model-icons';
import { Badge } from '@/components/ui/badge';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';
import { formatCacheHitRate, formatCNYCost, formatFirstByteMs, formatTPS } from '@/lib/log-metrics';
import { formatJsonForCopy, resolveLogDisplay } from './display';
import { useLogFieldVisibility } from './store';
import { AttemptChainPanel } from './AttemptChainPanel';
import { StopReasonPanel } from './StopReasonPanel';
import { CopyIconButton } from '@/components/common/CopyButton';
import { toast } from 'sonner';
import { MemberStatus } from '@/components/modules/group/MemberStatus';
import {
    MorphingDialog,
    MorphingDialogTrigger,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogClose,
    MorphingDialogTitle,
    MorphingDialogDescription,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';

// formatTime 将后端 RFC3339 时间转换为本地时分秒。
function formatTime(value: string) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime()) || date.getUTCFullYear() === 1) return '--';
    return date.toLocaleTimeString(undefined, {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
    });
}

// formatMilliseconds 将毫秒转换为紧凑耗时文本。
function formatMilliseconds(value: number) {
    const milliseconds = Math.max(0, value);
    if (milliseconds < 1000) return `${Math.round(milliseconds)}ms`;
    return `${(milliseconds / 1000).toFixed(2)}s`;
}

// formatRoundStartedAt 将服务端轮次开始时间格式化为本地时分秒.毫秒, 各部分固定补零。
function formatRoundStartedAt(value: string) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime()) || date.getUTCFullYear() === 1) return '--:--:--.---';
    return `${date.toLocaleTimeString(undefined, {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
    })}.${String(date.getMilliseconds()).padStart(3, '0')}`;
}

// PROTOCOL_LABELS 是协议位值对应的界面标识, 与渠道页和分组页的授权标签同一套词。
// 键是单个协议位而非掩码组合: 日志记录的是本次请求与本轮上游各自实际使用的那一个协议。
const PROTOCOL_LABELS: Record<number, string> = {
    [Protocol.OpenAIChatCompletion]: 'Chat',
    [Protocol.OpenAIResponse]: 'Response',
    [Protocol.AnthropicMessage]: 'Message',
};

// LogMetrics 渲染时间、API Key、耗时、费用和 Token 指标; card 变体用于卡片栅格, footer 变体用于弹窗底部。
// 首字时间/TPS/缓存命中率三项按字段可见性开关渲染, 公式见 @/lib/log-metrics（语义借鉴 fork 卡片, 实现重写）。
function LogMetrics({ log, now, brandColor, variant }: { log: RelayLogOverview; now: number; brandColor: string; variant: 'card' | 'footer' }) {
    const visibility = useLogFieldVisibility();
    const t = useTranslations('log.card');
    // 字段解析（含缺字段回退链）统一走 display.ts: 实时快照与持久化历史行都能渲染, 中途缺字段也不影响展示。
    const display = resolveLogDisplay(log, now);
    const cachedTokens = display.cachedTokens;
    const duration = formatMilliseconds(display.durationMs);
    const firstByteMs = display.firstByteMs;
    // TPS 按输出 token 与总耗时计算; 缓存命中率按缓存读取占输入总量。
    const tps = formatTPS(display.completionTokens, display.elapsedMs);
    const hitRate = formatCacheHitRate(cachedTokens, display.promptTokens);
    const metrics = [
        { key: 'time', Icon: Clock, iconClassName: 'size-3.5 shrink-0', iconStyle: { color: brandColor } as CSSProperties, value: formatTime(log.started_at), valueClassName: 'tabular-nums', cellClassName: 'col-span-4 whitespace-nowrap md:col-span-1', visible: visibility.time },
        { key: 'apiKey', Icon: KeyRound, iconClassName: 'size-3.5 shrink-0 text-orange-500', value: display.apiKeyName || '-', valueClassName: 'truncate', cellClassName: 'col-span-4 md:col-span-1', visible: visibility.apiKey },
        { key: 'duration', Icon: Cpu, iconClassName: 'size-3.5 shrink-0 text-blue-500', value: duration, cellClassName: 'col-span-4 md:col-span-1', visible: visibility.duration },
        { key: 'firstByte', Icon: Zap, iconClassName: 'size-3.5 shrink-0 text-amber-500', value: formatFirstByteMs(firstByteMs), cellClassName: 'col-span-4 md:col-span-1', visible: visibility.firstByte },
        // 上游轮次 > 1 说明中途换过成员(重试/换人): 排障时先看它, 才知道这次慢是上游本身慢还是换人换出来的。
        { key: 'attempts', Icon: Repeat2, iconClassName: 'size-3.5 shrink-0 text-fuchsia-500', value: String(display.attempts ?? 0), cellClassName: 'col-span-4 md:col-span-1', visible: visibility.attempts },
        // 判定理由: 回答"这次为什么走了这个成员"（亲和保持/冷却探测/成员顺序/综合排序/人工指定）。
        // 文本较长, 单元格里截断显示, 完整值见响应头 X-Octopus-Route（两者逐字一致）。
        { key: 'decision', Icon: Route, iconClassName: 'size-3.5 shrink-0 text-sky-500', value: display.decision || '-', valueClassName: 'truncate font-mono text-[11px]', cellClassName: 'col-span-4 md:col-span-2', visible: visibility.decision },
        { key: 'cost', Icon: DollarSign, iconClassName: 'size-3.5 shrink-0 text-emerald-500', value: formatCNYCost(display.cost), valueClassName: 'font-medium text-emerald-600 dark:text-emerald-400', cellClassName: 'col-span-4 md:col-span-1', visible: visibility.cost },
        { key: 'tps', Icon: Gauge, iconClassName: 'size-3.5 shrink-0 text-lime-500', value: tps, cellClassName: 'col-span-4 md:col-span-1', visible: visibility.tps },
        { key: 'cacheHitRate', Icon: Percent, iconClassName: 'size-3.5 shrink-0 text-teal-500', value: hitRate, cellClassName: 'col-span-4 md:col-span-1', visible: visibility.cacheHitRate },
        { key: 'prompt', Icon: ArrowDownToLine, iconClassName: 'size-3.5 shrink-0 text-green-500', value: Math.max(0, display.promptTokens - cachedTokens).toLocaleString(), cellClassName: 'col-span-3 md:col-span-1', visible: visibility.prompt },
        { key: 'cached', Icon: Database, iconClassName: 'size-3.5 shrink-0 text-cyan-500', value: cachedTokens.toLocaleString(), cellClassName: 'col-span-3 md:col-span-1', visible: visibility.cached },
        { key: 'completion', Icon: ArrowUpFromLine, iconClassName: 'size-3.5 shrink-0 text-purple-500', value: display.completionTokens.toLocaleString(), cellClassName: 'col-span-3 md:col-span-1', visible: visibility.completion },
        { key: 'cacheWrite', Icon: Database, iconClassName: 'size-3.5 shrink-0 text-orange-500', value: display.cacheWriteTokens.toLocaleString(), cellClassName: 'col-span-3 md:col-span-1', visible: false },
        // 思考强度与思考 token（T-insight-001）：回答"这条请求为什么这么慢、这么贵"。
        // 两者都只在**确有值**时出现 —— 没指定强度、上游不回报思考 token 都是常态，
        // 硬占一格显示占位只会让卡片变长而没有任何信息。
        // 可见性判定用 `!== false`：老浏览器里持久化的偏好没有这两个键（undefined），
        // 用真值判断会让新字段对老用户静默隐藏，看起来像功能没生效。
        { key: 'reasoningEffort', Icon: Brain, iconClassName: 'size-3.5 shrink-0 text-violet-500', value: display.reasoningEffort, title: t('reasoningEffortHint'), cellClassName: 'col-span-4 md:col-span-1', visible: visibility.reasoningEffort !== false && display.reasoningEffort !== '' },
        { key: 'reasoningTokens', Icon: BrainCircuit, iconClassName: 'size-3.5 shrink-0 text-violet-500', value: display.reasoningTokens.toLocaleString(), title: t('reasoningTokensHint'), cellClassName: 'col-span-3 md:col-span-1', visible: visibility.reasoningTokens !== false && display.reasoningTokens > 0 },
        // 思考字数（T-insight-005）：上游普遍不报思考 token（生产实测一条都没有），
        // 这一格是"思考有多长"唯一总有值的度量 —— 从响应正文的思考文本量出来。
        // 与上一格**并存而不互斥**：token 是上游说的，字数是文本实际长度，两个量各看各的。
        { key: 'reasoningChars', Icon: Type, iconClassName: 'size-3.5 shrink-0 text-violet-500', value: display.reasoningChars.toLocaleString(), title: t('reasoningCharsHint'), cellClassName: 'col-span-3 md:col-span-1', visible: visibility.reasoningChars !== false && display.reasoningChars > 0 },
    ];

    return metrics.filter((metric) => metric.visible).map((metric) => (
        <div
            key={metric.key}
            title={metric.title ?? (metric.key === 'apiKey' ? display.apiKeyName : undefined)}
            className={cn('flex min-w-0 items-center gap-1.5', variant === 'card' && metric.cellClassName)}
        >
            <metric.Icon className={metric.iconClassName} style={metric.iconStyle} />
            <span className={metric.valueClassName}>{metric.value}</span>
        </div>
    ));
}

// ObservedRound 保存弹窗打开期间观察到的一轮上游请求状态。
interface ObservedRound {
    round: number; // 当前请求内递增的轮次序号。
    channel: string; // 本轮实际请求的渠道名称。
    error: string; // 本轮最近一次上游错误。
    sending: boolean; // 本轮是否仍在等待上游响应。
    startedAt: string; // 服务端记录的本轮开始时间。
}

// JsonContent 渲染请求或响应正文, 能解析为 JSON 时使用折叠视图, 否则按纯文本展示。
function JsonContent({ content, fallbackText }: { content: string | object | undefined; fallbackText: string }) {
    const { resolvedTheme } = useTheme();

    const parsed = useMemo(() => {
        if (content === undefined || content === '') return null;
        if (typeof content !== 'string') return { isJson: true, data: content };
        try {
            return { isJson: true, data: JSON.parse(content) as object };
        } catch {
            return { isJson: false, data: content };
        }
    }, [content]);

    if (!parsed) {
        return (
            <pre className="p-4 text-xs text-muted-foreground whitespace-pre-wrap wrap-break-word leading-relaxed">
                {fallbackText}
            </pre>
        );
    }

    if (!parsed.isJson) {
        return (
            <pre className="p-4 text-xs text-muted-foreground whitespace-pre-wrap wrap-break-word font-mono leading-relaxed animate-in fade-in duration-200">
                {parsed.data as string}
            </pre>
        );
    }

    return (
        <div className="p-4 animate-in fade-in duration-200">
            <JsonView
                value={parsed.data as object}
                style={{
                    ...(resolvedTheme === 'dark' ? githubDarkTheme : githubLightTheme),
                    fontSize: '12px',
                    fontFamily: 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace',
                    backgroundColor: 'transparent',
                }}
                displayDataTypes={false}
                displayObjectSize={false}
                collapsed={false}
            />
        </div>
    );
}

// LogDetail 渲染日志详情弹窗内容, 仅在弹窗打开期间挂载, 由此避免列表中的卡片持有详情查询和状态。
function LogDetail({ log, now }: { log: RelayLogOverview; now: number }) {
    const t = useTranslations('log.card');
    const statusT = useTranslations('log.status');
    const [leftTab, setLeftTab] = useState<'request' | 'group'>('group');
    const [rounds, setRounds] = useState<ObservedRound[]>([]);
    const [observedRoundKey, setObservedRoundKey] = useState(''); // observedRoundKey 是已记入 rounds 的最近一次日志快照, 用于跳过重复渲染。
    const [detailReady, setDetailReady] = useState(false); // 展开动画结束后才允许加载详情数据。
    const [switchingItemId, setSwitchingItemId] = useState<number | null>(null);
    const requestBody = useLogRequestBody(log.id, log.started_at, detailReady && leftTab === 'request');
    const responseBody = useLogResponseBody(log.id, log.started_at, detailReady && log.status === 'success');
    const { data: activeGroup } = useGroup(log.group_id, detailReady, detailReady);
    const updateActiveItem = useUpdateGroup();
    const stopRound = useStopRound();
    const logDisplay = resolveLogDisplay(log);
    const actualModel = logDisplay.actualModel;
    // 上游回报的模型与请求的不一致 —— 这是「上游可能偷换模型」的唯一可见证据（T-verify-001）。
    // 必须在卡片上显式标出来：不标的话，用户看到 actualModel 只会以为那就是他要的模型。
    const modelMismatch = logDisplay.modelMismatch;
    const reportedModel = logDisplay.reportedModel;
    const { Icon, className: iconClassName, color: brandColor } = getModelIcon(actualModel);
    const errorText = log.error ?? '';
    const requestFailed = log.status === 'failed' || log.status === 'canceled';
    const responseCommitted = log.status === 'committed';
    const showRounds = log.status === 'running' || (requestFailed && rounds.length > 0);
    const isWaitingForSelection = log.status === 'running' && !log.sending && activeGroup?.mode === 'manual' && activeGroup.runtime.current_item_id === 0; // isWaitingForSelection 表示手动模式请求正等待选择渠道。

    // 让弹窗先完成展开动画, 避免详情请求及其状态更新占用动画起步帧。
    useEffect(() => {
        const timer = window.setTimeout(() => setDetailReady(true), 600);
        return () => window.clearTimeout(timer);
    }, []);

    // 按轮次记录本次打开期间观察到的上游请求状态, 最新一轮排在最前。
    // 轮次来自逐次推送的日志, 需在渲染期比对已记录的快照累积, 不能仅由当前 log 推导。
    const roundKey = log.round === 0 ? '' : `${log.round}:${log.target_channel}:${log.sending}:${errorText}`;
    if (roundKey !== '' && roundKey !== observedRoundKey) {
        setObservedRoundKey(roundKey);
        setRounds((current) => {
            if (!log.sending && current.every((item) => item.round !== log.round)) return current;
            const previous = current.find((item) => item.round === log.round);
            const startedAt = previous?.startedAt ?? log.round_started_at;
            return [
                {
                    round: log.round,
                    channel: log.target_channel,
                    error: errorText,
                    sending: log.sending,
                    startedAt,
                },
                ...current.filter((item) => item.round !== log.round),
            ];
        });
    }

    return (
        <MorphingDialogContent className="relative w-[calc(100vw-2rem)] md:w-[80vw] bg-card text-card-foreground px-6 py-4 rounded-3xl h-[calc(100vh-2rem)] flex flex-col overflow-hidden">
            <MorphingDialogClose className="top-4 right-5 text-muted-foreground hover:text-foreground transition-colors" />
            <MorphingDialogTitle className="flex items-center gap-2 mb-3 text-sm">
                <Icon aria-hidden="true" className={iconClassName} width={28} height={28} />
                <span className="text-xs text-muted-foreground/70">{PROTOCOL_LABELS[log.protocol] ?? '-'}</span>
                <span className="font-semibold text-card-foreground">{log.model || t('unknownModel')}</span>
                {log.status === 'running' || responseCommitted
                    ? <Loader2 className={cn('size-3.5 animate-spin', log.status === 'committed' ? 'text-green-500' : log.round > 1 ? 'text-red-500' : 'text-muted-foreground/50')} />
                    : <ArrowRight className="size-3.5 text-muted-foreground/50" />}
                <span className="text-xs text-muted-foreground/70">{PROTOCOL_LABELS[log.target_protocol] ?? '-'}</span>
                {logDisplay.protocolConverted && (
                    // 与卡片上同一标记、同一语义（T-trace-004）。弹窗是排查时看得最仔细的地方，
                    // 这里不能只有卡片有标记：两侧协议不同时，用户正是在弹窗里追问"到底转换了没有"。
                    <span className="shrink-0 inline-flex" title={t('protocolConvertedHint')}>
                        <ArrowLeftRight aria-hidden="true" className="size-3 text-amber-500" />
                    </span>
                )}
                <Badge
                    variant="secondary"
                    className="text-xs px-1.5 py-0"
                    style={{ backgroundColor: `${brandColor}15`, color: brandColor }}
                >
                    {log.target_channel || '-'}
                </Badge>
                <span className="text-muted-foreground">{actualModel}</span>
                {modelMismatch && (
                    // 上游说它用的是另一个模型。用告警色标出来并给出上游原话，
                    // 让用户自己判断是别名/路由层改名（可接受）还是真的偷换（要处理）。
                    <Badge
                        variant="secondary"
                        className="text-xs px-1.5 py-0 border border-destructive/40 text-destructive"
                        title={t('log.modelMismatchTip', { reported: reportedModel })}
                    >
                        {t('log.modelMismatch')}
                    </Badge>
                )}
            </MorphingDialogTitle>

            <MorphingDialogDescription className="flex-1 min-h-0">
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4 h-full min-h-0">
                    <div className="flex flex-col rounded-2xl border border-border bg-muted/30 overflow-hidden min-h-0">
                        <div className="flex h-10 shrink-0 items-center gap-2 border-b border-border bg-muted/50 pl-1 pr-3 md:pr-4">
                            <Tabs value={leftTab} onValueChange={(value) => setLeftTab(value as 'request' | 'group')}>
                                <TabsList variant="text" className="p-0">
                                    <TabsTrigger value="group" className="pr-0">
                                        {t('group')}
                                    </TabsTrigger>
                                    <span aria-hidden="true" className="mx-1 inline-flex h-full -translate-y-px items-center text-sm font-medium leading-none text-muted-foreground/50">/</span>
                                    <TabsTrigger value="request" className="pl-0">
                                        {t('requestContent')}
                                    </TabsTrigger>
                                </TabsList>
                            </Tabs>
                            {leftTab === 'request' && (
                                <Badge variant="secondary" className="ml-auto text-xs">
                                    {(log.usage.prompt_tokens - (log.usage.prompt_tokens_details?.cached_tokens ?? 0)).toLocaleString()} {t('tokens')}
                                </Badge>
                            )}
                        </div>
                        <div className="flex-1 overflow-auto min-h-0">
                            {!detailReady ? (
                                <div className="flex h-full items-center justify-center">
                                    <Loader2 className="size-5 animate-spin text-muted-foreground" />
                                </div>
                            ) : leftTab === 'request' ? (
                                requestBody.isLoading ? (
                                    <div className="flex h-full items-center justify-center">
                                        <Loader2 className="size-5 animate-spin text-muted-foreground" />
                                    </div>
                                ) : requestBody.error ? (
                                    <div className="flex h-full flex-col items-center justify-center gap-2 px-4 text-xs text-destructive">
                                        <AlertCircle className="size-5" />
                                        <span>{t('detailUnavailable')}</span>
                                    </div>
                                ) : (
                                    <JsonContent content={requestBody.data} fallbackText={t('noRequestContent')} />
                                )
                            ) : !activeGroup ? (
                                <div className="flex h-full items-center justify-center px-4 text-xs text-muted-foreground">
                                    {t('groupUnavailable')}
                                </div>
                            ) : !activeGroup.items.length ? (
                                <div className="flex h-full items-center justify-center px-4 text-xs text-muted-foreground">
                                    {t('noGroupItems')}
                                </div>
                            ) : (
                                <div className="divide-y divide-border">
                                    {activeGroup.items.map((item) => {
                                        const { Icon: ItemIcon, className: itemIconClassName } = getModelIcon(item.model_name);
                                        const itemCurrent = switchingItemId !== null
                                            ? item.id === switchingItemId
                                            : activeGroup.runtime.current_item_id === item.id;
                                        return (
                                            <button
                                                key={item.id}
                                                type="button"
                                                aria-pressed={itemCurrent}
                                                disabled={activeGroup.mode !== 'manual' || switchingItemId !== null || stopRound.isPending}
                                                onClick={async () => {
                                                    if (activeGroup.mode !== 'manual') return;
                                                    setSwitchingItemId(item.id);
                                                    const isCurrent = activeGroup.runtime.current_item_id === item.id;
                                                    try {
                                                        await updateActiveItem.mutateAsync({ id: activeGroup.id, active_item_id: isCurrent ? 0 : item.id });
                                                        if (log.sending) {
                                                            await stopRound.mutateAsync({ requestId: log.id, round: log.round });
                                                        }
                                                        toast.success(isCurrent ? t('channelCleared') : t('channelChanged'));
                                                    } catch (cause) {
                                                        toast.error(t('channelChangeFailed'), { description: cause instanceof Error ? cause.message : undefined });
                                                    } finally {
                                                        setSwitchingItemId(null);
                                                    }
                                                }}
                                                className="flex w-full items-center gap-2.5 rounded-lg px-3 py-2.5 text-left text-xs transition-colors hover:bg-muted/50 disabled:cursor-default disabled:hover:bg-transparent"
                                            >
                                                <ItemIcon aria-hidden="true" className={itemIconClassName} width={20} height={20} />
                                                <span className="min-w-0 flex-1">
                                                    <span className="block truncate font-semibold text-foreground">
                                                        {item.model_name}
                                                    </span>
                                                    <span className="block truncate text-[11px] text-muted-foreground">
                                                        {item.key_name ? `${item.channel_name} · ${item.key_name}` : item.channel_name}
                                                    </span>
                                                </span>
                                                <MemberStatus group={activeGroup} itemId={item.id} now={now} active={itemCurrent} />
                                            </button>
                                        );
                                    })}
                                </div>
                            )}
                        </div>
                    </div>

                    {/* 尝试明细: 补齐"中间换过谁、各自为何失败"——卡片上的 attempts 只是计数、
                        最终渠道只说明结果, 两者都答不出某个成员在反复拖后腿。 */}
                    <AttemptChainPanel chain={logDisplay.attemptChain} truncated={logDisplay.attemptChainTruncated} />

                    {/* 终止原因（T-trace-003）: 与上面的尝试明细是两件事 ——
                        明细说"每一轮各自怎么了", 这里说"整条请求被哪条规则终止的、
                        规则从哪来"。同为失败终态,"预算用尽"要去查上游、
                        "全体成员判定请求非法"要去改请求, 只看故障归因分不出来。
                        正常成功结束不渲染（绝大多数日志都是它）。 */}
                    <StopReasonPanel stopReason={logDisplay.stopReason} />

                    <div className="flex flex-col rounded-2xl border border-border bg-muted/30 overflow-hidden min-h-0">
                        <div className="flex h-10 shrink-0 items-center gap-2 border-b border-border bg-muted/50 px-3 md:px-4">
                            <span className="text-sm font-medium text-card-foreground">
                                {isWaitingForSelection ? t('waitingChannelSelection') : showRounds ? t('retryDetails') : requestFailed ? t('errorInfo') : t('responseContent')}
                            </span>
                            {log.status === 'running' && log.sending && activeGroup?.mode === 'manual' ? (
                                <button
                                    type="button"
                                    disabled={stopRound.isPending}
                                    onClick={async () => {
                                        try {
                                            await stopRound.mutateAsync({ requestId: log.id, round: log.round });
                                        } catch (cause) {
                                            toast.error(t('stopFailed'), { description: cause instanceof Error ? cause.message : undefined });
                                        }
                                    }}
                                    className="ml-auto flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-destructive transition-colors hover:bg-destructive/10 disabled:opacity-50"
                                >
                                    {stopRound.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Square className="size-3.5" />}
                                    {t('stopRound')}
                                </button>
                            ) : !requestFailed && (
                                <Badge variant="secondary" className="ml-auto text-xs">
                                    {responseCommitted
                                        ? statusT('committed')
                                        : `${log.usage.completion_tokens.toLocaleString()} ${t('tokens')}`}
                                </Badge>
                            )}
                        </div>
                        <div className="min-h-0 flex-1 overflow-auto">
                            {!detailReady ? (
                                <div className="flex h-full items-center justify-center">
                                    <Loader2 className="size-5 animate-spin text-muted-foreground" />
                                </div>
                            ) : isWaitingForSelection ? (
                                <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
                                    <Loader2 className="size-4 animate-spin" />
                                    {t('waitingChannelSelection')}
                                </div>
                            ) : showRounds ? (
                                rounds.length ? (
                                    <div className="divide-y divide-border">
                                        {rounds.map((round) => (
                                            <div key={round.round} className="flex flex-col gap-1.5 px-3 py-2.5 text-xs">
                                                <div className="flex items-center gap-2">
                                                    <span className="shrink-0 tabular-nums text-muted-foreground">{formatRoundStartedAt(round.startedAt)}</span>
                                                    <span className="shrink-0 text-muted-foreground">{t('retryIndex', { index: round.round })}</span>
                                                    <span className="shrink-0 font-semibold text-foreground">{round.channel || '-'}</span>
                                                    {round.sending ? (
                                                        <Loader2 className="ml-auto size-3.5 animate-spin text-muted-foreground" />
                                                    ) : round.error ? (
                                                        <CopyIconButton
                                                            text={formatJsonForCopy(round.error)}
                                                            className="ml-auto p-1 rounded-md text-destructive/60 hover:text-destructive hover:bg-destructive/10 transition-colors"
                                                            copyIconClassName="size-3.5"
                                                            checkIconClassName="size-3.5"
                                                        />
                                                    ) : null}
                                                </div>
                                                {round.error && (
                                                    <div className="text-[11px] leading-relaxed text-destructive/90 whitespace-pre-wrap wrap-break-word">
                                                        {round.error}
                                                    </div>
                                                )}
                                            </div>
                                        ))}
                                    </div>
                                ) : (
                                    <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
                                        <Loader2 className="size-4 animate-spin" />
                                        {t('waitingResponse')}
                                    </div>
                                )
                            ) : responseCommitted ? (
                                <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
                                    <Loader2 className="size-4 animate-spin" />
                                    {t('responseStreaming')}
                                </div>
                            ) : requestFailed ? (
                                <JsonContent content={errorText} fallbackText={t('noResponseContent')} />
                            ) : responseBody.isLoading ? (
                                <div className="flex h-full items-center justify-center">
                                    <Loader2 className="size-5 animate-spin text-muted-foreground" />
                                </div>
                            ) : responseBody.error ? (
                                <div className="flex h-full flex-col items-center justify-center gap-2 px-4 text-xs text-destructive">
                                    <AlertCircle className="size-5" />
                                    <span>{t('detailUnavailable')}</span>
                                </div>
                            ) : (
                                <JsonContent content={responseBody.data} fallbackText={t('noResponseContent')} />
                            )}
                        </div>
                    </div>
                </div>
            </MorphingDialogDescription>

            <div className="flex w-full shrink-0 flex-wrap items-center gap-3 pt-4 mt-auto text-xs text-muted-foreground md:gap-4">
                <LogMetrics log={log} now={now} brandColor={brandColor} variant="footer" />
            </div>
        </MorphingDialogContent>
    );
}

// LogCardBody 渲染日志概览卡片, 并在弹窗打开时挂载详情面板。
function LogCardBody({ log }: { log: RelayLogOverview }) {
    const t = useTranslations('log.card');
    const { isOpen } = useMorphingDialog();
    const [now, setNow] = useState(() => Date.now());
    const display = resolveLogDisplay(log);
    const actualModel = display.actualModel;
    // protocolConverted 为真时，卡片上的「入站协议 → 上游协议」不是一个装饰性的箭头，
    // 而是真的发生过一次跨协议转换（T-trace-004）。两者相同时不显示任何额外标记。
    const protocolConverted = display.protocolConverted;
    const { Icon, className: iconClassName, color: brandColor } = getModelIcon(actualModel);
    const requestRunning = log.status === 'running' || log.status === 'committed';
    const requestFailed = log.status === 'failed' || log.status === 'canceled';
    const errorText = log.error ?? '';

    // 仅在请求进行中或弹窗打开时走秒级刷新, 避免已完成日志持续触发重渲染。
    useEffect(() => {
        if (!requestRunning && !isOpen) return;
        const timer = window.setInterval(() => setNow(Date.now()), 1000);
        return () => window.clearInterval(timer);
    }, [isOpen, requestRunning]);

    return (
        <>
            <MorphingDialogTrigger
                className={cn(
                    "rounded-3xl border bg-card w-full text-left",
                    requestFailed ? "border-destructive/40" : "border-border",
                )}
            >
                <div className={cn("p-4 grid grid-cols-[auto_1fr] gap-4", requestFailed ? "items-start" : "items-center")}>
                    <Icon aria-hidden="true" className={iconClassName} width={40} height={40} />
                    <div className="min-w-0 flex flex-col gap-3">
                        <div className="flex items-center gap-2 min-w-0 text-sm">
                            <span className="shrink-0 text-xs text-muted-foreground/70">{PROTOCOL_LABELS[log.protocol] ?? '-'}</span>
                            <span className="font-semibold text-card-foreground truncate">
                                {log.model || t('unknownModel')}
                            </span>
                            {requestRunning
                                ? <Loader2 className={cn('size-3.5 shrink-0 animate-spin', log.status === 'committed' ? 'text-green-500' : log.round > 1 ? 'text-red-500' : 'text-muted-foreground/50')} />
                                : <ArrowRight className="size-3.5 shrink-0 text-muted-foreground/50" />}
                            <span className="shrink-0 text-xs text-muted-foreground/70">{PROTOCOL_LABELS[log.target_protocol] ?? '-'}</span>
                            {protocolConverted && (
                                // 跨协议转换的可见标记（T-trace-004）：两侧标签不同本身只是"看着不一样"，
                                // 这个图标把语义说实——中间确实换过一次协议。
                                // 用 span 承载 title（SVG 元素上的 title 属性不产生浏览器提示，得用宿主元素）。
                                <span className="shrink-0 inline-flex" title={t('protocolConvertedHint')}>
                                    <ArrowLeftRight aria-hidden="true" className="size-3 text-amber-500" />
                                </span>
                            )}
                            <Badge
                                variant="secondary"
                                className="shrink-0 text-xs px-1.5 py-0"
                                style={{ backgroundColor: `${brandColor}15`, color: brandColor }}
                            >
                                {log.target_channel || '-'}
                            </Badge>
                            <span className="text-muted-foreground truncate">
                                {actualModel}
                            </span>
                        </div>
                        <div className="grid grid-cols-12 gap-x-4 gap-y-2 text-xs tabular-nums text-muted-foreground md:grid-cols-8">
                            <LogMetrics log={log} now={now} brandColor={brandColor} variant="card" />
                        </div>
                        {requestFailed && errorText && (
                            <div className="p-2.5 rounded-xl bg-destructive/10 border border-destructive/20 overflow-hidden">
                                <p className="text-xs text-destructive line-clamp-2 whitespace-pre-line">{errorText}</p>
                            </div>
                        )}
                    </div>
                </div>
            </MorphingDialogTrigger>

            <MorphingDialogContainer>
                <LogDetail log={log} now={now} />
            </MorphingDialogContainer>
        </>
    );
}

// LogCard 展示一条日志概览, 并在弹窗打开时加载详情。
export const LogCard = memo(function LogCard({ log }: { log: RelayLogOverview }) {
    return (
        <MorphingDialog>
            <LogCardBody log={log} />
        </MorphingDialog>
    );
});
