#!/usr/bin/env bash
#
# 檢查發布說明：不能留下 TODO，內文不能手動換行。
#
#   ./scripts/release-notes-lint.sh .tmp/release-notes-v0.1.0.md
#
# GitHub 把 release 內文的換行當成硬換行，所以每個段落、條列與引言只用一行原始碼。
# 檢查看結構：接續上一個段落、條列或引言的非空行就是錯誤；程式碼區塊、HTML 註解、
# 標題、表格、分隔線與原始 HTML 不檢查。換行檢查移植自 urlet 的 scripts/prose-lint.sh。
#
set -euo pipefail

if [[ $# -eq 0 ]]; then sed -n '3p;5p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 2; fi
for file in "$@"; do
    [[ -f "$file" ]] || { echo "error: $file not found" >&2; exit 1; }
done

if grep -n 'TODO' "$@"; then
    echo "error: 還有沒填完的 TODO" >&2
    exit 1
fi

exec awk '
function fail(previous, current) {
    printf "%s:%d: hard-wrapped prose (line %d continues here)\n", FILENAME, current, previous
    failed = 1
}
BEGIN { failed = 0 }
FNR == 1 { fence = 0; comment = 0; table = 0; prev = 0; last = "" }
{
    line = $0
    previous = last
    last = line

    if (line ~ /^[[:space:]]*(```|~~~)/) { fence = !fence; table = 0; prev = 0; next }
    if (fence) next

    if (comment) {
        if (line ~ /-->/) comment = 0
        prev = 0
        next
    }
    if (line ~ /^[[:space:]]*<!--/) {
        if (line !~ /-->/) comment = 1
        prev = 0
        next
    }

    if (line ~ /^[[:space:]]*$/) { table = 0; prev = 0; next }

    # Continue a GFM table; its rows may omit the outer pipes.
    if (table) {
        if (line ~ /\|/ && line !~ /^[[:space:]]*(#|>|<)/) { prev = 0; next }
        table = 0
    }

    # A GFM delimiter row can omit the outer pipes (`--- | ---`). The header
    # row above it (`Header | Header`) looks like prose, so pair them here.
    if (line ~ /^[[:space:]|:-]+$/ && line ~ /-/ && line ~ /\|/ && previous ~ /\|/) {
        table = 1
        prev = 0
        next
    }

    # Headings, rules, table rows and raw HTML open a block of their own.
    if (line ~ /^[[:space:]]*(#|[-*_][-*_][-*_][-*_]*[[:space:]]*$|[|]|<)/) { prev = 0; next }

    if (line ~ /^[[:space:]]*>/) {
        quote = line
        sub(/^[[:space:]]*>[[:space:]]?/, "", quote)
        if (quote == "" || quote ~ /^\[![A-Z]+\]/) { prev = 0; next }
        if (quote ~ /^([*+-]|[0-9]+[.)])[[:space:]]/) { prev = 2; next }
        if (prev == 2) fail(FNR - 1, FNR)
        prev = 2
        next
    }

    if (line ~ /^[[:space:]]*([*+-]|[0-9]+[.)])[[:space:]]/) { prev = 1; next }

    if (prev == 1) fail(FNR - 1, FNR)
    prev = 1
}
END { exit failed }
' "$@"
