"""DS-TEST mock upstream for octopus relay format/timeout/failover tests.

Endpoints (all paths are what octopus channels will call):
  POST /v1/chat/completions   OpenAI Chat Completions
  POST /v1/responses          OpenAI Responses
  POST /v1/messages           Anthropic Messages
  GET  /v1/models             model list (for fetch-model / probe)

Behavior is driven by the upstream model name in the request body "model" field:
  contains "slow" -> sleep SLOW_SECONDS before answering (drives timeout switchover)
  contains "bad"  -> answer HTTP 500 immediately (drives failover)
  contains "stall"-> streaming only: send the first SSE frame and then go silent with the
                     connection still open, for STALL_SECONDS (drives the after-first-byte
                     "no progress" guard: the relay must end the response instead of hanging
                     until the client gives up)
  otherwise       -> answer normally
Streaming (body.stream == true) answers SSE in the same wire shape as the protocol.

Runtime override (drives proactive-probe tests, which must flip a model from
failing to healthy while the relay is running):
  POST /__control {"model": "mock-bad", "behavior": "ok"|"bad"|"slow"|"stall"}
  GET  /__control -> current overrides
An override wins over the name-derived behavior for that model.

Notification sinks (drives alert-delivery tests): POST /notify/echo, /notify/feishu,
/notify/dingtalk, /notify/wecom answer the provider-shaped success payload, and
/notify/feishu-fail answers the "HTTP 200 + business error code" shape. Every POST
body is appended to requests.jsonl like any other request.

Every request is appended to requests.jsonl with method/path/model/stream/headers
so tests can prove what the relay actually sent upstream (including protocol
conversion: an Anthropic inbound either arrives at /v1/messages or is converted
and arrives at /v1/chat/completions).
"""

import hashlib
import json
import os
import re
import select
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(os.environ.get("MOCK_PORT", "18099"))
SLOW_SECONDS = float(os.environ.get("MOCK_SLOW_SECONDS", "15"))
STALL_SECONDS = float(os.environ.get("MOCK_STALL_SECONDS", "120"))
LOG_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "requests.jsonl")
_lock = threading.Lock()

MODELS = ["mock-good", "mock-slow", "mock-bad", "mock-chatonly", "mock-stall",
          "mock-reject400", "mock-reject401"]

# FORCED 是运行期行为覆盖: 模型名 → "ok"/"bad"/"slow"。探活用例要证明"上游恢复后冷却被提前解除",
# 就需要在实例运行中把某个模型从失败翻成健康, 改模型名做不到 (成员模型名是落库配置)。
FORCED = {}  # 运行期覆盖: 模型名 -> ok|bad|slow
USAGE_SCALE = {}  # 运行期覆盖: 模型名 -> token 计数倍数（验证按 token 消耗选路）
USAGE_KEYS = {"input_tokens", "output_tokens", "prompt_tokens", "completion_tokens",
              "cached_tokens", "total_tokens"}


def _redact(value):
    """测试脚手架不持久化凭据: Authorization / x-api-key 只留类型与指纹, 便于区分用哪把凭据而不泄露内容。"""
    if not value:
        return None
    kind = "bearer:" if value.lower().startswith("bearer ") else "key:"
    return kind + hashlib.sha256(value.encode("utf-8")).hexdigest()[:12]


def redact_path(path):
    """路径里可能整段就是凭据（Server酱 的 /<SendKey>.send）—— 落盘前一律换成指纹。

    这里刻意做成 log_request 的唯一入口：调用方忘了脱敏也不会泄露，
    因为"凭据不进测试日志"这件事只能有一个实施点。
    """
    if not path:
        return path
    match = re.search(r"/([A-Za-z0-9_.\-]{12,})\.send(\?|$)", path)
    if not match:
        return path
    secret = match.group(1)
    masked = "%s…%s" % (secret[:4], hashlib.sha256(secret.encode("utf-8")).hexdigest()[:8])
    return path[:match.start(1)] + masked + ".send" + (match.group(2) if match.group(2) != "?" else "")


def log_request(entry):
    entry["ts"] = time.strftime("%H:%M:%S")
    if isinstance(entry.get("path"), str):
        entry["path"] = redact_path(entry["path"])
    with _lock:
        with open(LOG_PATH, "a", encoding="utf-8") as fh:
            fh.write(json.dumps(entry, ensure_ascii=False) + "\n")


def usage_echo(model):
    return {"input_tokens": 11, "output_tokens": 7, "total_tokens": 18}


def _scale_usage_payload(payload, scale):
    """把响应里出现的 token 计数按倍数放大, 用于验证"按 token 消耗选路"(TPM 口径)。

    只改已知的用量字段名, 递归处理 dict/list, 于是三种协议(含 SSE 事件)都覆盖到。
    """
    if scale == 1:
        return payload
    if isinstance(payload, dict):
        return {k: (int(v * scale) if k in USAGE_KEYS and isinstance(v, (int, float)) else _scale_usage_payload(v, scale))
                for k, v in payload.items()}
    if isinstance(payload, list):
        return [_scale_usage_payload(item, scale) for item in payload]
    return payload


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):  # keep stdout quiet
        pass

    def _read_body(self):
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length) if length else b""
        try:
            return json.loads(raw.decode("utf-8")) if raw else {}
        except Exception:
            return {"_raw": raw.decode("utf-8", "replace")}

    def _send_json(self, code, payload):
        body = json.dumps(_scale_usage_payload(payload, USAGE_SCALE.get(getattr(self, "_current_model", ""), 1))).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _send_sse(self, events):
        scale = USAGE_SCALE.get(getattr(self, "_current_model", ""), 1)
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.end_headers()
        if getattr(self, "_stall", False):
            # 模拟「上游吐完首帧就静默」: 只发第一个事件, 然后保持连接不关、不写 [DONE]、也不返回
            # （返回会关掉连接, 那就变成普通截断而不是静默）。relay 必须在「无进展上限」处主动结束
            # 这次响应, 而不是一直挂着, 等客户端自己放弃。
            if events:
                self.wfile.write(("data: " + json.dumps(_scale_usage_payload(events[0], scale)) + "\n\n").encode("utf-8"))
                self.wfile.flush()
            self._stall_watch_peer()
            return
        for ev in events:
            self.wfile.write(("data: " + json.dumps(_scale_usage_payload(ev, scale)) + "\n\n").encode("utf-8"))
            self.wfile.flush()
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()

    def _stall_watch_peer(self):
        """静默等待, 并记录对端(relay)何时关掉这条上游连接。

        只有盯着这条连接, 才能把「客户端走了就停」与「一直挂到无进展上限」区分开:
        事件行 {"event": "stall_peer_closed", "closed_after": 秒数或 null} 供套件断言,
        closed_after=null 表示等到 STALL_SECONDS 上限对端都没关（即后台空转）。
        事件行不带 method/model, 因此不会被各套件的「上游尝试次数」统计计入。
        """
        started = time.time()
        closed_after = None
        while time.time() - started < STALL_SECONDS:
            try:
                ready, _, _ = select.select([self.connection], [], [], 0.25)
            except (OSError, ValueError):
                ready = []
            if not ready:
                continue
            try:
                if not self.connection.recv(1):
                    closed_after = round(time.time() - started, 2)
                    break
            except OSError:
                closed_after = round(time.time() - started, 2)
                break
        log_request({"event": "stall_peer_closed", "closed_after": closed_after})

    def do_GET(self):
        if self.path.rstrip("/").endswith("/__control"):
            self._send_json(200, {"forced": dict(FORCED), "slow_seconds": SLOW_SECONDS})
            return
        if self.path.endswith("/models"):
            log_request({"method": "GET", "path": self.path, "model": None, "stream": None})
            self._send_json(200, {"object": "list", "data": [{"id": m, "object": "model"} for m in MODELS]})
            return
        if self.path.endswith("/api/user/self"):
            # New API 系的用户侧自查端点（只读）：号池的「探活」与「刷余额」都打这里。
            # 返回形状按 new-api 的口径（data.quota/used），额度解析层的别名匹配认这一份。
            log_request({"method": "GET", "path": self.path, "model": None, "stream": None,
                         "authorization": _redact(self.headers.get("Authorization"))})
            self._send_json(200, {"success": True, "message": "", "data": {"quota": 500, "used": 120}})
            return
        self._send_json(404, {"error": {"message": "not found: " + self.path}})

    def do_POST(self):
        body = self._read_body()
        model = body.get("model") or ""
        stream = bool(body.get("stream"))
        if self.path.rstrip("/").endswith("/__control"):
            behavior = body.get("behavior")
            if body.get("model") and behavior in ("ok", "bad", "slow", "stall"):
                FORCED[body["model"]] = behavior
            elif body.get("model") and behavior == "clear":
                FORCED.pop(body["model"], None)
            else:
                self._send_json(400, {"error": {"message": "want {model, behavior=ok|bad|slow|stall|clear}"}})
                return
            if body.get("model") and "usage_scale" in body:
                try:
                    scale = float(body["usage_scale"])
                except (TypeError, ValueError):
                    self._send_json(400, {"error": {"message": "usage_scale must be a number"}})
                    return
                if scale <= 1:
                    USAGE_SCALE.pop(body["model"], None)
                else:
                    USAGE_SCALE[body["model"]] = scale
            self._send_json(200, {"forced": dict(FORCED), "usage_scale": dict(USAGE_SCALE)})
            return
        self._current_model = model
        log_request({
            "method": "POST",
            "path": self.path,
            "model": model,
            "stream": stream,
            "body": body,
            "authorization": _redact(self.headers.get("Authorization")),
            "x_api_key": _redact(self.headers.get("x-api-key")),
            "anthropic_version": self.headers.get("anthropic-version"),
            "body_keys": sorted(body.keys()),
        })

        forced = FORCED.get(model)
        # stalling 由模型名或运行期覆盖决定: 只看流式分支（非流式请求没有「首帧之后再静默」这一说）。
        self._stall = forced == "stall" or (forced is None and "stall" in model)
        if forced == "slow" or (forced is None and "slow" in model):
            time.sleep(SLOW_SECONDS)
        if forced == "bad" or (forced is None and "bad" in model):
            self._send_json(500, {"error": {"message": "mock upstream forced failure", "type": "mock_error"}})
            return
        # 确定性错误桩 (T-retry-001): 名字里带 reject400 / reject401 的模型按对应状态码拒绝,
        # 用来验证「请求本身非法」与「成员自身问题」两类错误不再被反复重试。
        for status, marker in ((400, "reject400"), (401, "reject401")):
            if forced == marker or (forced is None and marker in model):
                self._send_json(status, {"error": {"message": "mock upstream rejected (%d)" % status,
                                                   "type": "mock_reject"}})
                return

        if self.path.endswith("/chat/completions"):
            return self._chat(model, stream)
        if self.path.endswith("/responses"):
            return self._responses(model, stream)
        if self.path.endswith("/messages"):
            return self._messages(model, stream)
        if self.path.endswith(".send"):
            return self._serverchan_sink()
        if "/notify/" in self.path:
            return self._notify_sink()
        self._send_json(404, {"error": {"message": "unknown path " + self.path}})

    # ---- Server酱(Turbo/³) 推送桩: 正文是表单, 应答里的 data.errno 才是真结论 ----
    # sendkey 里带 fail 时回一个业务错误, 用来验证"HTTP 200 也判失败"。
    def _serverchan_sink(self):
        if "fail" in self.path:
            return self._send_json(200, {"code": 0, "message": "",
                                         "data": {"errno": 1001, "error": "bad sendkey"}})
        return self._send_json(200, {"code": 0, "message": "",
                                     "data": {"pushid": "mock", "readkey": "mock",
                                              "errno": 0, "error": "SUCCESS"}})

    # ---- 通知渠道桩 (R-alert-001): 按各家协议返回成功/失败应答, 请求体已由 do_POST 落盘 ----
    def _notify_sink(self):
        if self.path.endswith("/notify/feishu"):
            return self._send_json(200, {"code": 0, "msg": "success"})
        if self.path.endswith("/notify/feishu-fail"):
            # 飞书式的"HTTP 200 包业务错误": 发送方必须看响应体里的码, 否则会误报成功。
            return self._send_json(200, {"code": 19024, "msg": "key not found"})
        if self.path.endswith("/notify/dingtalk"):
            return self._send_json(200, {"errcode": 0, "errmsg": "ok"})
        if self.path.endswith("/notify/wecom"):
            return self._send_json(200, {"errcode": 0, "errmsg": "ok"})
        return self._send_json(200, {"ok": True})

    # ---- OpenAI Chat Completions ----
    def _chat(self, model, stream):
        if stream:
            base = {"id": "chatcmpl-mock", "object": "chat.completion.chunk",
                    "created": int(time.time()), "model": model}
            self._send_sse([
                dict(base, choices=[{"index": 0, "delta": {"role": "assistant", "content": "mock "}, "finish_reason": None}]),
                dict(base, choices=[{"index": 0, "delta": {"content": "chat ok"}, "finish_reason": None}]),
                dict(base, choices=[{"index": 0, "delta": {}, "finish_reason": "stop"}],
                     usage={"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}),
            ])
            return
        self._send_json(200, {
            "id": "chatcmpl-mock", "object": "chat.completion", "created": int(time.time()), "model": model,
            "choices": [{"index": 0, "message": {"role": "assistant", "content": "mock chat ok"},
                         "finish_reason": "stop"}],
            "usage": {"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18},
        })

    # ---- OpenAI Responses ----
    def _responses(self, model, stream):
        if stream:
            base = {"type": "response.output_text.delta", "item_id": "msg_mock", "output_index": 0,
                    "content_index": 0, "delta": "mock responses ok"}
            events = [
                {"type": "response.created", "response": {"id": "resp_mock", "object": "response",
                                                          "status": "in_progress", "model": model, "output": []}},
                dict(base),
                {"type": "response.completed", "response": {"id": "resp_mock", "object": "response",
                                                            "status": "completed", "model": model,
                                                            "output": [{"type": "message", "role": "assistant",
                                                                        "content": [{"type": "output_text",
                                                                                     "text": "mock responses ok"}]}],
                                                            "usage": usage_echo(model)}},
            ]
            self._send_sse(events)
            return
        self._send_json(200, {
            "id": "resp_mock", "object": "response", "created_at": int(time.time()), "model": model,
            "status": "completed",
            "output": [{"type": "message", "id": "msg_mock", "role": "assistant", "status": "completed",
                        "content": [{"type": "output_text", "text": "mock responses ok", "annotations": []}]}],
            "usage": usage_echo(model),
        })

    # ---- Anthropic Messages ----
    def _messages(self, model, stream):
        if stream:
            self._send_sse([
                {"type": "message_start", "message": {"id": "msg_mock", "type": "message", "role": "assistant",
                                                      "model": model, "content": [], "stop_reason": None,
                                                      "usage": {"input_tokens": 11, "output_tokens": 0}}},
                {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}},
                {"type": "content_block_delta", "index": 0,
                 "delta": {"type": "text_delta", "text": "mock messages ok"}},
                {"type": "content_block_stop", "index": 0},
                {"type": "message_delta", "delta": {"stop_reason": "end_turn"}, "usage": {"output_tokens": 7}},
                {"type": "message_stop"},
            ])
            return
        self._send_json(200, {
            "id": "msg_mock", "type": "message", "role": "assistant", "model": model,
            "content": [{"type": "text", "text": "mock messages ok"}],
            "stop_reason": "end_turn", "stop_sequence": None,
            "usage": {"input_tokens": 11, "output_tokens": 7},
        })


if __name__ == "__main__":
    if os.path.exists(LOG_PATH):
        os.remove(LOG_PATH)
    srv = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
    print(f"mock upstream listening on 127.0.0.1:{PORT} slow={SLOW_SECONDS}s log={LOG_PATH}", flush=True)
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        sys.exit(0)
