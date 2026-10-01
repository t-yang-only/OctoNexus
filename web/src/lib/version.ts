// 版本号比较（T-identity-001 余项：修掉"把降级当升级"的误报）。
//
// # 这个 bug 的真实形态
//
// 线上版本 v0.79.0，而仓库里有一个 fork 时遗留的 v0.14.0 release。更新检查器读到它，
// 前端按**字符串不等**比较（latest !== current）判定"有新版本" —— 于是界面显示
// "发现新版本可用 v0.14.0"。用户点"立即更新"会把上游旧版二进制装进来：
// 那不是升级，而是把本项目的上百个提交、33 张表的数据模型整套抹掉。
//
// # 为什么用三元组而不是字符串
//
// 版本号是点分数字，字典序与大小序不一致：v0.9.0 在字典序上"大于" v0.10.0
// （'9' > '1'），按字符串比会得出"0.9 比 0.10 新"的相反结论。
//
// # 解析失败的取向
//
// 认不出的形状（空串、缺段、含非数字）返回 ok=false，调用方据此**不提示更新**。
// 宁可漏提示，也不能误提示 —— 漏提示只是"没告诉你"，误提示会让人主动降级。
//
// 这里刻意只认 v<数字>.<数字>.<数字> 的主干，忽略预发布与构建后缀：
// 本项目的 release tag 就是这种形状，多认几种写法只会增加"认错"的面积。

/** 解析出的版本号主干。 */
export interface ParsedVersion {
    major: number;
    minor: number;
    patch: number;
}

/**
 * parseVersion 解析版本号主干；ok=false 表示形状不被支持。
 *
 * 容忍两种常见前缀污染：`refs/tags/v1.2.3` 与裸 `1.2.3`（GitHub 的 tag_name 带不带 v
 * 取决于发布者怎么打的 tag，两种都见过）。
 */
export function parseVersion(raw: string): { version: ParsedVersion; ok: boolean } {
    let text = (raw || '').trim();
    if (!text) return { version: { major: 0, minor: 0, patch: 0 }, ok: false };

    // 去掉 refs/tags/ 这类前缀，只留最后一段。
    const slash = text.lastIndexOf('/');
    if (slash >= 0) text = text.slice(slash + 1);
    if (text.startsWith('v') || text.startsWith('V')) text = text.slice(1);

    const parts = text.split('.');
    if (parts.length < 2 || parts.length > 3) {
        return { version: { major: 0, minor: 0, patch: 0 }, ok: false };
    }
    const numbers: number[] = [];
    for (const part of parts) {
        // 只收纯数字段；`1.2.3-rc1` 这类带后缀的形状整体判为不支持
        // （宁可不提示，也不按错的值提示）。
        if (!/^\d+$/.test(part)) {
            return { version: { major: 0, minor: 0, patch: 0 }, ok: false };
        }
        numbers.push(Number(part));
    }
    while (numbers.length < 3) numbers.push(0);
    return { version: { major: numbers[0], minor: numbers[1], patch: numbers[2] }, ok: true };
}

/** compareVersions 比较两个已解析版本：1 = a 更新，-1 = a 更旧，0 = 同版本。 */
export function compareVersions(a: ParsedVersion, b: ParsedVersion): number {
    if (a.major !== b.major) return a.major > b.major ? 1 : -1;
    if (a.minor !== b.minor) return a.minor > b.minor ? 1 : -1;
    if (a.patch !== b.patch) return a.patch > b.patch ? 1 : -1;
    return 0;
}

/**
 * hasNewerVersion 判断 latest 是否真的比 current 新。
 *
 * 任一形状不被支持时返回 false（不提示）。这正是本函数存在的理由：
 * 旧实现用 `latest !== current`，任何不同的 tag 都算"有新版本"，
 * 于是把旧版本号当成了升级目标。
 */
export function hasNewerVersion(latest: string, current: string): boolean {
    const a = parseVersion(latest);
    const b = parseVersion(current);
    if (!a.ok || !b.ok) return false;
    return compareVersions(a.version, b.version) > 0;
}
