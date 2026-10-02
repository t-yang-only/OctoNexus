import type { CSSProperties, ReactNode } from 'react';
import { useEffect, useRef, useState } from 'react';
import { flushSync } from 'react-dom';
import { AnimatePresence, motion } from 'motion/react';
import { useTranslations } from 'use-intl';
import Logo from '@/components/modules/logo';
import { NAV_ITEMS, useAppStore } from '@/stores/app';
import { preloadPage } from '@/lib/page-preload';
import { cn } from '@/lib/utils';

// AppShell 作为普通用户界面的稳定布局层，统一渲染导航、顶栏和页面内容。
export function AppShell({ children, actions }: { children: ReactNode; actions?: ReactNode }) {
    const currentPage = useAppStore((state) => state.currentPage);
    const direction = useAppStore((state) => state.direction);
    const setCurrentPage = useAppStore((state) => state.setCurrentPage);
    const t = useTranslations('navbar');
    const activeIndex = NAV_ITEMS.findIndex((route) => route.id === currentPage); // activeIndex 表示选中项在 Dock 中的位置。
    const [hoveredIndex, setHoveredIndex] = useState<number | null>(null); // hoveredIndex 表示当前悬浮项的位置。
    const [isNavHovered, setIsNavHovered] = useState(false); // isNavHovered 表示悬浮背景是否显示。
    const hoverIndicatorRef = useRef<HTMLSpanElement>(null); // hoverIndicatorRef 用于在淡入前确认悬浮背景的新位置。
    const navRef = useRef<HTMLElement>(null); // navRef 指向导航容器，用于把当前项滚进可视区。

    // 窄屏下导航是横向滚动容器（见下方 className 的说明），切页时把当前项滚到中间。
    // 不做这件事的话，从主页切到「设置」会看到高亮停在屏幕外 —— 看起来像没生效。
    //
    // 这里手算目标位置而不是调 scrollIntoView：后者会连带滚动所有祖先滚动容器
    //（页面正文、抽屉等），在固定定位的导航上容易把别处一起带偏；
    // 手算只动导航自己这一条横向轴。桌面端导航是纵向排布、不可横向滚动，此处为空操作。
    useEffect(() => {
        const nav = navRef.current;
        if (!nav) return;
        const active = nav.querySelector<HTMLElement>('[aria-current="page"]');
        if (!active) return;
        nav.scrollTo({
            left: active.offsetLeft - (nav.clientWidth - active.clientWidth) / 2,
            behavior: 'smooth',
        });
    }, [currentPage]);

    return (
        <div className="mx-auto flex h-dvh max-w-[112rem] animate-in flex-col overflow-hidden px-3 fade-in duration-300 md:grid md:grid-cols-[auto_1fr] md:grid-rows-[auto_minmax(0,1fr)] md:gap-x-6 md:px-6">
            {/* 侧边栏容器：跨两行占满可视高度，并用 flex 把导航**垂直居中**。
                早先是 `md:sticky md:top-30`，但 shell 自己是 h-dvh + overflow-hidden
                （页面级根本不滚动，滚动都发生在各页内部容器里），所以 sticky 从头到尾
                没有生效过 —— 实际效果只是"钉在距顶 120px 处"，高一点的屏幕下方就空一大块。
                改成由这一层负责定位，导航本身回归普通文档流。 */}
            <div className="relative z-50 md:row-span-2 md:flex md:h-dvh md:items-center">
                <nav
                    ref={navRef}
                    aria-label="Main Navigation"
                    className={cn(
                        'fixed bottom-6 left-1/2 isolate -translate-x-1/2 flex animate-in items-center gap-1 p-3 fade-in zoom-in-95 duration-300',
                        // 窄屏放不下：13 个图标按 44px 步进约 596px，而手机视口只有 430px。
                        // 此前没有这条约束，导航宽 550px、左右各被切掉 60px（实测 430px 视口下
                        // navLeft=-60 / navRight=490），最左最右两项实际点不到 —— 只是没人注意。
                        // 改成「限宽 + 横向滚动 + 隐藏滚动条」：所有项都可达，外观不变。
                        'max-w-[calc(100vw-1.5rem)] overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden',
                        'md:static md:left-auto md:bottom-auto md:translate-x-0 md:flex-col md:gap-3',
                        // 桌面端是纵向排布、不需要滚动：恢复可见，避免滚动容器改变子项定位基准。
                        'md:max-w-none md:overflow-visible',
                        'bg-sidebar text-sidebar-foreground border border-sidebar-border rounded-3xl',
                    )}
                    onMouseLeave={() => setIsNavHovered(false)}
                >
                    <span
                        aria-hidden="true"
                        className="pointer-events-none absolute left-3 top-3 z-10 size-10 rounded-2xl bg-sidebar-primary transition-transform duration-300 ease-out [transform:translateX(var(--nav-offset-x))] md:size-12 md:[transform:translateY(var(--nav-offset-y))]"
                        style={{
                            '--nav-offset-x': `${activeIndex * 2.75}rem`,
                            '--nav-offset-y': `${activeIndex * 3.75}rem`,
                        } as CSSProperties}
                    />
                    <span
                        ref={hoverIndicatorRef}
                        aria-hidden="true"
                        className={cn(
                            'pointer-events-none absolute left-3 top-3 z-0 size-10 [transform:translateX(var(--nav-offset-x))] md:size-12 md:[transform:translateY(var(--nav-offset-y))]',
                            isNavHovered ? 'transition-transform duration-300 ease-out' : 'transition-none',
                        )}
                        style={{
                            '--nav-offset-x': `${(hoveredIndex ?? activeIndex) * 2.75}rem`,
                            '--nav-offset-y': `${(hoveredIndex ?? activeIndex) * 3.75}rem`,
                        } as CSSProperties}
                    >
                        <span
                            className="absolute inset-0 rounded-2xl bg-sidebar-accent transition-opacity duration-350 ease-linear"
                            style={{ opacity: isNavHovered ? 1 : 0 }}
                        />
                    </span>
                    {NAV_ITEMS.map((route, index) => {
                        const isActive = currentPage === route.id;

                        return (
                            <button
                                key={route.id}
                                type="button"
                                aria-label={route.label}
                                aria-current={isActive ? 'page' : undefined}
                                onMouseEnter={() => {
                                    // 首次进入先在不可见状态下定位，避免背景从上一次位置移动过来。
                                    if (isNavHovered) {
                                        setHoveredIndex(index);
                                    } else {
                                        flushSync(() => setHoveredIndex(index));
                                        hoverIndicatorRef.current?.getBoundingClientRect();
                                        setIsNavHovered(true);
                                    }
                                    preloadPage(route.id);
                                }}
                                onFocus={() => preloadPage(route.id)}
                                onTouchStart={() => preloadPage(route.id)}
                                onClick={() => {
                                    preloadPage(route.id);
                                    setCurrentPage(route.id);
                                }}
                                className={cn(
                                    'relative z-20 flex size-10 items-center justify-center rounded-2xl p-2 transition-[color,scale] duration-150 ease-linear hover:z-30 hover:scale-110 active:scale-95 md:size-12 md:p-3',
                                    isActive ? 'text-sidebar-primary-foreground' : 'text-sidebar-foreground/60',
                                )}
                            >
                                <span className="relative z-10">
                                    <route.icon strokeWidth={2} />
                                </span>
                            </button>
                        );
                    })}
                </nav>
            </div>

            <header className="my-3 md:my-6 flex flex-none items-center gap-x-2 px-2">
                <Logo size={48} />
                <div className="min-w-0 flex-1 overflow-hidden">
                    <AnimatePresence mode="wait" custom={direction}>
                        <motion.div
                            key={currentPage}
                            custom={direction}
                            variants={{
                                initial: (value: number) => ({ y: 32 * value, opacity: 0 }),
                                animate: { y: 0, opacity: 1 },
                                exit: (value: number) => ({ y: -32 * value, opacity: 0 }),
                            }}
                            initial="initial"
                            animate="animate"
                            exit="exit"
                            transition={{ duration: 0.3 }}
                            className="flex items-center"
                        >
                            <span className="mt-1 truncate text-3xl font-bold">
                                {t(currentPage)}
                            </span>
                        </motion.div>
                    </AnimatePresence>
                </div>
                {actions && <div className="ml-auto">{actions}</div>}
            </header>

            <main className="relative flex min-h-0 w-full min-w-0 flex-1 flex-col overflow-hidden">
                {children}
            </main>
        </div>
    );
}
