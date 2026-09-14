export function normalizeKey(value: string) {
    return value.trim().toLowerCase();
}

// memberKey 按引用类型区分成员键：授权成员 g:<grant>，子分组成员 c:<child>。
// 旧库内授权成员同时接受裸数字串（String(grant)）的比较，见 matchesMemberKey。
export function memberKey(member: { kind?: 'grant' | 'child'; channel_grant_id: number; child_group_id?: number }) {
    if (member.kind === 'child') return `c:${member.child_group_id ?? 0}`;
    return `g:${member.channel_grant_id}`;
}

// matchesMemberKey 比较两个成员引用是否相同，兼容旧形状裸数字键（授权成员）。
export function matchesMemberKey(a: string, b: string) {
    if (a === b) return true;
    // 旧形状只有裸数字（授权成员）；新形状 g:<grant> 与之数值相等即视为同一成员。
    const strip = (s: string) => (s.startsWith('g:') ? s.slice(2) : s);
    if (a.startsWith('c:') || b.startsWith('c:')) return false;
    return strip(a) !== '' && strip(a) === strip(b);
}

export function matchesGroupName(modelName: string, groupKey: string) {
    if (!groupKey) return false;
    return modelName.toLowerCase().includes(groupKey);
}
