#!/usr/bin/env bash
# 對建好的 Hub image 做冒煙測試：以 compose.hub.yaml 啟動整組服務（Postgres、SeaweedFS、migrate、hub），
# 確認版本、授權文件、非 root、migration、/readyz、doctor、Hub UI 與 API。
#
# 用法：deploy/docker/smoke-test-hub.sh <image> [預期版本]
set -euo pipefail

IMAGE="${1:?用法：$0 <image> [預期版本]}"
VERSION="${2:-}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT="notesteme-hub-smoke-$$"
# 在暫存目錄執行：不覆寫本機的 .env.hub 與 hub-config/。
WORK="$(mktemp -d)"

step() { printf '\n==> %s\n' "$*"; }
hub() {
  docker compose -p "$PROJECT" -f "$WORK/compose.hub.yaml" --env-file "$WORK/.env.hub" --profile hub "$@"
}
fail() {
  printf '\nFAIL: %s\n' "$*" >&2
  if [[ -f "$WORK/.env.hub" ]]; then
    echo '--- compose logs ---' >&2
    hub logs --no-color --tail 100 >&2 || true
  fi
  exit 1
}
cleanup() {
  if [[ -f "$WORK/.env.hub" ]]; then
    hub down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

step "版本"
out="$(docker run --rm "$IMAGE" --version)"
echo "$out"
[[ "$out" == notesteme-hub\ * ]] || fail "--version 的輸出不正確：$out"
if [[ -n "$VERSION" ]]; then
  [[ "$out" == "notesteme-hub $VERSION" ]] || fail "版本應該是 $VERSION，實際：$out"
fi

step "授權文件與非 root"
docker run --rm --entrypoint sh "$IMAGE" -c 'test -s /usr/share/doc/notesteme/LICENSE' || fail "image 缺少 LICENSE"
uid="$(docker run --rm --entrypoint id "$IMAGE" -u)"
[[ "$uid" != 0 ]] || fail "容器以 root 執行"

step "啟動整組服務"
cp "$HERE/compose.hub.yaml" "$WORK/"
mkdir "$WORK/hub-config"
# 設定檔維持一般權限，容器內的非 root 使用者才讀得到。
chmod 0755 "$WORK" "$WORK/hub-config"
cp "$HERE/hub-config.example.yaml" "$WORK/hub-config/notesteme-hub.yaml"
# doctor 要求至少啟用一個登入方式：填入假的 client_id（只啟用設定，不會連到 GitHub）。
sed -i 's/github: { client_id: "" }/github: { client_id: "smoke-client-id" }/' "$WORK/hub-config/notesteme-hub.yaml"
grep -q 'client_id: "smoke-client-id"' "$WORK/hub-config/notesteme-hub.yaml" || fail "無法在範例設定檔填入 client_id"
chmod 0644 "$WORK/hub-config/notesteme-hub.yaml"
secret() { head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n'; }
(
  umask 077
  {
    echo "NOTESTEME_HUB_IMAGE=$IMAGE"
    # 由 Docker 挑一個空的 port，不和本機正在執行的 Hub 衝突。
    echo "NOTESTEME_HUB_PORT=0"
    echo "NOTESTEME_HUB_MASTER_KEY=$(secret)"
    echo "NOTESTEME_HUB_SESSION_KEY=$(secret)"
    echo "NOTESTEME_HUB_POSTGRES_PASSWORD=$(secret)"
    echo "NOTESTEME_HUB_S3_ACCESS_KEY_ID=notestemehub"
    echo "NOTESTEME_HUB_S3_SECRET_ACCESS_KEY=$(secret)"
    echo "NOTESTEME_HUB_OAUTH_GITHUB_CLIENT_SECRET=smoke-client-secret"
  } > "$WORK/.env.hub"
)
# --no-build：測的必須是傳進來的 image，不能退回成從原始碼建置。
hub up -d --no-build || fail "compose up 失敗"
BASE="http://$(hub port hub 8090)"
curl --retry 60 --retry-delay 2 --retry-all-errors -fsS "$BASE/readyz" || fail "/readyz 沒有就緒"
echo
actual="$(docker inspect -f '{{.Config.Image}}' "$(hub ps -q hub)")"
[[ "$actual" == "$IMAGE" ]] || fail "執行中的 image 是 $actual，不是 $IMAGE"

step "doctor"
hub run -T --rm hub doctor || fail "doctor 沒有通過"

step "Hub UI 與授權聲明"
page="$(curl -fsS "$BASE/")"
grep -q 'src="/assets/index-' <<<"$page" || fail "首頁不是建置出來的 Hub UI"
notices="$(curl -fsS "$BASE/third-party-notices.txt")"
grep -q 'SIL Open Font License' <<<"$notices" || fail "缺少字型的授權聲明"

printf '\nOK: smoke test passed for %s\n' "$IMAGE"
