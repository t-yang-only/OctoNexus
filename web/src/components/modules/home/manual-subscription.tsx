import { CalendarClock, Pencil, Plus, Trash2, X } from 'lucide-react';
import { useState } from 'react';
import { useTranslations } from 'use-intl';
import {
    useCreateManualSubscription,
    useDeleteManualSubscription,
    useUpdateManualSubscription,
    type ManualSubscriptionInput,
    type ManualSubscriptionRow,
} from '@/api/subscription';
import type { ChannelBalanceRow } from '@/api/balance';

// 手动订阅（R-acct-004 / T-acct-005）：上游没有余额接口时人录一条，按同一口径并入总余额。
//
// 面板只做两件事：如实展示「这条算没算进总额、为什么」，以及增删改。
// 金额一律由后端折算（行级口径可覆盖全局），前端不重复实现换算 —— 两处算法必然会对不上。

interface FormState {
    id: number;
    channel_id: number;
    name: string;
    note: string;
    balance_points: string;
    points_per_unit: string;
    expire_date: string;
    enabled: boolean;
}

function emptyForm(): FormState {
    return {
        id: 0,
        channel_id: 0,
        name: '',
        note: '',
        balance_points: '0',
        points_per_unit: '0',
        expire_date: '',
        enabled: true,
    };
}

function toForm(row: ManualSubscriptionRow): FormState {
    return {
        id: row.id,
        channel_id: row.channel_id,
        name: row.name,
        note: row.note ?? '',
        balance_points: String(row.balance_points ?? 0),
        points_per_unit: String(row.points_per_unit ?? 0),
        expire_date: row.expire_at ? new Date(row.expire_at * 1000).toISOString().slice(0, 10) : '',
        enabled: row.enabled,
    };
}

function toInput(form: FormState): ManualSubscriptionInput {
    const points = Number(form.balance_points);
    const unit = Number(form.points_per_unit);
    return {
        id: form.id || undefined,
        channel_id: Number(form.channel_id) || 0,
        name: form.name.trim(),
        note: form.note.trim(),
        balance_points: Number.isFinite(points) && points > 0 ? points : 0,
        points_per_unit: Number.isFinite(unit) && unit > 0 ? unit : 0,
        expire_at: form.expire_date ? Math.floor(new Date(`${form.expire_date}T23:59:59`).getTime() / 1000) : 0,
        enabled: form.enabled,
    };
}

export function ManualSubscriptions({
    rows,
    channels,
    currency,
    globalPointsPerUnit,
    manualTotal,
    expired,
}: {
    rows: ManualSubscriptionRow[];
    channels: ChannelBalanceRow[];
    currency: string;
    globalPointsPerUnit: number;
    manualTotal: number;
    expired: number;
}) {
    const t = useTranslations('home.balance.manual');
    const [form, setForm] = useState<FormState | null>(null);
    const [error, setError] = useState('');
    const create = useCreateManualSubscription();
    const update = useUpdateManualSubscription();
    const remove = useDeleteManualSubscription();

    const submit = async () => {
        if (!form) return;
        if (!form.name.trim()) {
            setError(t('nameRequired'));
            return;
        }
        setError('');
        try {
            if (form.id) await update.mutateAsync(toInput(form));
            else await create.mutateAsync(toInput(form));
            setForm(null);
        } catch (err) {
            setError(err instanceof Error ? err.message : String(err));
        }
    };

    const drop = async (id: number) => {
        setError('');
        try {
            await remove.mutateAsync(id);
        } catch (err) {
            setError(err instanceof Error ? err.message : String(err));
        }
    };

    const field = 'rounded-lg border border-border bg-transparent px-2 py-1 text-xs';

    return (
        <div className="border-border border-t pt-4 space-y-3">
            <header className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex flex-col">
                    <span className="text-sm font-medium">{t('title')}</span>
                    <span className="text-xs text-muted-foreground">
                        {t('subtitle', { points: globalPointsPerUnit.toLocaleString() })}
                    </span>
                </div>
                <div className="flex items-center gap-3">
                    <span className="text-xs text-muted-foreground">
                        {t('total', { amount: manualTotal.toFixed(2), currency })}
                    </span>
                    {expired > 0 && (
                        <span className="text-xs text-orange-500">{t('expiredCount', { count: expired })}</span>
                    )}
                    <button
                        type="button"
                        className="flex items-center gap-1 rounded-lg border border-border px-2 py-1 text-xs hover:bg-accent"
                        onClick={() => { setForm(emptyForm()); setError(''); }}
                    >
                        <Plus className="size-3.5" />
                        {t('add')}
                    </button>
                </div>
            </header>

            {error && <p className="text-xs text-red-500">{error}</p>}

            {form && (
                <div className="flex flex-wrap items-end gap-2 rounded-xl border border-border p-3">
                    <label className="flex flex-col gap-1">
                        <span className="text-xs text-muted-foreground">{t('name')}</span>
                        <input className={`${field} w-40`} value={form.name} placeholder={t('namePlaceholder')}
                            onChange={(e) => setForm({ ...form, name: e.target.value })} />
                    </label>
                    <label className="flex flex-col gap-1">
                        <span className="text-xs text-muted-foreground">{t('channel')}</span>
                        <select className={`${field} w-40`} value={form.channel_id}
                            onChange={(e) => setForm({ ...form, channel_id: Number(e.target.value) })}>
                            <option value={0}>{t('channelNone')}</option>
                            {channels.map((item) => (
                                <option key={item.channel_id} value={item.channel_id}>{item.channel_name}</option>
                            ))}
                        </select>
                    </label>
                    <label className="flex flex-col gap-1">
                        <span className="text-xs text-muted-foreground">{t('points')}</span>
                        <input className={`${field} w-28`} value={form.balance_points} inputMode="decimal"
                            onChange={(e) => setForm({ ...form, balance_points: e.target.value })} />
                    </label>
                    <label className="flex flex-col gap-1">
                        <span className="text-xs text-muted-foreground">{t('pointsPerUnit')}</span>
                        <input className={`${field} w-32`} value={form.points_per_unit} inputMode="decimal"
                            placeholder={t('pointsPerUnitPlaceholder', { points: globalPointsPerUnit })}
                            onChange={(e) => setForm({ ...form, points_per_unit: e.target.value })} />
                    </label>
                    <label className="flex flex-col gap-1">
                        <span className="text-xs text-muted-foreground">{t('expire')}</span>
                        <input type="date" className={`${field} w-36`} value={form.expire_date}
                            onChange={(e) => setForm({ ...form, expire_date: e.target.value })} />
                    </label>
                    <label className="flex items-center gap-1 text-xs text-muted-foreground">
                        <input type="checkbox" checked={form.enabled}
                            onChange={(e) => setForm({ ...form, enabled: e.target.checked })} />
                        {t('enabled')}
                    </label>
                    <div className="flex items-center gap-2">
                        <button type="button" className="rounded-lg border border-border px-3 py-1 text-xs hover:bg-accent"
                            onClick={submit}>{t('save')}</button>
                        <button type="button" className="rounded-lg border border-border px-2 py-1 text-xs hover:bg-accent"
                            onClick={() => setForm(null)}><X className="size-3.5" /></button>
                    </div>
                    <label className="flex w-full flex-col gap-1">
                        <span className="text-xs text-muted-foreground">{t('note')}</span>
                        <input className={`${field} w-full`} value={form.note} placeholder={t('notePlaceholder')}
                            onChange={(e) => setForm({ ...form, note: e.target.value })} />
                    </label>
                </div>
            )}

            {rows.length === 0 && !form && (
                <p className="text-xs text-muted-foreground">{t('empty')}</p>
            )}

            <div className="space-y-1">
                {rows.map((row) => (
                    <div key={row.id} className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-border px-3 py-2">
                        <div className="flex flex-col">
                            <span className="text-xs font-medium">
                                {row.name}
                                {!row.enabled && <span className="ml-2 text-muted-foreground">{t('disabled')}</span>}
                                {row.expired && <span className="ml-2 text-orange-500">{t('expired')}</span>}
                            </span>
                            <span className="text-[11px] text-muted-foreground">
                                {row.channel_id ? row.channel_name || `#${row.channel_id}` : t('channelNone')}
                                {' · '}
                                {row.balance_points.toLocaleString()} {t('pointsSuffix')}
                                {row.points_per_unit !== globalPointsPerUnit
                                    ? ` @ ${row.points_per_unit.toLocaleString()}` : ''}
                                {' · '}
                                {row.expire_at
                                    ? t('daysLeft', { days: row.days_left })
                                    : t('noExpire')}
                                {row.note ? ` · ${row.note}` : ''}
                            </span>
                        </div>
                        <div className="flex items-center gap-3">
                            <span className="text-xs">
                                {row.balance.toFixed(2)} {currency}
                            </span>
                            <span className={`text-[11px] ${row.counted ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground'}`}>
                                {row.counted ? t('counted') : t('notCounted')}
                            </span>
                            <button type="button" className="rounded-lg border border-border p-1 hover:bg-accent"
                                title={t('edit')} onClick={() => { setForm(toForm(row)); setError(''); }}>
                                <Pencil className="size-3.5" />
                            </button>
                            <button type="button" className="rounded-lg border border-border p-1 hover:bg-accent"
                                title={t('delete')} onClick={() => drop(row.id)}>
                                <Trash2 className="size-3.5" />
                            </button>
                        </div>
                    </div>
                ))}
            </div>

            <p className="flex items-center gap-1 text-[11px] text-muted-foreground">
                <CalendarClock className="size-3" />
                {t('hint')}
            </p>
        </div>
    );
}
