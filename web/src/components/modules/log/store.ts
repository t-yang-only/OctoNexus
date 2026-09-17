import { create } from 'zustand';
import { persist } from 'zustand/middleware';

// LogFieldName 是日志卡片上可独立开关的字段标识。
export type LogFieldName =
    | 'time'
    | 'apiKey'
    | 'duration'
    | 'firstByte'
    | 'attempts'
    | 'cost'
    | 'tps'
    | 'cacheHitRate'
    | 'prompt'
    | 'cached'
    | 'completion';

export type LogFieldVisibility = Record<LogFieldName, boolean>;

// DEFAULT_LOG_FIELD_VISIBILITY 是字段可见性的出厂默认值: 全部可见。
export const DEFAULT_LOG_FIELD_VISIBILITY: LogFieldVisibility = {
    time: true,
    apiKey: true,
    duration: true,
    firstByte: true,
    attempts: true,
    cost: true,
    tps: true,
    cacheHitRate: true,
    prompt: true,
    cached: true,
    completion: true,
};

interface LogFieldVisibilityState {
    visibility: LogFieldVisibility; // 各字段当前的可见性。
    toggleField: (field: LogFieldName) => void; // 翻转指定字段的可见性。
    resetFields: () => void; // 恢复出厂默认值。
}

// useLogFieldVisibilityStore 以 zustand persist 保存字段可见性偏好到本地浏览器。
export const useLogFieldVisibilityStore = create<LogFieldVisibilityState>()(
    persist(
        (set) => ({
            visibility: { ...DEFAULT_LOG_FIELD_VISIBILITY },
            toggleField: (field) =>
                set((state) => ({
                    visibility: { ...state.visibility, [field]: !state.visibility[field] },
                })),
            resetFields: () => set({ visibility: { ...DEFAULT_LOG_FIELD_VISIBILITY } }),
        }),
        {
            name: 'log-field-visibility-storage',
            partialize: (state) => ({ visibility: state.visibility }),
        }
    )
);

export function useLogFieldVisibility() {
    return useLogFieldVisibilityStore((s) => s.visibility);
}

// LOG_AUTO_REFRESH_OPTIONS 是日志列表自动刷新间隔（秒）, 0 表示关闭。
// SSE 本就是实时推送, 该间隔仅用于断线/空闲兜底重建。
export const LOG_AUTO_REFRESH_OPTIONS = [0, 5, 10, 30] as const;
export type LogAutoRefreshInterval = (typeof LOG_AUTO_REFRESH_OPTIONS)[number];

interface LogAutoRefreshState {
    interval: LogAutoRefreshInterval; // 当前选中的刷新间隔。
    setInterval: (value: LogAutoRefreshInterval) => void; // 更新刷新间隔。
}

// useLogAutoRefreshStore 以 zustand persist 保存自动刷新偏好到本地浏览器。
export const useLogAutoRefreshStore = create<LogAutoRefreshState>()(
    persist(
        (set) => ({
            interval: 0 as LogAutoRefreshInterval,
            setInterval: (value) => set({ interval: value }),
        }),
        {
            name: 'log-auto-refresh-storage',
            partialize: (state) => ({ interval: state.interval }),
        }
    )
);
