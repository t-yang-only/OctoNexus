import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, apiRequest } from './client';

/**
 * Setting 数据
 */
export interface Setting {
    key: string;
    value: string;
}

export const SettingKey = {
    ProxyURL: 'proxy_url',
    StatsSaveInterval: 'stats_save_interval',
    ModelInfoUpdateInterval: 'model_info_update_interval',
    CORSAllowOrigins: 'cors_allow_origins',
    ModelFilter: 'model_filter',
    QuotaScanInterval: 'quota_scan_interval',
    QuotaAlertThreshold: 'quota_alert_threshold',
    UsageReportEnabled: 'usage_report_enabled',
    UsageReportPeriod: 'usage_report_period',
    UsageReportHour: 'usage_report_hour',
    RouteBalanceEnabled: 'route_balance_enabled',
    RouteProbeEnabled: 'route_probe_enabled',
    RouteProbeInterval: 'route_probe_interval_seconds',
    AlertWebhookURL: 'alert_webhook_url',
    AlertChannels: 'alert_channels',
    AlertFeishuWebhook: 'alert_feishu_webhook',
    AlertDingTalkWebhook: 'alert_dingtalk_webhook',
    AlertWeComWebhook: 'alert_wecom_webhook',
    AlertServerChanSendKey: 'alert_serverchan_sendkey',
    AlertSMTPHost: 'alert_smtp_host',
    AlertSMTPPort: 'alert_smtp_port',
    AlertSMTPUser: 'alert_smtp_user',
    AlertSMTPFrom: 'alert_smtp_from',
    AlertSMTPTo: 'alert_smtp_to',
    RouteWeightCost: 'route_weight_cost',
    RouteWeightQuality: 'route_weight_quality',
    RouteWeightLatency: 'route_weight_latency',
    RouteWeightBusy: 'route_weight_busy',
    RouteWeightLoad: 'route_weight_load',
    RouteWeightMultiplier: 'route_weight_multiplier',
    RouteWeightPerCall: 'route_weight_per_call',
    RouteWeightBalance: 'route_weight_balance',
    RouteWeightMonthly: 'route_weight_monthly',
    RouteMonthlyAction: 'route_monthly_exhausted_action',
    RequestFaultAction: 'relay_request_fault_action',
    TrustedProxies: 'trusted_proxies',
} as const;

/**
 * 通知渠道状态与发送前真实测试（R-alert-001）
 */
export interface NotifyChannelStatus {
    kind: 'webhook' | 'feishu' | 'dingtalk' | 'wecom' | 'smtp' | 'serverchan';
    enabled: boolean;
    configured: boolean;
    hint: string;
}

export interface NotifyDeliveryResult extends NotifyChannelStatus {
    sent: boolean;
    detail: string;
}

/** 各通知渠道的启用与配置状态；只回"能不能发"，不回显带凭据的地址。 */
export function useNotifyChannels(enabled = true) {
    return useQuery({
        queryKey: ['settings', 'notify', 'channels'],
        queryFn: () => apiRequest<NotifyChannelStatus[]>('/api/v1/setting/notify/channels'),
        enabled,
    });
}

/** 发送前真实测试：用合成事件真的投递一次，逐渠道回报成败与失败原因。 */
export function useTestNotifyChannels() {
    return useMutation({
        mutationFn: () => apiRequest<NotifyDeliveryResult[]>('/api/v1/setting/notify/test', { method: 'POST' }),
        onSuccess: () => undefined,
    });
}

/**
 * 获取 Setting 列表 Hook
 * 
 * @example
 * const { data: settings, isLoading, error } = useSettingList();
 * 
 * if (isLoading) return <Loading />;
 * if (error) return <Error message={error.message} />;
 * 
 * settings?.forEach(setting => console.log(setting.key, setting.value));
 */
export function useSettingList() {
    return useQuery({
        queryKey: ['settings', 'list'],
        queryFn: () => apiRequest<Setting[]>('/api/v1/setting/list'),
        refetchInterval: 30000,
        refetchOnMount: 'always',
    });
}

/**
 * 设置 Setting Hook
 * 
 * @example
 * const setSetting = useSetSetting();
 * 
 * setSetting.mutate({
 *   key: 'theme',
 *   value: 'dark',
 * });
 */
export function useSetSetting() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: Setting) =>
            apiRequest<Setting>('/api/v1/setting/set', { method: 'POST', body: data }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: ['settings', 'list'] }),
    });
}

/**
 * 数据库导入/导出
 */
interface DBImportResult {
    rows_affected: Record<string, number>;
}

function parseFilename(contentDisposition: string | null): string | null {
    if (!contentDisposition) return null;
    // e.g. attachment; filename="octopus-export-20250101120000.json"
    const match = contentDisposition.match(/filename="([^"]+)"/i);
    return match?.[1] ?? null;
}

function exportFallbackFilename() {
    const d = new Date();
    const pad = (n: number) => String(n).padStart(2, '0');
    const ts = `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`;
    return `octopus-export-${ts}.json`;
}

async function downloadBlob(blob: Blob, filename: string) {
    const url = URL.createObjectURL(blob);
    try {
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        a.remove();
    } finally {
        URL.revokeObjectURL(url);
    }
}

/**
 * 导出数据库（下载 JSON 文件）
 */
export function useExportDB() {
    return useMutation({
        mutationFn: async () => {
            const res = await fetch('/api/v1/setting/export', {
                method: 'GET',
                credentials: 'include',
            });

            if (!res.ok) {
                const data = await res.json().catch(() => null) as { message?: string } | null;
                throw new ApiError(res.status, data?.message || `Request failed: ${res.status}`);
            }

            const blob = await res.blob();
            const filename = parseFilename(res.headers.get('content-disposition')) || exportFallbackFilename();
            await downloadBlob(blob, filename);
            return { filename };
        },
    });
}

/**
 * 导入数据库（上传 JSON 文件）
 */
export function useImportDB() {
    return useMutation({
        mutationFn: async (file: File) => {
            const form = new FormData();
            form.append('file', file);

            const res = await fetch('/api/v1/setting/import', {
                method: 'POST',
                body: form,
                credentials: 'include',
            });

            const contentType = res.headers.get('content-type') || '';
            const isJson = contentType.includes('application/json');
            const data = isJson
                ? await res.json() as { message?: string; data?: DBImportResult }
                : await res.text();

            if (!res.ok) {
                const message = typeof data === 'string' ? data : data.message;
                throw new ApiError(res.status, message || `Request failed: ${res.status}`);
            }

            // 支持后端标准 ApiResponse：{code,message,data:{...}}
            return typeof data === 'string' ? data as unknown as DBImportResult : data.data as DBImportResult;
        },
    });
}
