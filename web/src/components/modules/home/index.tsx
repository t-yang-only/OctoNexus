import { useEffect, useRef, useState } from 'react';
import { Copy, Download, Eye, EyeOff, Loader2, Share2, X } from 'lucide-react';
import { snapdom } from '@zumer/snapdom';
import { toast } from 'sonner';
import { useTranslations } from 'use-intl';
import dayjs from 'dayjs';
import { buttonVariants } from '@/components/ui/button';
import Logo from '@/components/modules/logo';
import { Activity } from './activity';
import { Total } from './total';
import { Balance } from './balance';
import { PerformanceCompact } from './performance-compact';
import { StatsChart } from './chart';
import { Rank } from './rank';
import { useHomeViewStore } from './store';

// HomeSections 汇总首页的**概览**区块, 屏内正文与分享图舞台共用。
//
// ## 主页只放概览（本轮拆分）
//
// 此前主页堆了 12 块，这一屏要滚很久，而最高频的四个数字（请求次数 / 词元 / 费用 /
// 余额）被埋在长列表里。现在按「看数据的频率」分层：
//   · 主页 = **概览读数**：一眼能看完、每次都看（总量 / 余额 / 活跃度 / 趋势 / 榜单）；
//   · 分析页（导航「分析」）= **诊断读数**：定位问题才会看，且每块自带筛选与下钻
//     （性能吞吐 / 模型分布 / 请求窗口 / 延迟分布 / 路由画像 / 分组健康 / 额度分压）。
// 深度分析的面板实现仍在本模块内，由 @/components/modules/analytics 编排。
//
// 这么切的直接收益有两个：主页不用再滚；分享图产出的是干净的概览而不是一条长图。
function HomeSections() {
    return (
        <div className="@container/home space-y-6">
            {/* 四个总量卡片：请求次数 / 词元 / 费用，主页的唯一「一句话报数」。 */}
            <Total />
            {/* 总余额与未读渠道归类：账目概览。 */}
            <Balance />
            {/* 性能指标简版：只留 RPM / TPM / 吞吐 三个当下读数（完整版在分析页）。 */}
            <PerformanceCompact />
            {/* 活跃度热力图：一眼看出量在什么时候来。 */}
            <Activity />
            {/* 趋势图：概览量级的时间走向。 */}
            <StatsChart />
            {/* 榜单：渠道/分组/模型的排行概览。 */}
            <Rank />
        </div>
    );
}

// Home 渲染首页统计正文。
export function Home() {
    return (
        <div className="h-full min-h-0 overflow-y-auto overscroll-contain rounded-t-3xl pb-24 md:pb-4">
            <HomeSections />
        </div>
    );
}

// HomeActions 向稳定顶栏提供渠道名模糊开关和分享入口, 并承载分享图舞台与预览。
export function HomeActions() {
    const t = useTranslations('toolbar');
    const tCommon = useTranslations('common');
    const isChannelNameHidden = useHomeViewStore((state) => state.isChannelNameHidden);
    const setChannelNameHidden = useHomeViewStore((state) => state.setChannelNameHidden);
    const [isStaged, setIsStaged] = useState(false); // 为真时屏外挂载正文副本供截图, 期间分享按钮转为加载态。
    const [preview, setPreview] = useState<{ url: string; blob: Blob } | null>(null); // url 用于展示, blob 用于复制和下载。
    const stageRef = useRef<HTMLDivElement>(null); // 屏外副本的根节点, 截图取材于此。

    // 预览关闭或组件卸载时释放临时对象 URL。
    useEffect(() => () => {
        if (preview) URL.revokeObjectURL(preview.url);
    }, [preview]);

    // 屏外副本在自身树内按固定宽度布局, 图表和栅格都不受当前窗口宽度影响; 等其动画结束后截图。
    useEffect(() => {
        if (!isStaged) return;

        let cancelled = false;
        const timer = window.setTimeout(async () => {
            if (cancelled || !stageRef.current) return;
            try {
                const blob = await snapdom.toBlob(stageRef.current, { type: 'png', scale: 2, embedFonts: true });
                if (!cancelled) setPreview({ url: URL.createObjectURL(blob), blob });
            } catch (error) {
                if (!cancelled) toast.error(error instanceof Error ? error.message : String(error));
            } finally {
                if (!cancelled) setIsStaged(false);
            }
            // snapdom 只序列化当前 DOM, 不推进动画, 故等副本自身动画跑完: 趋势图 1500ms, 数字 800ms, 余量 300ms。
        }, 1800);

        return () => {
            cancelled = true;
            window.clearTimeout(timer);
        };
    }, [isStaged]);

    const handleCopyImage = async () => {
        if (!preview) return;
        try {
            await navigator.clipboard.write([new ClipboardItem({ 'image/png': preview.blob })]);
            toast.success(tCommon('copy.success'));
        } catch {
            toast.error(tCommon('copy.failed'));
        }
    };

    const handleDownloadImage = () => {
        if (!preview) return;
        const link = document.createElement('a');
        link.href = preview.url;
        link.download = `octopus-${dayjs().format('YYYYMMDD')}.png`;
        link.click();
    };

    return (
        <div className="flex items-center gap-2">
            <button
                type="button"
                onClick={() => setChannelNameHidden(!isChannelNameHidden)}
                aria-pressed={isChannelNameHidden}
                aria-label={t('hideChannelName')}
                className={buttonVariants({
                    variant: 'ghost',
                    size: 'icon',
                    className: 'rounded-xl transition-none hover:bg-transparent text-muted-foreground hover:text-foreground',
                })}
            >
                {isChannelNameHidden
                    ? <EyeOff className="size-4 transition-colors duration-300" />
                    : <Eye className="size-4 transition-colors duration-300" />}
            </button>

            <button
                type="button"
                onClick={() => setIsStaged(true)}
                disabled={isStaged}
                aria-label={t('share')}
                className={buttonVariants({
                    variant: 'ghost',
                    size: 'icon',
                    className: 'rounded-xl transition-none hover:bg-transparent text-muted-foreground hover:text-foreground disabled:opacity-50',
                })}
            >
                {isStaged
                    ? <Loader2 className="size-4 animate-spin" />
                    : <Share2 className="size-4 transition-colors duration-300" />}
            </button>

            {isStaged && (
                <div
                    ref={stageRef}
                    aria-hidden="true"
                    className="fixed top-0 rounded-3xl bg-background p-4 text-foreground"
                    // 宽度固定, 使截图不随窗口宽度变化。1002 是热力图恰好铺满且不横向溢出的宽度:
                    // 网格 54 列 × 14px + 53 × 4px 间隙 = 968px, 加滚动容器 p-4 的左右 16px, 再加卡片左右各 1px 边框。
                    // content-box 使该宽度落在正文上, 内边距向外扩展; 定位到屏外避免闪动。
                    style={{ left: '-10000px', boxSizing: 'content-box', width: '1002px' }}
                >
                    {/* 分享图抬头, 与应用顶栏一致的标识和名称。 */}
                    <div className="mb-4 flex items-center gap-x-2 px-2">
                        <Logo size={48} />
                        <span className="text-3xl font-bold">Octopus</span>
                    </div>
                    <HomeSections />
                </div>
            )}

            {preview && (
                <div className="fixed inset-0 z-60 flex flex-col items-center justify-center gap-3 bg-background/95 p-4 backdrop-blur-sm">
                    <img
                        src={preview.url}
                        alt=""
                        className="max-h-[75vh] min-h-0 w-auto max-w-full rounded-2xl border border-border object-contain"
                    />
                    <div className="flex shrink-0 items-center gap-2">
                        <button
                            type="button"
                            onClick={handleCopyImage}
                            className="flex size-10 items-center justify-center rounded-xl border border-border bg-card text-muted-foreground transition-colors hover:text-foreground active:scale-95"
                        >
                            <Copy className="size-4" />
                        </button>
                        <button
                            type="button"
                            onClick={handleDownloadImage}
                            className="flex size-10 items-center justify-center rounded-xl border border-border bg-card text-muted-foreground transition-colors hover:text-foreground active:scale-95"
                        >
                            <Download className="size-4" />
                        </button>
                        <button
                            type="button"
                            onClick={() => setPreview(null)}
                            className="flex size-10 items-center justify-center rounded-xl border border-border bg-card text-muted-foreground transition-colors hover:text-foreground active:scale-95"
                        >
                            <X className="size-4" />
                        </button>
                    </div>
                </div>
            )}
        </div>
    );
}
