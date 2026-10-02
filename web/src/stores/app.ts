import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { LucideIcon } from 'lucide-react';
import { Boxes, ChartLine, FolderTree, History, Home, Network, Puzzle, Radio, ScrollText, Settings, Sparkles, TrendingUp, UserRound } from 'lucide-react';

// Page 表示应用支持的固定页面集合。
export type Page = 'home' | 'channel' | 'group' | 'model' | 'account' | 'pool' | 'proxy' | 'price' | 'extensions' | 'log' | 'projectlog' | 'analytics' | 'setting';

// NavItem 描述导航按钮使用的页面标识、文案和图标。
type NavItem = { id: Page; label: string; icon: LucideIcon };

// NAV_ITEMS 是桌面和移动导航共用的固定导航定义。
export const NAV_ITEMS: NavItem[] = [
    { id: 'home', label: 'Home', icon: Home },
    // 「分析」紧跟在主页之后：它是主页的下钻视图（主页给概览、分析给明细），
    // 两者是一对，放一起才找得到。此前排在「日志」之后、倒数第二位，
    // 13 项导航里要翻到底才看得见。
    { id: 'analytics', label: 'Analytics', icon: ChartLine },
    { id: 'channel', label: 'Channel', icon: Radio },
    { id: 'group', label: 'Group', icon: FolderTree },
    { id: 'model', label: 'Model', icon: Sparkles },
    { id: 'account', label: 'Account', icon: UserRound },
    { id: 'pool', label: 'Pool', icon: Boxes },
    { id: 'proxy', label: 'Proxy', icon: Network },
    { id: 'price', label: 'Price', icon: TrendingUp },
    { id: 'extensions', label: 'Extensions', icon: Puzzle },
    // 这一项展示的是"每一次调用留下了什么"，不只是排错用的日志流：
    // 因此图标用 History（记录/历史）而不是 Logs（日志滚动条），文案也改叫「使用记录」（需求7）。
    { id: 'log', label: 'Usage', icon: History },
    // 新增「日志」= 项目侧诊断（尝试链聚合等），与使用记录分工：
    // 使用记录回答「我调了什么」，日志回答「链路哪里在出问题」。
    { id: 'projectlog', label: 'Logs', icon: ScrollText },
    { id: 'setting', label: 'Setting', icon: Settings },
];

const NAV_ORDER: Page[] = NAV_ITEMS.map((item) => item.id); // NAV_ORDER 用于计算页面名称滚动方向。

interface AppState {
    currentPage: Page; // 当前选中的固定页面。
    direction: number; // 页面名称切换时的滚动方向。
    setCurrentPage: (page: Page) => void; // 切换当前页面。
}

// useAppStore 保存应用当前页面及页面名称切换方向。
export const useAppStore = create<AppState>()(
    persist(
        (set, get) => ({
            currentPage: 'home',
            direction: 0,
            setCurrentPage: (page) => {
                const currentIndex = NAV_ORDER.indexOf(get().currentPage);
                const nextIndex = NAV_ORDER.indexOf(page);
                set({ currentPage: page, direction: nextIndex > currentIndex ? 1 : -1 });
            },
        }),
        {
            name: 'nav-storage',
        }
    )
);
