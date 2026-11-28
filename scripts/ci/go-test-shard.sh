#!/usr/bin/env bash
# 把 go test -race 拆成多台 runner 平行執行：scripts/ci/go-test-shard.sh <shard> <total>（shard 從 0 開始）。
#
# 列出所有套件的頂層測試名稱（Test、Fuzz、Example），排序去重後依序號取模分給各 shard，
# 再以 -run '^(名稱|...)$' 執行。同名的測試不論在哪個套件都分到同一個 shard，所以每個測試剛好執行一次；
# 子測試跟著頂層測試走。列名稱與執行用同一組旗標（-race），測試 binary 只編譯一次。
#
# 驗收場景（features 套件本身）不在這裡：另由 bdd job 執行（見 ci.yml）。
set -euo pipefail

shard="${1:?用法：$0 <shard> <total>}"
total="${2:?用法：$0 <shard> <total>}"
if ! [[ "$shard" =~ ^[0-9]+$ && "$total" =~ ^[1-9][0-9]*$ ]] || ((shard >= total)); then
  echo "::error::shard 要是 0 到 total-1 的整數（收到 shard=$shard total=$total）" >&2
  exit 2
fi

flags=(-race -count=1)
mapfile -t pkgs < <(go list ./... | grep -vx 'github.com/cxyfer/notesteme/features')

# -list 的輸出是每行一個名稱，夾著每個套件的 "ok  <pkg>  0.01s"；只留 Test、Fuzz、Example 開頭、不含空白的名稱
#（Go 的識別符可以有非 ASCII 字母，所以不限定 [A-Za-z0-9_]）。
mapfile -t names < <(go test "${flags[@]}" -list '.*' "${pkgs[@]}" | grep -E '^(Test|Fuzz|Example)[^[:space:]]*$' | LC_ALL=C sort -u)
if ((${#names[@]} == 0)); then
  echo "::error::沒有列出任何測試" >&2
  exit 1
fi

mine=()
for i in "${!names[@]}"; do
  if ((i % total == shard)); then
    mine+=("${names[$i]}")
  fi
done
echo "shard $shard/$total：${#mine[@]} / ${#names[@]} 個頂層測試"
if ((${#mine[@]} == 0)); then
  exit 0
fi

regex="^($(IFS='|'; echo "${mine[*]}"))\$"
# -race 下 internal/pipeline 等套件仍要數分鐘；逾時以每個測試 binary 各自計算。
go test "${flags[@]}" -timeout 60m -run "$regex" "${pkgs[@]}"
