// formatTPS 按输出 token 与总耗时毫秒计算每秒 token 数, 输入为零或负时返回占位。
// 公式语义借鉴 lingyuins/octopus 的卡片 TPS（tokens/timeMs→tk/s 分档）, 实现逐行重写。
export function formatTPS(tokens: number, timeMs: number): string {
    if (tokens <= 0 || timeMs <= 0) return '- tk/s';
    const tps = tokens / (timeMs / 1000);
    if (tps >= 100) return `${tps.toFixed(0)} tk/s`;
    if (tps >= 10) return `${tps.toFixed(1)} tk/s`;
    return `${tps.toFixed(2)} tk/s`;
}

// formatCacheHitRate 计算缓存命中率 = cacheReadTokens / totalTokens, 输入无效时返回占位。
// 公式语义借鉴 lingyuins/octopus 的缓存命中率（cacheRead/total 分档百分比）, 实现逐行重写。
export function formatCacheHitRate(cacheRead: number, total: number): string {
    if (cacheRead <= 0 || total <= 0) return '-';
    const rate = (cacheRead / total) * 100;
    if (rate >= 100) return '100%';
    if (rate >= 10) return `${rate.toFixed(1)}%`;
    return `${rate.toFixed(2)}%`;
}

// formatCNYCost 以 2 有效数字格式化费用（CNY）, 零直接给两位小数。
// 公式语义借鉴 lingyuins/octopus 的 costFmt, 实现逐行重写。
export function formatCNYCost(value: number): string {
    if (value === 0) return '0.00';
    const abs = Math.abs(value);
    if (abs >= 1) return value.toFixed(2);
    const significant = Number(abs.toPrecision(2));
    const decimals = Math.max(0, 1 - Math.floor(Math.log10(significant)));
    return (value < 0 ? '-' : '') + significant.toFixed(decimals);
}

// formatFirstByteMs 将"请求到达至首字节"的毫秒数格式化为紧凑文本, 未提交（-1）返回占位。
export function formatFirstByteMs(value: number): string {
    if (value < 0) return '--';
    if (value < 1000) return `${Math.round(value)}ms`;
    return `${(value / 1000).toFixed(2)}s`;
}
