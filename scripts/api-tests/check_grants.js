// 渠道授权矩阵按凭据实际支持的模型收敛（上游 #387）纯函数自检：
// 编译产物由 esbuild 生成（grants.cjs / state.cjs），零依赖运行。
//
// 为什么单独立这条套件：这一改动的效果全在界面与提交链路上——全选跳过不可用组合、不可用组合勾不上、
// 保存时不落无效授权。后端接口不会因为少了这层收敛而报错，只会让请求被派给供不了该模型的凭据、
// 在上游失败。故把判定与裁剪抽成纯函数，在这里逐个边界钉住：未知不设限、按凭据隔离、空探测不改判、
// 人工声明优先。
const { isPairSupported, supportedKeyNames, unsupportedPairs, recordProbe, declareModel,
    grantKey, pruneUnsupportedGrants, toChannelDetail } = require(__dirname + '/grants.cjs');

let pass = 0;
let fail = 0;
function check(name, actual, expected) {
    const ok = JSON.stringify(actual) === JSON.stringify(expected);
    if (ok) { pass++; console.log('PASS  ' + name + ' = ' + JSON.stringify(actual)); }
    else { fail++; console.log('FAIL  ' + name + ' got ' + JSON.stringify(actual) + ' want ' + JSON.stringify(expected)); }
}

// formState 给出裁剪与提交用得上的完整表单形状（提交会逐个 trim 字段，缺字段会抛错）。
function formState({ models, keyNames, grants, keyModels }) {
    return {
        name: 'ch', dialect: 'generic', base_url: 'https://up.example', enabled: true, proxy: false,
        openai_chat_completion_path: '/v1/chat/completions', openai_response_path: '/v1/responses',
        anthropic_message_path: '/v1/messages',
        keys: keyNames.map((name) => ({ name, key: 'sk-x', enabled: true })),
        models, grants, keyModels,
        custom_header: [], channel_proxy: '', param_override: '', match_regex: '',
        billing_mode: '', multiplier: 0, per_call_price: 0, monthly_quota: 0, monthly_used: 0,
    };
}

// 1) 没有探测记录的凭据一律放行：不拿"没探到"当"不支持"，否则未探测凭据会被一刀切掉。
check('isPairSupported 未知凭据放行', isPairSupported(new Map(), 'any-model', 'k1'), true);
check('supportedKeyNames 未知凭据不裁剪', supportedKeyNames(new Map(), 'any-model', ['k1', 'k2']), ['k1', 'k2']);

// 2) 探测结论按凭据隔离：model-a 只有 A 供得了，model-b 只有 B 供得了。
const idx = recordProbe(recordProbe(new Map(), 'A', ['model-a']), 'B', ['model-b']);
check('A 供 model-a', isPairSupported(idx, 'model-a', 'A'), true);
check('B 不供 model-a', isPairSupported(idx, 'model-a', 'B'), false);
check('A 不供 model-b', isPairSupported(idx, 'model-b', 'A'), false);
check('B 供 model-b', isPairSupported(idx, 'model-b', 'B'), true);

// 3) 全选只落在可用组合：批量勾选按 unsupportedPairs 跳过，分母也只数可用凭据。
check('不可用组合清单', unsupportedPairs(idx, ['model-a', 'model-b'], ['A', 'B']), [['model-a', 'B'], ['model-b', 'A']]);
check('model-a 的可用凭据', supportedKeyNames(idx, 'model-a', ['A', 'B']), ['A']);
check('全选后 model-a 只剩 A', unsupportedPairs(idx, ['model-a'], ['A', 'B']), [['model-a', 'B']]);

// 4) 保存前裁剪：模拟旧版全量置位留下的四条授权，其中两条无效。
const polluted = new Map([
    [grantKey('model-a', 'A'), 4],
    [grantKey('model-a', 'B'), 4],
    [grantKey('model-b', 'A'), 4],
    [grantKey('model-b', 'B'), 2],
]);
const pruned = pruneUnsupportedGrants(formState({
    models: ['model-a', 'model-b'], keyNames: ['A', 'B'], grants: polluted, keyModels: idx,
}));
check('裁剪条数', pruned.removed, 2);
check('裁剪后剩余授权', [...pruned.next.grants.keys()].sort(), [grantKey('model-a', 'A'), grantKey('model-b', 'B')].sort());
check('合法组合协议位未被改动', pruned.next.grants.get(grantKey('model-b', 'B')), 2);

// 5) 未探测凭据的授权不被裁：C 没探测过，它在 model-a 上的授权保留。
const withUnknown = new Map(polluted);
withUnknown.set(grantKey('model-a', 'C'), 1);
const keptUnknown = pruneUnsupportedGrants(formState({
    models: ['model-a', 'model-b'], keyNames: ['A', 'B', 'C'], grants: withUnknown, keyModels: idx,
}));
check('未知凭据的授权保留', keptUnknown.removed, 2);
check('未知凭据仍在授权表', keptUnknown.next.grants.get(grantKey('model-a', 'C')), 1);

// 6) 提交链路：裁剪后生成的载荷里不存在无效组合（这是"保存时不落无效授权"的直接证据）。
const payload = toChannelDetail(pruned.next, 7);
check('载荷里的授权', payload.grants, [
    { model_name: 'model-a', key_name: 'A', protocols: 4 },
    { model_name: 'model-b', key_name: 'B', protocols: 2 },
]);

// 7) 合法组合取消勾选后仍可重新勾选：判据只看探测结论，与当前授权无关。
const legalKey = grantKey('model-a', 'A');
const unticked = new Map(pruned.next.grants);
unticked.delete(legalKey);
check('取消后不再有该授权', unticked.has(legalKey), false);
check('取消后仍判可用', isPairSupported(idx, 'model-a', 'A'), true);
unticked.set(legalKey, 4);
check('重新勾选写回授权', unticked.get(legalKey), 4);

// 8) 探测结论以实测为准，且不会因一次空结果改判。
const reprobed = recordProbe(idx, 'A', ['model-a', 'model-c']);
check('新增实测模型可用', isPairSupported(reprobed, 'model-c', 'A'), true);
check('原实测模型保留', isPairSupported(reprobed, 'model-a', 'A'), true);
check('空探测结果不改判', recordProbe(reprobed, 'A', []) === reprobed, true);
check('探测结果去重去空', [...recordProbe(new Map(), 'A', ['m1', 'm1', '']).get('A')], ['m1']);
// 名字原样保留不裁剪空白：探到的名字要和表单里的模型名逐字相等才判为可用，裁剪会造成"探到了却判不支持"。
check('探测结果不裁剪空白', [...recordProbe(new Map(), 'A', [' m1 ']).get('A')], [' m1 ']);

// 9) 人工声明优先于探测结论：手填模型刚加上时不能被自己的实测结论锁死。
const declared = declareModel(idx, 'A', 'custom-x');
check('手填模型可用', isPairSupported(declared, 'custom-x', 'A'), true);
check('已支持模型声明无变化', declareModel(idx, 'A', 'model-a') === idx, true);
check('无记录凭据不因声明建立限制', declareModel(new Map(), 'B', 'x').size, 0);

console.log('\nGRANTS_CHECK pass=' + pass + ' fail=' + fail);
process.exit(fail === 0 ? 0 : 1);
