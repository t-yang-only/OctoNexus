#!/bin/bash
# 上线后自检：校验二进制、版本、鉴权、一条真实转发与库内凭据形态。
# 用法（放在 /opt/octonexus 下，与二进制同级）：
#   bash verify.sh [客户端Key] [分组名] [admin端口] [relay端口] [安装目录]
# 例：bash verify.sh sk-xxxx my-group 3303 1234 /opt/octonexus
# 说明：第 5 个参数是「安装目录」。缺省时自检按发布件目录走（把发布件直接当安装目录用）。
#       升级脚本会在发布件目录里调它、并把安装目录传进来 —— 否则库内形态与日志检查
#       会去看发布件目录里的 data/ 与 logs/（那里没有），于是这两项常年被"跳过"。
set -u
KEY="${1:-}"
MODEL="${2:-}"
ADMIN="${3:-3303}"
RELAY="${4:-1234}"
DIR="$(cd "$(dirname "$0")" && pwd)"
INSTALL="${5:-$DIR}"
BIN=""
# 优先看安装目录里正在跑的那份（升级后要证明"线上就是发布件"），再退回发布件目录。
for cand in "$INSTALL/octopus" "$DIR/octopus" "$DIR/octopus-linux-amd64"; do
  [ -x "$cand" ] && BIN="$cand" && break
done
fail=0
say() { printf '%s  %s\n' "$1" "$2"; }

echo "== 1. 二进制与校验和 =="
if [ -z "$BIN" ]; then
  say FAIL "没找到可执行的二进制（期望 $DIR/octopus）"; fail=1
else
  say INFO "二进制：$BIN"
  if [ -f "$DIR/SHA256SUMS.txt" ]; then
    want=$(awk '{print $1; exit}' "$DIR/SHA256SUMS.txt")
    got=$(sha256sum "$BIN" | awk '{print $1}')
    if [ "$want" = "$got" ]; then
      say PASS "sha256 与发布件一致（文件名改过也照样比对内容）"
    else
      say FAIL "sha256 不一致：期望 ${want:0:16}… 实际 ${got:0:16}…（线上不是这份发布件）"; fail=1
    fi
  else
    say INFO "没有 SHA256SUMS.txt，跳过校验"
  fi
fi

echo "== 2. 版本 =="
if [ -n "$BIN" ]; then
  ver=$("$BIN" version 2>/dev/null | tr -d '\r')
  printf '%s\n' "$ver" | grep -q "v0.14.0" && say PASS "二进制版本 v0.14.0" || { say FAIL "二进制版本不是 v0.14.0（当前：$(printf '%s' "$ver" | tr '\n' ' ')）"; fail=1; }
fi

echo "== 3. 运行中的实例 =="
if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet octonexus; then
  say PASS "systemd 单元 octonexus 正在运行"
  log=$(journalctl -u octonexus -n 300 --no-pager 2>/dev/null || true)
else
  log=$(ls -t "$INSTALL"/logs/*.log "$INSTALL"/*.log 2>/dev/null | head -1 | xargs -r tail -300 || true)
  [ -n "$log" ] && say INFO "没有 systemd 单元，改读安装目录下的日志文件" || say INFO "没有 systemd 单元也没有日志文件，跳过日志检查"
fi
if [ -n "$log" ]; then
  printf '%s\n' "$log" | grep -q "渠道凭据静态加密已启用" \
    && say PASS "凭据静态加密已启用" \
    || { say FAIL "凭据加密未启用（凭据会明文落库，检查 OCTOPUS_OFFICIAL_KEY 或数据目录是否可写）"; fail=1; }
  printf '%s\n' "$log" | grep -q "渠道凭据加密：" && say INFO "本次启动已加密存量明文凭据" || say INFO "本次启动没有存量明文凭据需要加密（正常）"
fi

anon=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$ADMIN/api/v1/channel/stats" || echo 000)
[ "$anon" = "401" ] && say PASS "管理面在监听且拒匿名（401）" || { say FAIL "管理面响应 $anon（端口写错？服务没起？）"; fail=1; }

echo "== 4. 真实转发 =="
if [ -z "$KEY" ] || [ -z "$MODEL" ]; then
  say INFO "未提供 <客户端Key> <分组名>，跳过真实转发检查"
else
  code=$(curl -s -o /tmp/verify-relay.json -w '%{http_code}' -X POST \
    -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
    -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"ping\"}],\"max_tokens\":16}" \
    "http://127.0.0.1:$RELAY/v1/chat/completions" || echo 000)
  [ "$code" = "200" ] && say PASS "转发返回 200（模型 $MODEL）" \
    || { say FAIL "转发返回 $code（面板日志看「上游轮次」定位是哪个成员失败）"; fail=1; }
fi

echo "== 5. 库内凭据形态 =="
DB="$INSTALL/data/data.db"
if [ -f "$DB" ] && command -v sqlite3 >/dev/null 2>&1; then
  rows=$(sqlite3 "$DB" "select count(*) from channel_keys where key is not null and key <> ''")
  sealed=$(sqlite3 "$DB" "select count(*) from channel_keys where key like 'enc:v1:%'")
  if [ "$rows" = "$sealed" ]; then say PASS "库内凭据全部为密文（$sealed/$rows）"; else say FAIL "仍有明文凭据（$sealed/$rows）"; fail=1; fi
elif [ -f "$DB" ] && command -v python3 >/dev/null 2>&1; then
  # 没有 sqlite3 命令就用 python3 的 sqlite3 模块（Ubuntu 默认自带），避免这条检查常年被跳过。
  out=$(python3 - "$DB" <<'PY'
import sqlite3, sys
conn = sqlite3.connect(sys.argv[1])
rows = conn.execute("select count(*) from channel_keys where key is not null and key <> ''").fetchone()[0]
sealed = conn.execute("select count(*) from channel_keys where key like 'enc:v1:%'").fetchone()[0]
conn.close()
print("%d %d" % (rows, sealed))
PY
)
  rows=${out%% *}; sealed=${out##* }
  if [ "$rows" = "$sealed" ]; then say PASS "库内凭据全部为密文（$sealed/$rows）"; else say FAIL "仍有明文凭据（$sealed/$rows）"; fail=1; fi
else
  say INFO "找不到 $DB 或没有 sqlite3/python3，跳过库内形态检查"
fi

echo
if [ "$fail" = "0" ]; then echo "自检通过 ✅"; else echo "自检发现失败项 ❌（见上面 FAIL 行）"; fi
exit "$fail"
