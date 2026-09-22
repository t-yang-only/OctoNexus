import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { CloudUpload, Loader2, RefreshCw } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { toast } from 'sonner';
import { useSettingList, useSetSetting, SettingKey } from '@/api/setting';
import { useRunWebDAVBackup, useWebDAVBackupList } from '@/api/webdav-backup';

// 云备份面板（T-backup-001）。
//
// 与上面那张「备份恢复」卡片的分工：那张管**本机**的导出/导入（手动下载一个文件），
// 这张管**远端**的定时上传——两者的价值不同：本机坏了，存在本机上的备份也没了。
//
// 口令刻意不在这里：它只从服务端环境变量 OCTOPUS_WEBDAV_PASSWORD 读。
// 面板上写一句说明，免得用户找不到输入框以为功能不全。
export function SettingWebDAVBackup() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();
    const runBackup = useRunWebDAVBackup();

    const [url, setUrl] = useState('');
    const [username, setUsername] = useState('');
    const [interval, setIntervalHours] = useState('24');
    const [keep, setKeep] = useState('7');
    const [enabled, setEnabled] = useState(false);
    const initial = useRef({ url: '', username: '', interval: '24', keep: '7' });

    useEffect(() => {
        if (!settings) return;
        const pick = (key: string) => settings.find((s) => s.key === key)?.value ?? '';
        const v = {
            url: pick(SettingKey.WebDAVURL),
            username: pick(SettingKey.WebDAVUsername),
            interval: pick(SettingKey.WebDAVInterval) || '24',
            keep: pick(SettingKey.WebDAVKeep) || '7',
        };
        queueMicrotask(() => {
            setUrl(v.url);
            setUsername(v.username);
            setIntervalHours(v.interval);
            setKeep(v.keep);
            setEnabled(pick(SettingKey.WebDAVEnabled) === 'true');
        });
        initial.current = v;
    }, [settings]);

    const save = (key: string, value: string, prev: string, onDone?: () => void) => {
        if (value === prev) return;
        setSetting.mutate(
            { key, value },
            {
                onSuccess: () => {
                    onDone?.();
                    toast.success(t('saved'));
                },
                onError: (e) => toast.error(e instanceof Error ? e.message : String(e)),
            }
        );
    };

    const listQuery = useWebDAVBackupList(enabled && url.trim() !== '');

    const onRun = async () => {
        try {
            const result = await runBackup.mutateAsync();
            if (result.warning) {
                // 备份成功、只是清理失败：如实说明，不要报成失败。
                toast.warning(t('webdavBackup.pruneWarning', { reason: result.warning }));
            } else {
                toast.success(t('webdavBackup.runSuccess', { file: result.file }));
            }
            await listQuery.refetch();
        } catch (e) {
            toast.error(e instanceof Error ? e.message : t('webdavBackup.runFailed'));
        }
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 space-y-5">
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <CloudUpload className="h-5 w-5 text-muted-foreground" />
                    <div>
                        <div className="text-sm font-medium">{t('webdavBackup.title')}</div>
                        <div className="text-xs text-muted-foreground">{t('webdavBackup.subtitle')}</div>
                    </div>
                </div>
                <Switch
                    checked={enabled}
                    onCheckedChange={(checked) => {
                        setEnabled(checked);
                        save(SettingKey.WebDAVEnabled, checked ? 'true' : 'false', enabled ? 'true' : 'false');
                    }}
                />
            </div>

            <div className="grid gap-4 md:grid-cols-2">
                <label className="grid gap-1 md:col-span-2">
                    <span className="text-xs text-muted-foreground">{t('webdavBackup.url')}</span>
                    <Input
                        value={url}
                        onChange={(e) => setUrl(e.target.value)}
                        onBlur={() => save(SettingKey.WebDAVURL, url, initial.current.url, () => (initial.current.url = url))}
                        placeholder="https://dav.example.com/remote.php/dav/files/me/octopus"
                        className="rounded-xl"
                    />
                </label>
                <label className="grid gap-1">
                    <span className="text-xs text-muted-foreground">{t('webdavBackup.username')}</span>
                    <Input
                        value={username}
                        onChange={(e) => setUsername(e.target.value)}
                        onBlur={() =>
                            save(SettingKey.WebDAVUsername, username, initial.current.username, () => (initial.current.username = username))
                        }
                        className="rounded-xl"
                    />
                </label>
                <label className="grid gap-1">
                    <span className="text-xs text-muted-foreground">{t('webdavBackup.interval')}</span>
                    <Input
                        type="number"
                        min={1}
                        max={720}
                        value={interval}
                        onChange={(e) => setIntervalHours(e.target.value)}
                        onBlur={() =>
                            save(SettingKey.WebDAVInterval, interval, initial.current.interval, () => (initial.current.interval = interval))
                        }
                        className="rounded-xl"
                    />
                </label>
                <label className="grid gap-1">
                    <span className="text-xs text-muted-foreground">{t('webdavBackup.keep')}</span>
                    <Input
                        type="number"
                        min={0}
                        max={365}
                        value={keep}
                        onChange={(e) => setKeep(e.target.value)}
                        onBlur={() => save(SettingKey.WebDAVKeep, keep, initial.current.keep, () => (initial.current.keep = keep))}
                        className="rounded-xl"
                    />
                </label>
            </div>

            {/* 口令只从环境变量读，必须说清楚，否则用户找不到输入框会以为功能没做完 */}
            <div className="rounded-2xl border border-border/50 bg-muted/20 p-3 text-xs text-muted-foreground">
                {t('webdavBackup.passwordHint')}
            </div>

            <div className="flex items-center gap-2">
                <Button onClick={onRun} disabled={runBackup.isPending || !url.trim()} className="rounded-xl">
                    {runBackup.isPending ? <Loader2 className="size-4 animate-spin" /> : <CloudUpload className="size-4" />}
                    {t('webdavBackup.runNow')}
                </Button>
                <Button
                    variant="outline"
                    onClick={() => listQuery.refetch()}
                    disabled={!url.trim() || listQuery.isFetching}
                    className="rounded-xl"
                >
                    <RefreshCw className={`size-4 ${listQuery.isFetching ? 'animate-spin' : ''}`} />
                    {t('webdavBackup.refresh')}
                </Button>
            </div>

            {listQuery.isError && (
                <div className="text-xs text-destructive">
                    {listQuery.error instanceof Error ? listQuery.error.message : t('webdavBackup.listFailed')}
                </div>
            )}
            {listQuery.data && (
                <div className="space-y-1">
                    <div className="text-xs text-muted-foreground">
                        {t('webdavBackup.count', { count: listQuery.data.count })}
                    </div>
                    {listQuery.data.files.length > 0 && (
                        <div className="max-h-32 overflow-auto rounded-xl bg-muted/20 p-2 text-xs font-mono">
                            {listQuery.data.files.map((f) => (
                                <div key={f}>{f}</div>
                            ))}
                        </div>
                    )}
                </div>
            )}
        </div>
    );
}
