#!/usr/bin/env bash
# 客户端授权：激活、校验、离线宽限
# 依赖 common.sh

LICENSE_STATE="$VMBRA_STATE_DIR/license.env"

# ---------- 设备指纹 ----------
device_fingerprint() {
  local raw=""
  case "$(uname -s)" in
    Darwin)
      raw="$(ioreg -rd1 -c IOPlatformExpertDevice 2>/dev/null | awk -F'"' '/IOPlatformUUID/{print $4}')"
      ;;
    Linux)
      if [ -r /etc/machine-id ]; then
        raw="$(cat /etc/machine-id)"
      elif [ -r /var/lib/dbus/machine-id ]; then
        raw="$(cat /var/lib/dbus/machine-id)"
      fi
      ;;
  esac
  if [ -z "$raw" ]; then raw="$(hostname)-$(whoami)"; fi
  printf '%s' "$raw" | shasum -a 256 2>/dev/null | awk '{print $1}'
}

# ---------- JSON 取值（响应由本服务生成，格式稳定） ----------
json_get() {
  local json="$1" key="$2"
  printf '%s' "$json" | tr -d '\n' \
    | grep -o "\"$key\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" \
    | head -1 | sed 's/.*:[[:space:]]*"\(.*\)"$/\1/'
}

json_num() {
  local json="$1" key="$2"
  printf '%s' "$json" | tr -d '\n' \
    | grep -o "\"$key\"[[:space:]]*:[[:space:]]*[0-9][0-9]*" \
    | head -1 | sed 's/.*:[[:space:]]*//'
}

# 跨平台的时间戳格式化
fmt_epoch() {
  local epoch="$1"
  [ -z "$epoch" ] && return 1
  if [ "$(uname -s)" = "Darwin" ]; then
    date -r "$epoch" '+%Y-%m-%d' 2>/dev/null
  else
    date -d "@$epoch" '+%Y-%m-%d' 2>/dev/null
  fi
}

# ISO8601(UTC) -> epoch，兼容带毫秒/不带毫秒
iso_to_epoch() {
  local iso="$1"
  [ -z "$iso" ] && return 1
  local d="${iso%%.*}"
  d="${d%Z}"
  if [ "$(uname -s)" = "Darwin" ]; then
    date -j -u -f '%Y-%m-%dT%H:%M:%S' "$d" '+%s' 2>/dev/null
  else
    date -u -d "$d" '+%s' 2>/dev/null
  fi
}

# ---------- HTTP ----------
# 返回 "http_code\nbody"
http_post() {
  local url="$1" payload="$2" auth="${3:-}"
  local args=(-sS --max-time 15 -X POST "$url" -H 'Content-Type: application/json' -d "$payload" -w '\n%{http_code}')
  if [ -n "$auth" ]; then args+=(-H "Authorization: Bearer $auth"); fi
  curl "${args[@]}" 2>/dev/null
}

# ---------- 本地状态 ----------
state_read() {
  local key="$1"
  [ -f "$LICENSE_STATE" ] || return 1
  grep -E "^${key}=" "$LICENSE_STATE" 2>/dev/null | head -1 | cut -d= -f2-
}

state_write_all() {
  mkdir -p "$VMBRA_STATE_DIR"
  {
    printf 'token=%s\n' "$1"
    printf 'expires_at=%s\n' "$2"
    printf 'last_verified=%s\n' "$3"
    printf 'plan=%s\n' "$4"
    printf 'offline_grace_ms=%s\n' "$5"
    printf 'vps_host=%s\n' "${6:-}"
    printf 'vps_port=%s\n' "${7:-443}"
    printf 'key_hash=%s\n' "${8:-}"
  } > "$LICENSE_STATE"
  chmod 600 "$LICENSE_STATE" 2>/dev/null || true
}

# 授权码摘要，用于判断本地缓存是否属于当前输入的授权码
key_hash() {
  printf '%s' "$1" | shasum -a 256 2>/dev/null | awk '{print $1}'
}

# ---------- 激活 ----------
activate_license() {
  local license_key="$1" server="$2"
  local fp host os_desc app_version payload resp code body

  fp="$(device_fingerprint)"
  host="$(hostname)"
  os_desc="$(uname -s) $(uname -r) $(uname -m)"
  app_version="${VMBRA_APP_VERSION:-1.0.0}"

  payload="{\"licenseKey\":$(json_str "$license_key"),\"fingerprint\":$(json_str "$fp"),\"hostname\":$(json_str "$host"),\"os\":$(json_str "$os_desc"),\"appVersion\":$(json_str "$app_version")}"

  log "向授权中心申请激活：$server"
  resp="$(http_post "${server%/}/api/v1/activate" "$payload")" || true
  code="$(printf '%s' "$resp" | tail -1)"
  body="$(printf '%s' "$resp" | sed '$d')"

  if [ -z "$code" ] || [ "$code" = "000" ]; then
    # 网络不可达：允许离线宽限
    local grace_until
    grace_until="$(state_read grace_until)"
    if [ -n "$grace_until" ] && [ "$(date +%s)" -lt "$grace_until" ] && [ -n "$(state_read token)" ]; then
      warn "授权中心暂时不可达，进入离线宽限模式（截止 $(fmt_epoch "$grace_until" || printf '%s' "$grace_until")）"
      return 0
    fi
    return 1
  fi

  if [ "$code" != "200" ]; then
    local msg
    msg="$(json_get "$body" message)"
    [ -z "$msg" ] && msg="激活失败（HTTP ${code}）"
    FAIL_REASON="$msg"
    return 2
  fi

  local token expires plan grace_ms now epoch vps_host vps_port
  token="$(json_get "$body" token)"
  expires="$(json_get "$body" tokenExpiresAt)"
  plan="$(json_get "$body" plan)"
  grace_ms="$(json_num "$body" offlineGraceMs)"
  vps_host="$(json_get "$body" host)"
  vps_port="$(json_num "$body" port)"
  [ -z "$vps_port" ] && vps_port="443"
  now="$(date +%s)"
  epoch="$(iso_to_epoch "$expires")"
  [ -z "$epoch" ] && epoch="$(( now + 604800 ))"

  state_write_all "$token" "$epoch" "$now" "$plan" "${grace_ms:-259200000}" "$vps_host" "$vps_port" "$(key_hash "$license_key")"
  printf '%s\n' "$(( now + ${grace_ms:-259200000} / 1000 ))" > "$VMBRA_STATE_DIR/grace_until"
  log "激活成功，套餐：${plan:-lifetime}，token 有效期至 $(fmt_epoch "$epoch" || printf '%s' "$expires")"
  [ -n "$vps_host" ] && log "下发出口节点：$vps_host:$vps_port"
  return 0
}

# 已有缓存且未过期则复用，避免每次启动都打服务端
ensure_license() {
  local license_key="$1" server="$2"
  local token expires now last_verified cached_key
  token="$(state_read token)"
  expires="$(state_read expires_at)"
  last_verified="$(state_read last_verified)"
  cached_key="$(state_read key_hash)"
  now="$(date +%s)"

  # 换了授权码、或旧版本缓存缺少标识时，必须重新激活
  if [ -z "$cached_key" ] || [ "$cached_key" != "$(key_hash "$license_key")" ]; then
    if [ -n "$token" ]; then log "授权码变更或缓存过期，重新激活"; fi
    token=""
  fi

  if [ -n "$token" ] && [ -n "$expires" ] && [ "$now" -lt "$expires" ] &&
     [ -n "$last_verified" ] && [ $(( now - last_verified )) -lt 86400 ]; then
    log "复用本地授权（有效期至 $(fmt_epoch "$expires" || printf '%s' "$expires")）"
    return 0
  fi

  FAIL_REASON=""
  activate_license "$license_key" "$server"
  return $?
}
