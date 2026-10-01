// display.ts 纯函数逻辑自检（NM-DS-008）：编译产物由 esbuild 生成，零依赖运行。
const { resolveLogDisplay, formatJsonForCopy } = require(__dirname + '/display.cjs');

let pass = 0;
let fail = 0;
function check(name, actual, expected) {
    const ok = JSON.stringify(actual) === JSON.stringify(expected);
    if (ok) { pass++; console.log('PASS  ' + name + ' = ' + JSON.stringify(actual)); }
    else { fail++; console.log('FAIL  ' + name + ' got ' + JSON.stringify(actual) + ' want ' + JSON.stringify(expected)); }
}

const now = Date.parse('2026-09-15T10:00:05.000Z');
const started = '2026-09-15T10:00:00.000Z';

// 1) 实时快照·进行中且中途缺字段：无首字、用量全 0、尚未选出成员
const running = resolveLogDisplay({
    id: 7, status: 'running', started_at: started, duration: 0, model: 'gpt-x', protocol: 2, group_id: 3,
    api_key_name: 'k1', usage: { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0, prompt_tokens_details: null },
    cost: 0, round: 1, round_started_at: started, target_channel: '', target_model: '', target_protocol: 0, sending: true,
}, now);
check('running.firstByteMs', running.firstByteMs, -1);
check('running.durationMs', running.durationMs, 5000);
check('running.elapsedMs', running.elapsedMs, 5000);
check('running.totalTokens', running.totalTokens, 0);
check('running.actualModel 回退到请求模型', running.actualModel, 'gpt-x');
check('running.source', running.source, 'live');

// 2) 实时快照·成功：纳秒耗时、首字节时间戳相减、总量缺失用输入+输出兜底
const done = resolveLogDisplay({
    id: 8, status: 'success', started_at: started, first_byte_at: '2026-09-15T10:00:00.800Z',
    duration: 2_500_000_000, model: 'gpt-x', protocol: 4, group_id: 3, api_key_name: 'k1',
    usage: { prompt_tokens: 1000, completion_tokens: 200, total_tokens: 0, prompt_tokens_details: { cached_tokens: 300, write_cached_tokens: 50 } },
    cost: 0.0123, round: 1, round_started_at: started, target_channel: 'ch', target_model: 'm1', target_protocol: 4, sending: false,
}, now);
check('done.durationMs（纳秒→毫秒）', done.durationMs, 2500);
check('done.firstByteMs（时间戳相减）', done.firstByteMs, 800);
check('done.totalTokens（兜底）', done.totalTokens, 1200);
check('done.cacheWriteTokens', done.cacheWriteTokens, 50);
check('done.elapsedMs（结束后用最终耗时）', done.elapsedMs, 2500);
check('done.actualModel', done.actualModel, 'm1');

// 3) 持久化历史行（扁平字段）：同一套解析照样可用
const history = resolveLogDisplay({
    id: 1, request_id: 42, status: 'success', model: 'gpt-x', group_id: 3, api_key_name: 'k1',
    target_channel: 'ch', target_model: 'm1', target_protocol: 2, started_at: started,
    first_byte_ms: -1, duration_ms: 1234, prompt_tokens: 1000, cached_tokens: 300,
    completion_tokens: 200, cost: 0.012, error: '',
});
check('history.requestId 取自 request_id', history.requestId, 42);
check('history.durationMs', history.durationMs, 1234);
check('history.firstByteMs（-1 表示未提交）', history.firstByteMs, -1);
check('history.totalTokens（扁平字段兜底）', history.totalTokens, 1200);
check('history.cachedTokens', history.cachedTokens, 300);
check('history.source', history.source, 'history');

// 4) 复制时格式化：能解析就重排，不能解析就原样去空白，绝不抛错
check('formatJsonForCopy(JSON)', formatJsonForCopy('{"b":2,"a":1}'), '{\n  "b": 2,\n  "a": 1\n}');
check('formatJsonForCopy(纯文本)', formatJsonForCopy('  oops  '), 'oops');
check('formatJsonForCopy(空)', formatJsonForCopy(undefined), '');
check('formatJsonForCopy(截断 JSON 不抛错)', formatJsonForCopy('{"a":'), '{"a":');

console.log('\nDISPLAY_CHECK pass=' + pass + ' fail=' + fail);
process.exit(fail === 0 ? 0 : 1);
