"""Static checks for the per-model protocol probe feature (no Go toolchain needed).

Checks:
1. Backend: ChannelFetchModelRequest.Probe field + per-probe result types exist.
2. Backend: fetch-model handler keeps the non-probe path intact and adds the probe branch.
3. Backend: three probe senders cover chat / response / message protocol bits.
4. Backend: probes are only marked OK on 2xx + JSON-decodable body.
5. Frontend: FetchModelRequest carries probe, ProtocolProbe/FetchModel shapes match backend JSON.
6. Frontend: probe() always sends probe=true and ORs tested bits into grants (never downgrades).
7. i18n: modelRefreshProbed exists in en / zh_hans / zh_hant.
8. Balance heuristic on all edited files.
"""
from __future__ import annotations

import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
failures: list[str] = []


def check(cond: bool, msg: str) -> None:
    print(("PASS " if cond else "FAIL ") + msg)
    if not cond:
        failures.append(msg)


def read(p: Path) -> str:
    return p.read_text(encoding="utf-8")


def balanced(text: str) -> bool:
    stack: list[str] = []
    pairs = {")": "(", "}": "{", "]": "["}
    in_str = False
    esc = False
    str_ch = ""
    in_line_comment = False
    in_block_comment = False
    i = 0
    while i < len(text):
        ch = text[i]
        nxt = text[i + 1] if i + 1 < len(text) else ""
        if in_line_comment:
            if ch == "\n":
                in_line_comment = False
            i += 1
            continue
        if in_block_comment:
            if ch == "*" and nxt == "/":
                in_block_comment = False
                i += 2
                continue
            i += 1
            continue
        if in_str:
            if esc:
                esc = False
            elif ch == "\\":
                esc = True
            elif ch == str_ch:
                in_str = False
            i += 1
            continue
        if ch == "/" and nxt == "/":
            in_line_comment = True
            i += 2
            continue
        if ch == "/" and nxt == "*":
            in_block_comment = True
            i += 2
            continue
        if ch in ("\"", "'", "`"):
            in_str = True
            str_ch = ch
            i += 1
            continue
        if ch in "({[":
            stack.append(ch)
        elif ch in ")}]":
            if not stack or stack.pop() != pairs[ch]:
                return False
        i += 1
    return not stack and not in_str and not in_block_comment


handler_src = read(ROOT / "internal" / "server" / "handlers" / "channel.go")
model_src = read(ROOT / "internal" / "model" / "channel.go")
api_src = read(ROOT / "web" / "src" / "api" / "channel.ts")
probe_src = read(ROOT / "web" / "src" / "components" / "modules" / "channel" / "probe.ts")

check("Probe   bool          `json:\"probe\"`" in model_src,
      "model.go: ChannelFetchModelRequest has Probe bool field")
check("type ChannelProtocolProbe struct" in model_src and "OK       bool     `json:\"ok\"`" in model_src,
      "model.go: ChannelProtocolProbe carries ok/status/error")
check("Probes    []ChannelProtocolProbe `json:\"probes,omitempty\"`" in model_src,
      "model.go: ChannelFetchModel carries per-protocol probe details")

check("if !request.Probe {" in handler_src, "handler.go: non-probe path returns /models-derived bits unchanged")
check("func probeModels(httpClient *http.Client, ctx context.Context, target model.ChannelConfig, key string, order []string) []model.ChannelFetchModel" in handler_src,
      "handler.go: probeModels fans out per model over all three protocols")
check("probeSingleModel(httpClient, ctx, target, key, name)" in handler_src,
      "handler.go: protocols mask derived from live probe conclusions")
check("probeOpenAIChatCompletion" in handler_src and "probeOpenAIResponse" in handler_src and "probeAnthropicMessage" in handler_src,
      "handler.go: three probe senders cover chat/response/message")
check("model.ProtocolOpenAIChatCompletion, probeOpenAIChatCompletion" in handler_src,
      "handler.go: chat bit maps to chat sender")
check("candidates&definition.bit == 0" not in handler_src,
      "handler.go: probe no longer gated by /models side (chat can auto-enable)")
check('response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices' in handler_src
      and "non-json body" in handler_src,
      "handler.go: probe OK requires 2xx + JSON body")
check("probeModelTimeout = 20" in handler_src, "handler.go: single probe capped at 20s")
check("sem := make(chan struct{}, 4)" in handler_src, "handler.go: probe concurrency limited to 4")
check('"max_output_tokens": 16' in handler_src, "handler.go: responses probe uses min legal 16 tokens")
check('strings.HasPrefix(head, "{")' in handler_src, "handler.go: 2xx must also yield JSON-looking body (anti 200-HTML)")

check("probe?: boolean;" in api_src, "api.ts: FetchModelRequest carries probe flag")
check("export type ProtocolProbe = {" in api_src and "protocol: number;" in api_src and "ok: boolean;" in api_src,
      "api.ts: ProtocolProbe shape matches backend JSON")
check("probes?: ProtocolProbe[]" in api_src, "api.ts: FetchModel carries optional probes details")

check("probe: true," in probe_src, "probe.ts: fetch always sends probe=true")
check("(grants.get(mapKey) ?? 0) | protocols" in probe_src, "probe.ts: grants OR tested bits (no downgrade)")
check("modelRefreshProbed" in probe_src, "probe.ts: success toast uses probed message")

for locale in ("en", "zh_hans", "zh_hant"):
    src = read(ROOT / "web" / "src" / "locales" / f"{locale}.json")
    check("modelRefreshProbed" in src, f"i18n {locale}: modelRefreshProbed key present")

for path, src in [
    ("internal/server/handlers/channel.go", handler_src),
    ("internal/model/channel.go", model_src),
    ("web/src/api/channel.ts", api_src),
    ("web/src/components/modules/channel/probe.ts", probe_src),
]:
    check(balanced(src), f"{path} braces/parens/strings/comments balanced")

if failures:
    print(f"\n{len(failures)} check(s) failed")
    sys.exit(1)
print("\nAll static checks passed")
