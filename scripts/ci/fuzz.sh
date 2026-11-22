#!/usr/bin/env bash
# NFR-SEC-6：逐個執行所有 func Fuzz*，不得 panic。
# 環境變數：
#   FUZZ_TOTAL_SECONDS  總預算（預設 600 秒＝10 分鐘），平均分給每個目標。
#   FUZZ_TIME           每個目標的 -fuzztime（例如 15s）；設定後覆寫上面的均分。
set -euo pipefail

TOTAL="${FUZZ_TOTAL_SECONDS:-600}"

# 收集 "<package 目錄>\t<目標名稱>"。
mapfile -t targets < <(
  grep -rEH --include='*_test.go' '^func Fuzz[A-Za-z0-9_]+\(' . |
    sed -E 's#^(.*)/[^/:]+:func (Fuzz[A-Za-z0-9_]+)\(.*#\1\t\2#' | sort -u
)
n="${#targets[@]}"
if [ "$n" -eq 0 ]; then
  echo "::error::找不到任何 Fuzz 目標"
  exit 1
fi

if [ -n "${FUZZ_TIME:-}" ]; then
  per="$FUZZ_TIME"
else
  per="$(( TOTAL / n ))s"
fi
echo "共 $n 個 fuzz 目標，每個 $per"

fail=0
for t in "${targets[@]}"; do
  dir="${t%%$'\t'*}"
  name="${t##*$'\t'}"
  echo "=== $dir $name"
  # 每個目標獨立執行；失敗時繼續跑完其他目標，最後一起回報。
  if ! go test "$dir" -run='^$' -fuzz="^${name}\$" -fuzztime="$per"; then
    echo "::error::$dir $name 失敗"
    fail=1
  fi
done
exit "$fail"
