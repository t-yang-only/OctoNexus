#!/usr/bin/env bash
# OctoNexus 一条命令升级（在服务器上执行）
#
# 设计口径（每一处都对应一次真实事故或坑）：
#   1. 先备份**整个 data 目录**（含 credential.key）；只备份 data.db 会在回滚时丢失解密能力，
#      因为 v0.14.0 起渠道凭据是密文落库、密钥在 credential.key 里。
#   2. 换二进制是「先装 .new → 校验版本 → 再原子 mv」，任一步失败都不动线上那份。
#   3. 启动后要过健康检查（管理面在听且拒匿名 401），不通过就回滚（给了 --rollback-on-fail 时自动）。
#   4. 回滚 = 二进制回滚 + **数据目录回滚**；只回滚二进制会让旧版读到密文后全线 401（旧版不认识 enc:v1:）。
#   5. 全程不打印任何凭据值（Key 只显示前 6 位）。
#
# 用法：
#   sudo bash upgrade-on-server.sh --package /root/octonexus-release [--dir /opt/octonexus] \
#        [--service octonexus] [--admin-port 8080] [--relay-port 8080] [--client-key sk-xxx --group my-group] \
#        [--rollback-on-fail] [--dry-run] [--yes]
#   回滚：
#   sudo bash upgrade-on-server.sh --dir /opt/octonexus --rollback            # 用最近一次备份回滚
#   sudo bash upgrade-on-server.sh --dir /opt/octonexus --rollback-artifact /opt/octonexus/backups/xxx
set -euo pipefail

DIR=/opt/octonexus
SERVICE=octonexus
PACKAGE=""
ADMIN_PORT=8080
RELAY_PORT=""
CLIENT_KEY=""
GROUP=""
ROLLBACK_ON_FAIL=0
DRY_RUN=0
ASSUME_YES=0
ACTION=upgrade
ROLLBACK_ARTIFACT=""
HEALTH_TIMEOUT=45
# 转发口缺省与管理口相同（单端口部属时的常见形态）；两个端口分开时显式传 --relay-port。
RELAY_PORT="${RELAY_PORT:-$ADMIN_PORT}"

while [ $# -gt 0 ]; do
    case "$1" in
        --dir) DIR="$2"; shift 2 ;;
        --service) SERVICE="$2"; shift 2 ;;
        --package) PACKAGE="$2"; shift 2 ;;
        --admin-port) ADMIN_PORT="$2"; shift 2 ;;
        --relay-port) RELAY_PORT="$2"; shift 2 ;;
        --client-key) CLIENT_KEY="$2"; shift 2 ;;
        --group) GROUP="$2"; shift 2 ;;
        --rollback-on-fail) ROLLBACK_ON_FAIL=1; shift ;;
        --dry-run) DRY_RUN=1; shift ;;
        --yes) ASSUME_YES=1; shift ;;
        --rollback) ACTION=rollback; shift ;;
        --rollback-artifact) ACTION=rollback; ROLLBACK_ARTIFACT="$2"; shift 2 ;;
        -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
        *) echo "未知参数: $1（用 --help 看用法）" >&2; exit 2 ;;
    esac
done

log()  { printf '%s\n' "$*"; }
ok()   { printf 'PASS  %s\n' "$*"; }
warn() { printf 'WARN  %s\n' "$*"; }
die()  { printf 'FAIL  %s\n' "$*" >&2; exit 1; }

# 取应用版本：必须锚定行首的 `Version:` —— 二进制 version 的输出里还有
# `Go Version: go1.27.0 linux/amd64` 这一行，用 /Version/ 会先命中它，
# 于是"版本"被显示成 Go 版本（本轮演练实测）。
version_of() {
    [ -x "$1" ] || return 0
    "$1" version 2>/dev/null | tr -d '\r' | awk -F': ' '/^Version:/{print $2; exit}' || true
}

mask_key() {
    if [ -z "${1:-}" ]; then printf '(未提供)'; else printf '%s…(%d 位)' "${1:0:6}" "${#1}"; fi
}

need_root() {
    if [ "$(id -u)" != "0" ]; then die "需要 root（用 sudo 执行）"; fi
}

# ---------- 服务管理抽象：systemd 优先，没有就退化成进程管理 ----------
SERVICE_MODE=""
detect_service_mode() {
    # 先取全量再判定，不要写成 `systemctl list-unit-files | grep -q`：
    # grep -q 命中即退出会让 systemctl 收到 SIGPIPE（退出码 141），
    # 而 set -o pipefail 会把整条管道判为失败 —— 结果是明明跑在 systemd 上
    # 却被判成 process 模式，于是绕过 systemctl 手工杀进程，
    # 被 Restart=on-failure 拉起的新旧实例抢端口（实测踩到）。
    local units=""
    if command -v systemctl >/dev/null 2>&1; then
        units=$(systemctl list-unit-files 2>/dev/null | awk '{print $1}')
    fi
    if printf '%s\n' "$units" | grep -qx "${SERVICE}.service"; then
        SERVICE_MODE=systemd
    elif pgrep -f "$DIR/octopus start" >/dev/null 2>&1; then
        SERVICE_MODE=process
    else
        SERVICE_MODE=none
    fi
    log "服务管理方式: $SERVICE_MODE"
}

stop_service() {
    case "$SERVICE_MODE" in
        systemd) systemctl stop "$SERVICE" ;;
        process|none) stop_manual_process ;;
    esac
}

# 按 /proc/<pid>/exe 反查进程，而不是按命令行字符串匹配：
# 手工启动时命令行是相对的（`cd /opt/octonexus && ./octopus start`），
# 用 "$DIR/octopus start" 去 pkill 会一个都匹配不到 —— 脚本会以为"已经停了"，
# 于是新实例和旧实例抢端口（本轮演练里就是这样挂住的）。
find_pids() {
    local pid exe
    for pid in $(pgrep -f 'octopus start' 2>/dev/null || true); do
        exe=$(readlink "/proc/$pid/exe" 2>/dev/null || true)
        case "$exe" in
            "$DIR/octopus"|"$DIR/octopus (deleted)") printf '%s\n' "$pid" ;;
        esac
    done
}

stop_manual_process() {
    local pids waited=0
    pids=$(find_pids || true)
    if [ -z "$pids" ]; then
        log "  没有正在运行的实例（手工模式）"
        return 0
    fi
    log "  停止进程: $(printf '%s' "$pids" | tr '\n' ' ')"
    # shellcheck disable=SC2086
    kill $pids 2>/dev/null || true
    while [ "$waited" -lt 20 ]; do
        [ -z "$(find_pids || true)" ] && break
        sleep 1
        waited=$((waited + 1))
    done
    local left
    left=$(find_pids || true)
    if [ -n "$left" ]; then
        warn "SIGTERM 后仍有进程未退出，强制结束: $(printf '%s' "$left" | tr '\n' ' ')"
        # shellcheck disable=SC2086
        kill -9 $left 2>/dev/null || true
        sleep 1
    fi
    ok "旧实例已停止"
}

start_service() {
    case "$SERVICE_MODE" in
        systemd)
            systemctl start "$SERVICE"
            ;;
        process|none)
            mkdir -p "$DIR/logs"
            LOGFILE="$DIR/logs/octopus-$(date +%Y%m%d-%H%M%S).log"
            # setsid 让实例脱离本脚本的会话：脚本退出/终端断开都不会带走它（宝塔、裸跑都适用）。
            if command -v setsid >/dev/null 2>&1; then
                ( cd "$DIR" && setsid ./octopus start >"$LOGFILE" 2>&1 < /dev/null & echo $! >"$DIR/octopus.pid" )
            else
                ( cd "$DIR" && nohup ./octopus start >"$LOGFILE" 2>&1 < /dev/null & echo $! >"$DIR/octopus.pid" )
            fi
            sleep 1
            ;;
    esac
}

# 取最近的启动日志内容（健康检查要看「加密已启用 / 补加密」这类启动行）
recent_log() {
    case "$SERVICE_MODE" in
        systemd) journalctl -u "$SERVICE" --since "-3min" --no-pager 2>/dev/null || true ;;
        *) if [ -n "${LOGFILE:-}" ] && [ -f "$LOGFILE" ]; then cat "$LOGFILE"; else
               ls -t "$DIR"/logs/*.log 2>/dev/null | head -1 | xargs -r cat || true
           fi ;;
    esac
}

health_check() {
    local waited=0
    while [ "$waited" -lt "$HEALTH_TIMEOUT" ]; do
        local code
        code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${ADMIN_PORT}/api/v1/balance/summary" || true)
        if [ "$code" = "401" ] || [ "$code" = "200" ]; then
            ok "管理面在监听且拒匿名（HTTP $code，等待 ${waited}s）"
            return 0
        fi
        sleep 2
        waited=$((waited + 2))
    done
    return 1
}

# ---------- 回滚 ----------
do_rollback() {
    need_root
    local artifact="${ROLLBACK_ARTIFACT}"
    if [ -z "$artifact" ]; then
        artifact=$(ls -dt "$DIR"/backups/* 2>/dev/null | head -1 || true)
    fi
    [ -n "$artifact" ] || die "找不到任何备份（$DIR/backups/ 为空）"
    local tarball binary
    tarball=$(ls -t "$artifact"/data-*.tar.gz 2>/dev/null | head -1 || true)
    binary=$(ls -t "$artifact"/octopus.old-* 2>/dev/null | head -1 || true)
    log "回滚自: $artifact"
    log "  数据备份: ${tarball:-（无）}"
    log "  旧二进制: ${binary:-（无）}"

    detect_service_mode
    stop_service
    if [ -n "$binary" ]; then
        install -m 0755 "$binary" "$DIR/octopus.rollback"
        mv "$DIR/octopus.rollback" "$DIR/octopus"
        ok "二进制已回滚（$(version_of "$DIR/octopus")）"
    else
        warn "备份里没有旧二进制，跳过二进制回滚"
    fi
    if [ -n "$tarball" ]; then
        # 整目录替换：只覆盖 data.db 是不够的 —— SQLite 的 data.db-wal 里可能还留着
        # 升级后写入的密文，回放后会覆盖掉刚恢复的明文（本轮演练实测到这条）。
        # 当前数据不删，整体挪进备份目录留档，方便事后取证。
        if [ -d "$DIR/data" ]; then
            mv "$DIR/data" "$artifact/data-before-rollback-$(date +%Y%m%d-%H%M%S)"
        fi
        tar -xzf "$tarball" -C "$DIR" || die "解包备份失败"
        ok "数据目录已整目录回滚（含 credential.key；回滚前的数据留在 $artifact/）"
    fi
    start_service
    health_check && ok "回滚完成" || die "回滚后健康检查未通过"
    log ""
    log "提醒：回滚到 v0.13.x 之前请确认库里凭据是明文；本次数据目录已一并回滚，"
    log "      若你后来又手工填过凭据，请重新填一遍。"
}

# ---------- 升级 ----------
do_upgrade() {
    [ -n "$PACKAGE" ] || die "缺少 --package（发布件目录，含 octopus-linux-amd64 与 SHA256SUMS.txt）"
    [ -d "$PACKAGE" ] || die "发布件目录不存在: $PACKAGE"
    local new_bin="$PACKAGE/octopus-linux-amd64"
    [ -f "$new_bin" ] || new_bin="$PACKAGE/octopus"
    [ -f "$new_bin" ] || die "发布件里找不到二进制（octopus-linux-amd64 / octopus）"

    log "== 1. 校验发布件 =="
    if [ -f "$PACKAGE/SHA256SUMS.txt" ]; then
        local want got
        want=$(awk '{print $1}' "$PACKAGE/SHA256SUMS.txt")
        got=$(sha256sum "$new_bin" | awk '{print $1}')
        [ "$want" = "$got" ] || die "校验和不匹配（期望 ${want:0:16}…，实际 ${got:0:16}…）"
        ok "sha256 与发布件一致（${got:0:16}…）"
    else
        warn "发布件里没有 SHA256SUMS.txt，跳过校验（不建议）"
    fi
    local new_version
    new_version=$(version_of "$new_bin")
    [ -n "$new_version" ] || die "新二进制跑不起来（version 子命令失败）"
    ok "新二进制版本 $new_version"

    log "== 2. 现状 =="
    detect_service_mode
    [ -d "$DIR" ] || die "安装目录不存在: $DIR"
    local old_version="-"
    [ -x "$DIR/octopus" ] && old_version=$(version_of "$DIR/octopus")
    log "  安装目录: $DIR"
    log "  现版本  : ${old_version:--}"
    log "  新版本  : $new_version"

    if [ "$DRY_RUN" = "1" ]; then
        log "== dry-run：到此为止，不动任何东西 =="
        return 0
    fi
    # 只有真正要落盘的路径才要求 root：先让任何人（含只想看一眼的运维）能跑 dry-run 预览。
    need_root
    if [ "$ASSUME_YES" != "1" ]; then
        printf '确认执行升级？[y/N] '
        read -r answer
        case "$answer" in y|Y|yes|YES) ;; *) die "已取消" ;; esac
    fi

    log "== 3. 备份（数据目录 + 现有二进制） =="
    local ts artifact
    ts=$(date +%Y%m%d-%H%M%S)
    artifact="$DIR/backups/$ts"
    mkdir -p "$artifact"
    # 排除运行时日志：它们是"文件在变"的根源，会让 tar 报
    # "file changed as we read it" 并返回非零，而 set -e 会因此中止整个升级
    # （实测卡在备份这步两次）。日志也不需要备份 —— 恢复时要的是数据不是历史日志。
    #
    # 仍额外容许 tar 的退出码 1：它的语义是"有文件在读取期间发生变化"，
    # 备份本身依旧可用（这是 tar 的固有行为，不是备份失败）。
    # 其余非零才是真失败，必须中止。
    if [ -d "$DIR/data" ]; then
        set +e
        tar -czf "$artifact/data-$ts.tar.gz" -C "$DIR" \
            --exclude='data/core/*.log' --exclude='data/logs' --exclude='data/*.log' data
        local tar_code=$?
        set -e
        if [ "$tar_code" -ne 0 ] && [ "$tar_code" -ne 1 ]; then
            die "备份数据目录失败（tar 退出码 $tar_code），已中止，线上未改动"
        fi
        if [ "$tar_code" -eq 1 ]; then
            warn "备份时有文件正在变化（tar 退出码 1）：备份仍可用，继续升级"
        fi
    fi
    [ -x "$DIR/octopus" ] && cp -a "$DIR/octopus" "$artifact/octopus.old-$ts"
    ok "备份到 $artifact（数据 ${ts}.tar.gz + 二进制 octopus.old-$ts）"

    log "== 4. 换二进制 =="
    stop_service
    install -m 0755 "$new_bin" "$DIR/octopus.new"
    # 先校验再落位：这一步失败，线上那份二进制一个字都没动。
    local staged_version
    staged_version=$(version_of "$DIR/octopus.new")
    [ "$staged_version" = "$new_version" ] || die "暂存二进制版本异常（${staged_version:-空}），已中止，线上未改动"
    mv -f "$DIR/octopus.new" "$DIR/octopus"
    ok "已替换为 $new_version（旧版在 $artifact/octopus.old-$ts）"

    log "== 5. 启动与健康检查 =="
    start_service
    if ! health_check; then
        warn "健康检查未通过"
        if [ "$ROLLBACK_ON_FAIL" = "1" ]; then
            warn "按 --rollback-on-fail 自动回滚"
            ROLLBACK_ARTIFACT="$artifact"
            do_rollback
        fi
        die "升级未通过健康检查；回滚命令：sudo bash $0 --dir $DIR --rollback-artifact $artifact"
    fi

    log "== 6. 启动日志要点 =="
    local logs
    logs=$(recent_log)
    if printf '%s' "$logs" | grep -q '渠道凭据静态加密已启用'; then
        ok "凭据静态加密已启用"
    else
        warn "日志里没看到「渠道凭据静态加密已启用」（可能是 0600 权限/日志级别问题，建议手工确认）"
    fi
    if printf '%s' "$logs" | grep -q '补加密'; then
        ok "存量明文凭据已补加密：$(printf '%s' "$logs" | grep -m1 '补加密' | tail -c 70)"
    fi

    log "== 7. 上线后自检（可选） =="
    if [ -f "$PACKAGE/verify.sh" ]; then
        if [ -n "$CLIENT_KEY" ] && [ -n "$GROUP" ]; then
            log "  用客户端 Key $(mask_key "$CLIENT_KEY") 与分组 $GROUP 跑真实转发自检"
            ( cd "$PACKAGE" && bash verify.sh "$CLIENT_KEY" "$GROUP" "$ADMIN_PORT" "$RELAY_PORT" "$DIR" "$SERVICE" ) || warn "自检未全绿（见上面 FAIL 行）"
        else
            log "  没给 --client-key/--group，跳过真实转发检查；手工执行：bash $PACKAGE/verify.sh <客户端Key> <分组名> $ADMIN_PORT $RELAY_PORT $DIR $SERVICE"
        fi
    fi

    log ""
    log "升级完成：${old_version:--} → $new_version"
    log "回滚命令：sudo bash $0 --dir $DIR --rollback-artifact $artifact"
    log "注意：v0.14.0 起凭据是密文落库，回滚到更早版本前必须一并回滚 data 目录（本脚本已包含）。"
}

case "$ACTION" in
    upgrade) do_upgrade ;;
    rollback) do_rollback ;;
esac
