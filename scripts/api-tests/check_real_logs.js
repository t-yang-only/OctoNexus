// 真实日志的等价性 + 可渲染性校验（NM-DS-008）。
// 1) 等价性：把同一条请求按"实时快照"形状喂进去，新解析（display.ts）与旧内联公式必须给出同样的数值；
// 2) 可渲染性：直接喂持久化历史行（扁平字段），卡片所需的数值同样齐备——旧代码只能渲染实时快照。
const fs = require('fs');
const { resolveLogDisplay } = require(__dirname + '/display.cjs');

// 与 web/src/lib/log-metrics.ts 同款的展示格式化（只用于打印，不参与数值比对）。
function pad(n) { return String(n).padStart(2, '0'); }
function msText(ms) { return ms < 1000 ? ms + 'ms' : (ms / 1000).toFixed(2) + 's'; }
function tpsText(tokens, ms) {
    if (tokens <= 0 || ms <= 0) return '- tk/s';
    const tps = tokens / (ms / 1000);
    return (tps >= 100 ? tps.toFixed(0) : tps >= 10 ? tps.toFixed(1) : tps.toFixed(2)) + ' tk/s';
}
function hitText(cached, total) {
    if (cached <= 0 || total <= 0) return '-';
    const rate = (cached / total) * 100;
    return (rate >= 100 ? '100' : rate >= 10 ? rate.toFixed(1) : rate.toFixed(2)) + '%';
}
// 旧内联公式（改动前 Item.tsx 的 LogMetrics 写法，实时快照形状）。
function legacy(overview) {
    const status = overview.status;
    const running = status === 'running' || status === 'committed';
    const startedMs = new Date(overview.started_at).getTime();
    const now = startedMs + overview.duration / 1e6;
    return {
        durationMs: running ? now - startedMs : overview.duration / 1e6,
        firstByteMs: overview.first_byte_at ? new Date(overview.first_byte_at).getTime() - startedMs : -1,
        cachedTokens: overview.usage.prompt_tokens_details?.cached_tokens ?? 0,
        promptTokens: overview.usage.prompt_tokens,
        completionTokens: overview.usage.completion_tokens,
    };
}

const rows = JSON.parse(fs.readFileSync(__dirname + '/real_logs.json', 'utf8'));
if (!rows.length) { console.log('没有真实日志行可校验'); process.exit(1); }

let pass = 0;
let fail = 0;
for (const row of rows) {
    const startedMs = new Date(row.started_at).getTime();
    const firstByteAt = row.first_byte_ms >= 0 ? new Date(startedMs + row.first_byte_ms).toISOString() : undefined;
    // 实时快照形状（SSE 会发的样子）
    const live = {
        id: row.request_id, status: row.status, started_at: row.started_at, first_byte_at: firstByteAt,
        duration: row.duration_ms * 1e6, model: row.model, protocol: row.target_protocol, group_id: row.group_id,
        api_key_name: row.api_key_name,
        usage: {
            prompt_tokens: row.prompt_tokens, completion_tokens: row.completion_tokens ?? row.completion_toks ?? 0,
            total_tokens: row.prompt_tokens + row.completion_tokens,
            prompt_tokens_details: { cached_tokens: row.cached_tokens },
        },
        cost: row.cost, round: 1, round_started_at: row.started_at, target_channel: row.target_channel,
        target_model: row.target_model, target_protocol: row.target_protocol, sending: false, error: row.error || undefined,
    };
    const nowMs = startedMs + row.duration_ms;
    const next = resolveLogDisplay(live, nowMs);
    const prev = legacy(live);
    const pairs = [
        ['durationMs', next.durationMs, Math.round(prev.durationMs)],
        ['firstByteMs', next.firstByteMs, prev.firstByteMs],
        ['promptTokens', next.promptTokens, prev.promptTokens],
        ['cachedTokens', next.cachedTokens, prev.cachedTokens],
        ['completionTokens', next.completionTokens, prev.completionTokens],
    ];
    console.log('--- 真实日志 #' + row.id + ' (' + row.model + ' → ' + row.target_model + ')');
    for (const [name, a, b] of pairs) {
        const ok = a === b;
        if (ok) pass++; else fail++;
        console.log((ok ? 'PASS  ' : 'FAIL  ') + '等价 ' + name + ' 新=' + a + ' 旧=' + b);
    }
    // 持久化历史行直接喂进去（旧代码渲染不了这种形状）
    const apiShaped = Object.assign({}, row, {
        completion_tokens: row.completion_tokens ?? row.completion_toks ?? 0,
    });
    const fromHistory = resolveLogDisplay(apiShaped, nowMs);
    const histPairs = [
        ['durationMs', fromHistory.durationMs, row.duration_ms],
        ['firstByteMs', fromHistory.firstByteMs, row.first_byte_ms >= 0 ? row.first_byte_ms : -1],
        ['promptTokens', fromHistory.promptTokens, row.prompt_tokens],
        ['completionTokens', fromHistory.completionTokens, row.completion_tokens ?? row.completion_toks ?? 0],
        ['cost', fromHistory.cost, row.cost],
        ['actualModel', fromHistory.actualModel, row.target_model || row.model],
        ['source', fromHistory.source, 'history'],
    ];
    for (const [name, a, b] of histPairs) {
        const ok = JSON.stringify(a) === JSON.stringify(b);
        if (ok) pass++; else fail++;
        console.log((ok ? 'PASS  ' : 'FAIL  ') + '历史行 ' + name + ' = ' + JSON.stringify(a));
    }
    console.log('      卡片显示: 耗时 ' + msText(next.durationMs) + ' | 首字 ' + msText(next.firstByteMs) +
        ' | TPS ' + tpsText(next.completionTokens, next.elapsedMs) +
        ' | 缓存命中 ' + hitText(next.cachedTokens, next.promptTokens) +
        ' | 费用 ' + next.cost);
}

console.log('\nREAL_LOG_CHECK pass=' + pass + ' fail=' + fail);
process.exit(fail === 0 ? 0 : 1);
