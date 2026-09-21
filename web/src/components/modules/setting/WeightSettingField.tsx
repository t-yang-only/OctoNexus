import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useSettingList, useSetSetting, type Setting } from '@/api/setting';
import { toast } from 'sonner';

// WeightSettingField 是选路与转发类设置的一组控件（数字或下拉）。
// 自包含: 自己从设置列表取当前值、自己保存并 toast, 免得为十几个键再抄一遍父组件的 state/ref/同步样板。
export function WeightSettingField({ settingKey, label, kind, options, max, hint }: {
    settingKey: string;
    label: string;
    kind: 'number' | 'select';
    options?: { value: string; label: string }[];
    // max 是数字控件的上限: 加权维度是 0..100, 而分压设置里还有 token 数/RPM 这类更大的量纲
    // （T-allocate-001）, 故不能把 100 写死在控件上。
    max?: string;
    hint?: string;
}) {
    const t = useTranslations('setting');
    const settingsQuery = useSettingList();
    const setSetting = useSetSetting();
    const [value, setValue] = useState<string>('');
    const initial = useRef<string>('');
    const effective = (settingsQuery.data ?? []) as Setting[];

    useEffect(() => {
        const found = effective.find((s) => s.key === settingKey);
        if (found) {
            initial.current = found.value;
            queueMicrotask(() => setValue(found.value));
        }
    }, [effective, settingKey]);

    const save = (next: string) => {
        if (next === initial.current) return;
        setSetting.mutate({ key: settingKey, value: next }, {
            onSuccess: () => {
                initial.current = next;
                toast.success(t('saved'));
            },
            onError: (error) => toast.error(error instanceof Error ? error.message : String(error)),
        });
    };

    if (kind === 'select') {
        return (
            <label className="grid gap-1 text-xs text-muted-foreground">
                {label}
                <Select value={value || (options?.[0]?.value ?? '')} onValueChange={(next) => { setValue(next); save(next); }}>
                    <SelectTrigger className="rounded-xl"><SelectValue /></SelectTrigger>
                    <SelectContent>
                        {(options ?? []).map((option) => (
                            <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            </label>
        );
    }

    return (
        <label className="grid gap-1 text-xs text-muted-foreground">
            {label}
            {hint && <span className="text-[11px] text-muted-foreground/80">{hint}</span>}
            <Input
                type="number"
                min="0"
                max={max ?? '100'}
                value={value}
                onChange={(e) => setValue(e.target.value)}
                onBlur={() => save(value)}
                className="rounded-xl"
            />
        </label>
    );
}
