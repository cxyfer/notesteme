#!/usr/bin/env bash
# NFR-TEST-3：核心 package 的涵蓋率不得低於門檻（預設 80%）。
set -euo pipefail

THRESHOLD="${COVERAGE_THRESHOLD:-80}"
PACKAGES=(
  ./internal/subtitle
  ./internal/pipeline
  ./internal/queue
  ./internal/netguard
)

fail=0
for pkg in "${PACKAGES[@]}"; do
  out="$(go test -count=1 -cover "$pkg")" || { echo "$out"; echo "::error::$pkg 測試失敗"; exit 1; }
  echo "$out"
  pct="$(sed -n 's/.*coverage: \([0-9.]*\)% of statements.*/\1/p' <<<"$out" | head -n1)"
  if [ -z "$pct" ]; then
    echo "::error::$pkg 讀不到涵蓋率"
    fail=1
    continue
  fi
  if awk -v p="$pct" -v t="$THRESHOLD" 'BEGIN { exit !(p < t) }'; then
    echo "::error::$pkg 涵蓋率 ${pct}% 低於 ${THRESHOLD}%"
    fail=1
  else
    echo "OK  $pkg ${pct}% (>= ${THRESHOLD}%)"
  fi
done
exit "$fail"
