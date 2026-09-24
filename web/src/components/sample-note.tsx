import { useTranslations } from 'use-intl';
import type { RelayLogSample } from '@/api/analytics';

/**
 * SampleNote 显示画像样本的来源账（T-trace-006）。
 *
 * 画像默认剔除客户端声明的测试请求：否则每次发版验证发的那几条请求都会扰动成功率与
 * 延迟分布，让「这一版到底变好没有」失去可比性。
 *
 * 但剔除必须**看得见**。只给一个剩余条数，读的人会以为样本凭空少了，于是白白去查一次
 * "是不是丢数据了" —— 把「剔除了几条」写在卡片上，这趟排查就不必发生。
 *
 * 没有测试请求时返回 null（不占位置）：常态下这里不该有噪音。
 */
export function SampleNote({ sample, className }: { sample?: RelayLogSample; className?: string }) {
    const t = useTranslations('common');
    if (!sample || sample.test_skipped <= 0) return null;
    return (
        <p className={className ?? 'text-xs text-muted-foreground'}>
            {t('sampleNote', { count: sample.test_skipped, total: sample.window })}
        </p>
    );
}
