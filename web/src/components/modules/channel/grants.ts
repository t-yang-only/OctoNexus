// 授权矩阵里"某凭据能不能供某模型"的判定, 与界面无关, 单测可直接跑。
//
// 同一渠道的多个凭据可能各自只支持上游的一部分模型: 一个供应商配两个密钥, 一个发 Gemini 系,
// 一个发 OpenAI 系。此时协议全选若按 模型 × 凭据 全量置位, 会给 model-a 记上"凭据 B 可用",
// 转发时就会把 model-a 派给供不了它的凭据, 请求必然失败。故三件事都要按可用性收敛:
// 全选跳过不可用组合, 不可用组合在界面上不可勾选, 保存时不落这些授权。
//
// 判定依据只有一处: 该凭据最近一次成功的探测结果。没有探测记录的凭据视为"未知"一律放行,
// 不拿"没探到"当"不支持"——手填模型和尚未探测的凭据都不该被拦，否则会误伤正在使用的路由。
export type KeyModelIndex = Map<string, string[]>; // 键为凭据名, 值为该凭据已确认支持的模型名。

// isPairSupported 判断 (模型, 凭据) 组合可分发生效; 无探测记录的凭据一律视为可用。
export function isPairSupported(index: KeyModelIndex, modelName: string, keyName: string): boolean {
    const supported = index.get(keyName);
    if (!supported) return true;
    return supported.includes(modelName);
}

// supportedKeyNames 给出某模型下真正可分发的凭据名, 批量勾选与 "已授权/可用" 计数都按它算。
// 计数按可用凭据而非全部凭据: 供不了该模型的凭据本来就不该计入分母, 否则一行的 3/3 是假的。
export function supportedKeyNames(index: KeyModelIndex, modelName: string, keyNames: string[]): string[] {
    return keyNames.filter((keyName) => isPairSupported(index, modelName, keyName));
}

// unsupportedPairs 列出当前模型集合与凭据集合中已知不可用的组合, 保存前按它裁剪。
// 只列"有探测记录且不含该模型"的凭据, 未知凭据的组合一律不列。
export function unsupportedPairs(
    index: KeyModelIndex,
    models: string[],
    keyNames: string[],
): [string, string][] {
    const pairs: [string, string][] = [];
    for (const modelName of models) {
        for (const keyName of keyNames) {
            if (!isPairSupported(index, modelName, keyName)) pairs.push([modelName, keyName]);
        }
    }
    return pairs;
}

// recordProbe 记录一次成功探测的模型清单, 以本次实测结果替换该凭据的旧结论。
// 探测没拿到任何模型时上游必然异常, 调用方不会把空清单传进来; 此处再挡一次,
// 保证一次故障不会把凭据改判成"什么模型都不支持"而牵连已有授权。
// 模型名原样收下不裁剪空白: 它要和表单里的模型名逐字相等才判为可用, 裁剪会造成"探到了却判不支持"。
export function recordProbe(index: KeyModelIndex, keyName: string, models: string[]): KeyModelIndex {
    const unique = [...new Set(models.filter((name) => name.trim() !== ''))];
    if (unique.length === 0) return index;
    const next = new Map(index);
    next.set(keyName, unique);
    return next;
}

// declareModel 记录人工声明: 手工添加或勾选即用户明确说"这个凭据供这个模型", 优先于探测结论。
// 少了这一步, 手填一个上游模型列表里没有的模型后, 该组合会立刻被自己的结论锁死。
// 没有探测记录的凭据不建记录: "无记录=不限制" 已经放行, 建了反而会开始限制其他模型。
export function declareModel(index: KeyModelIndex, keyName: string, modelName: string): KeyModelIndex {
    const supported = index.get(keyName);
    if (!supported || supported.includes(modelName)) return index;
    const next = new Map(index);
    next.set(keyName, [...supported, modelName]);
    return next;
}
