#!/usr/bin/env bash
#
# 產生某個 tag 的發布說明骨架（格式見 .github/release-notes-template.md）。
#
#   ./scripts/release-notes.sh v0.2.0
#   ./scripts/release-notes.sh v0.2.0 v0.1.0     # 指定前一個 tag
#
# 寫出 .tmp/release-notes-<tag>.md（可用 NOTES_OUT 改路徑）。摘要與 Highlights 留成 TODO；
# What's Changed 由兩個 tag 之間的 commit 產生：subject 結尾有 (#N) 的連到 PR，其他列出 SHA。
# 沒有前一個 tag（第一個版本）時不列 What's Changed，Full Changelog 連到該 tag 的 commit 歷史。
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

usage() { sed -n '3p;5,6p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; }

TAG="${1:-}"
PREV="${2:-}"
if [[ -z "$TAG" ]]; then usage; exit 2; fi

git rev-parse -q --verify "refs/tags/$TAG" >/dev/null \
  || { echo "error: 本機沒有 tag '$TAG'（先執行 git fetch --tags）" >&2; exit 1; }

if [[ -z "$PREV" ]]; then
  # 正式版和前一個正式版比；預發布版本和最近的任何一個 tag 比。
  exclude=()
  [[ "$TAG" == *-* ]] || exclude=(--exclude='*-*')
  PREV="$(git describe --tags --abbrev=0 --match='v[0-9]*' "${exclude[@]}" "$TAG^" 2>/dev/null || true)"
else
  git rev-parse -q --verify "refs/tags/$PREV" >/dev/null \
    || { echo "error: 本機沒有 tag '$PREV'" >&2; exit 1; }
fi

REPO="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
OUT="${NOTES_OUT:-.tmp/release-notes-$TAG.md}"
if [[ -e "$OUT" ]]; then
  echo "error: $OUT 已經存在；移開它或用 NOTES_OUT 指定別的路徑" >&2
  exit 1
fi
mkdir -p "$(dirname "$OUT")"

changes() {
  local sha subject pr line
  while IFS=$'\t' read -r sha subject; do
    [[ "$subject" == "🔖 release:"* ]] && continue
    if [[ "$subject" =~ \(#([0-9]+)\)$ ]]; then
      pr="${BASH_REMATCH[1]}"
      line="$(gh pr view "$pr" --repo "$REPO" --json title,author,url \
        --jq '"* \(.title) by @\(.author.login) in \(.url)"')"
      printf '%s\n' "$line"
    else
      printf '* %s (%s)\n' "$subject" "$sha"
    fi
  done < <(git log --no-merges --reverse --format='%h%x09%s' "$PREV..$TAG")
}

{
  cat <<'EOF'
> [!IMPORTANT]
> TODO：一段話說明這個版本是什麼、為什麼重要。
>
> TODO：現有安裝升級時要注意的事（migration、改變的預設值、升級前後要做的事）；沒有就刪掉這段。

## Highlights

* **TODO**：TODO

EOF
  if [[ -n "$PREV" ]]; then
    printf -- "---\n\n## What's Changed\n"
    changes
    printf '\n**Full Changelog**: https://github.com/%s/compare/%s...%s\n' "$REPO" "$PREV" "$TAG"
  else
    printf '**Full Changelog**: https://github.com/%s/commits/%s\n' "$REPO" "$TAG"
  fi
} > "$OUT"

echo "wrote $OUT (${PREV:-第一個版本} -> $TAG)"
echo "next: 填完 TODO（一個段落一行），再執行 ./scripts/release-notes-lint.sh $OUT"
