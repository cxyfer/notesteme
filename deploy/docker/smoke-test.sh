#!/usr/bin/env bash
# 對建好的 image 做冒煙測試：工具齊全、啟動檢查、健康檢查、登入、資料保存與正常關閉。
#
# 用法：deploy/docker/smoke-test.sh <image>
set -euo pipefail

IMAGE="${1:?用法：$0 <image>}"
NAME="notesteme-smoke-$$"
VOLUME="notesteme-smoke-$$"
PASSWORD="smoke-test-password"
# 32 bytes 的 base64 會直接作為金鑰，不必跑 argon2id 推導。
MASTER_KEY="$(head -c 32 /dev/urandom | base64)"

step() { printf '\n==> %s\n' "$*"; }
fail() {
  printf '\nFAIL: %s\n' "$*" >&2
  if docker inspect "$NAME" >/dev/null 2>&1; then
    echo '--- container logs ---' >&2
    docker logs "$NAME" >&2 || true
  fi
  exit 1
}
cleanup() {
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  docker volume rm -f "$VOLUME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

run_shell() { docker run --rm --entrypoint sh "$IMAGE" -c "$1"; }

step "版本"
out="$(docker run --rm "$IMAGE" --version)"
echo "$out"
[[ "$out" == notesteme\ * ]] || fail "--version 的輸出不正確：$out"

step "以非 root 使用者執行"
uid="$(docker run --rm --entrypoint id "$IMAGE" -u)"
[[ "$uid" != 0 ]] || fail "容器以 root 執行"

step "外部工具"
# 容器內的 sh 沒有 pipefail：先把輸出存進變數，讓工具本身失敗時 set -e 會中止，
# 不會被 pipeline 最後的 head 或 grep 蓋掉結束碼。
run_shell 'set -e
  first_line() { printf "%s\n" "$1" | head -n 1; }
  yt-dlp --version
  out="$(ffmpeg -hide_banner -version)"; first_line "$out"
  conf="$(ffmpeg -hide_banner -buildconf)"
  case "$conf" in *--enable-libass*) echo "ffmpeg: libass enabled" ;; *) echo "ffmpeg: libass missing" >&2; exit 1 ;; esac
  out="$(ffprobe -hide_banner -version)"; first_line "$out"
  out="$(deno --version)"; first_line "$out"' || fail "外部工具不齊全"

step "沒有 master key 時拒絕啟動"
set +e
out="$(docker run --rm "$IMAGE" 2>&1)"
code=$?
set -e
echo "$out"
[[ $code -ne 0 ]] || fail "沒有 NOTESTEME_MASTER_KEY 卻正常啟動"
grep -q 'NOTESTEME_MASTER_KEY' <<<"$out" || fail "錯誤訊息沒有提到 NOTESTEME_MASTER_KEY"

start() {
  docker run -d --name "$NAME" \
    -e NOTESTEME_MASTER_KEY="$MASTER_KEY" \
    "$@" \
    -v "$VOLUME":/data \
    -p 127.0.0.1::8080 \
    "$IMAGE" >/dev/null
}

wait_healthy() {
  local status
  for _ in $(seq 1 60); do
    status="$(docker inspect -f '{{.State.Health.Status}}' "$NAME" 2>/dev/null || echo missing)"
    case "$status" in
      healthy) echo "health: $status"; return 0 ;;
      unhealthy) fail "容器的 HEALTHCHECK 回報 unhealthy" ;;
    esac
    if [[ "$(docker inspect -f '{{.State.Running}}' "$NAME")" != true ]]; then
      fail "容器已經結束"
    fi
    sleep 2
  done
  fail "等待 healthy 逾時（最後狀態：$status）"
}

http_code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }

expect_code() {
  local want="$1" desc="$2"
  shift 2
  local got
  got="$(http_code "$@")"
  echo "$desc -> $got"
  [[ "$got" == "$want" ]] || fail "$desc：預期 HTTP $want，實際 $got"
}

login_checks() {
  local base="$1"
  expect_code 200 "GET /healthz" "$base/healthz"
  expect_code 200 "GET /readyz" "$base/readyz"
  expect_code 401 "未登入 GET /api/items" "$base/api/items"
  expect_code 401 "錯誤密碼登入" -X POST -H 'Content-Type: application/json' \
    -d '{"password":"wrong-password"}' "$base/api/auth/login"
  expect_code 200 "正確密碼登入" -X POST -H 'Content-Type: application/json' \
    -d "{\"password\":\"$PASSWORD\"}" "$base/api/auth/login"
}

step "第一次啟動（以 NOTESTEME_PASSWORD 設定密碼）"
start -e NOTESTEME_PASSWORD="$PASSWORD"
wait_healthy
BASE="http://$(docker port "$NAME" 8080/tcp | head -n 1)"
login_checks "$BASE"

step "SIGTERM 正常關閉"
docker stop -t 30 "$NAME" >/dev/null
exit_code="$(docker inspect -f '{{.State.ExitCode}}' "$NAME")"
echo "exit code: $exit_code"
[[ "$exit_code" == 0 ]] || fail "收到 SIGTERM 後的結束碼是 $exit_code"
docker rm "$NAME" >/dev/null

step "重新啟動（沿用 /data，不再提供密碼）"
start
wait_healthy
BASE="http://$(docker port "$NAME" 8080/tcp | head -n 1)"
login_checks "$BASE"

printf '\nOK: smoke test passed for %s\n' "$IMAGE"
