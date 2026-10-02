import { AllocationMonitor } from '@/components/modules/home/allocation';
import { GroupHealthPanel } from '@/components/modules/home/group-health';
import { RequestInsight } from '@/components/modules/home/insight';
import { LatencyDistributionPanel } from '@/components/modules/home/latency';
import { ModelMonitor } from '@/components/modules/home/monitor';
import { PerformancePanel } from '@/components/modules/home/performance';
import RoutingProfilePanel from '@/components/modules/home/routing';

// AnalyticsSections 汇总「深度分析」类区块。
//
// ## 为什么单独成页
//
// 这些区块原先全堆在主页上（加上概览一共 12 块），主页要滚很久才看得完，
// 而且真正高频的四个数字（请求次数/词元/费用/余额）被埋在长列表最上面一屏之后。
// 拆成两页之后：主页回答「现在大概什么状况」，本页回答「具体哪里有问题」。
//
// ## 分组依据（不是随手对半切）
//
// 留下的是**概览读数**（总量、余额、活跃度、趋势、榜单）——一眼能看完、每次都看；
// 搬过来的是**诊断读数**（性能吞吐、模型分布、请求窗口、延迟分布、路由画像、
// 分组健康、额度分压）——要定位问题才会看，而且每块自己都带筛选与下钻。
//
// 面板实现仍放在 home 模块里（它们是同一批组件，搬文件只增加改动面）；
// 本页只负责编排顺序，与主页共用同一套组件。
export function AnalyticsSections() {
    return (
        <div className="@container/analytics space-y-6">
            {/* 性能指标在最前：RPM / TPM / 吞吐是「现在跑得动多快」的直接读数。 */}
            <PerformancePanel />
            {/* 模型调用分析：按模型拆分请求与成本，回答「钱花在哪几个模型上」。 */}
            <ModelMonitor />
            {/* 请求窗口分析：时间线堆叠 / 失败归因 / RPM 与 TPM，读 relay_logs 明细。 */}
            <RequestInsight />
            {/* 延迟分布：回答「整体有多慢、慢是普遍的还是被少数拖累的」。 */}
            <LatencyDistributionPanel />
            {/* 路由画像：选路模式与成员命中分布。 */}
            <RoutingProfilePanel />
            {/* 分组健康：哪些分组的成员正在冷却/不可用。 */}
            <GroupHealthPanel />
            {/* 额度分压：各成员的剩余额度与分配比例。 */}
            <AllocationMonitor />
        </div>
    );
}

// Analytics 渲染深度分析正文。
// 滚动容器与主页同构（h-full + 内部滚动 + 移动端给底部导航留出 pb-24）。
export function Analytics() {
    return (
        <div className="h-full min-h-0 overflow-y-auto overscroll-contain rounded-t-3xl pb-24 md:pb-4">
            <AnalyticsSections />
        </div>
    );
}
