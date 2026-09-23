import { CircleStop } from 'lucide-react';
import { useTranslations } from 'use-intl';

// StopReasonPanel 展示一次请求**为什么停下来**（T-trace-003）。
//
// ## 与 FaultKind 的分工
//
//   fault_kind   这次失败**算谁的账**（request / member / transient）
//   stop_reason  **哪条规则**终止了请求、这条规则**从哪来**
//
// 两者都缺一不可：同样是失败终态，"试到次数上限才放弃"是去查上游是否大面积故障，
// 而"全部成员都说这个请求非法"是去改请求 —— 处置动作完全相反，
// 但 fault_kind 两者都可能记成 request、HTTP 状态都是 502、
// 错误文本更是同一句话（后端五个终止出口都产出 upstream_error）。
//
// ## 展示口径
//
// 后端下发的是 "action=stop;reason=X;source=Y" 形式（与 Decision 同形）。
// 这里**解析成人类可读的一句话**，而不是把原始串直接摊给用户看 ——
// 但 reason 未知时如实显示原始值，绝不猜（后端将来新增取值时，
// 界面显示新值比显示"未知原因"更有用）。
//
// 成功请求的 reason 恒为 request_completed，没有任何信息量（绝大多数日志都是它），
// 故此值不渲染 —— 面板只对"非正常终止"出现。

// reason 取值 → i18n 键名。与后端 internal/relay/stop_reason.go 的常量一一对应。
const REASON_KEYS: Record<string, string> = {
    attempt_budget_exhausted: 'reasonBudget',
    all_members_rejected: 'reasonAllRejected',
    request_fault_failfast: 'reasonFailFast',
    member_fault_no_alternative: 'reasonMemberFault',
    no_available_member: 'reasonNoMember',
    client_canceled: 'reasonClientCancel',
};

// source 取值 → i18n 键名。source 决定"该去改什么"。
const SOURCE_KEYS: Record<string, string> = {
    config: 'sourceConfig',
    upstream: 'sourceUpstream',
    client: 'sourceClient',
    system: 'sourceSystem',
};

function parseStopReason(raw: string): { reason: string; source: string } | null {
    if (!raw) return null;
    const parts: Record<string, string> = {};
    for (const segment of raw.split(';')) {
        const idx = segment.indexOf('=');
        if (idx <= 0) continue;
        parts[segment.slice(0, idx)] = segment.slice(idx + 1);
    }
    if (!parts.reason) return null;
    return { reason: parts.reason, source: parts.source ?? '' };
}

export function StopReasonPanel({ stopReason }: { stopReason?: string }) {
    const t = useTranslations('log.stopReason');
    const parsed = parseStopReason(stopReason ?? '');
    if (!parsed) return null;

    // 正常结束不渲染：绝大多数日志都是它，显示出来只是噪音。
    if (parsed.reason === 'request_completed') return null;

    const reasonKey = REASON_KEYS[parsed.reason];
    // 未知 reason 显示原始值 —— 后端新增取值时，显示新值比显示"未知"更有用。
    const reasonText = reasonKey ? t(reasonKey) : parsed.reason;
    const sourceKey = SOURCE_KEYS[parsed.source];
    const sourceText = sourceKey ? t(sourceKey) : parsed.source;

    return (
        <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 rounded-lg border border-border bg-muted/20 px-3 py-1.5 text-[11px]">
            <CircleStop className="size-3.5 shrink-0 text-amber-500" />
            <span className="shrink-0 font-medium text-card-foreground">{t('title')}</span>
            <span className="text-foreground">{reasonText}</span>
            {sourceText && (
                <span className="text-muted-foreground/70">
                    {t('source', { source: sourceText })}
                </span>
            )}
        </div>
    );
}
