import type { SvgIconProps } from '@thesvg/react';

// GenericVendorIcon 是给「没有现成品牌图标」的厂商预设用的占位图标。
//
// 为什么不拿别家的品牌图标顶上：图标是用户辨识服务商的第一眼依据，
// 张冠李戴比"没有专属图标"更危险——用户会以为自己点的是别家的请求。
// 当前只有 Nanogpt 走到这里（@thesvg/react 没收录它）。
//
// 为什么单独一个文件：channel-presets.tsx 只导出常量（CHANNEL_PRESETS 与类型），
// 在里面定义组件会触发 react-refresh/only-export-components
//（一个文件要么只导出组件、要么只导出常量，两者混在一起会让整块热更新失效）。
// 这与 log-metrics.ts、page-preload.ts 的拆法是同一个理由。
export function GenericVendorIcon(props: SvgIconProps) {
    return (
        <svg
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth={1.8}
            strokeLinecap="round"
            strokeLinejoin="round"
            {...props}
        >
            {/* 机架/服务图形：中性地表达「一台上游服务」，不冒充任何品牌。 */}
            <rect x="3" y="4" width="18" height="6" rx="2" />
            <rect x="3" y="14" width="18" height="6" rx="2" />
            <circle cx="7" cy="7" r="0.9" fill="currentColor" stroke="none" />
            <circle cx="7" cy="17" r="0.9" fill="currentColor" stroke="none" />
        </svg>
    );
}
