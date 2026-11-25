#!/usr/bin/env python3
"""把 internal/nfrbench 的 JSONL 報告（NOTESTEME_BENCH_REPORT）轉成 Markdown 表格。

用法：python3 -I scripts/ci/bench-summary.py <報告.jsonl> [標題] [--known-miss]
輸出到 stdout（bench.yml 附加到 $GITHUB_STEP_SUMMARY）。只負責呈現，不決定 job 成敗（由 go test 的結束碼決定）。
"""
import json
import sys


def fmt(v, unit):
    if unit == "ms":
        return f"{v:.1f} ms"
    if unit == "s":
        return f"{v:.2f} s"
    if unit == "MB":
        return f"{v:.1f} MB"
    if unit == "%":
        return f"{v:.3f} %"
    if unit == "x":
        return f"{v:.2f}×"
    return f"{v:g}"


def main():
    args = [a for a in sys.argv[1:] if not a.startswith("--")]
    known = "--known-miss" in sys.argv
    path = args[0]
    title = args[1] if len(args) > 1 else "NFR bench"
    rows = []
    try:
        with open(path, encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if line:
                    rows.append(json.loads(line))
    except FileNotFoundError:
        pass
    print(f"### {title}\n")
    if known:
        print("> **KNOWN-MISS**：以下項目已記錄為「已知未達標」（docs/specs/nfr.md 第 10 節），"
              "這個 job 失敗不影響其他 job；數字超標時仍照實列出。\n")
    if not rows:
        print("（沒有結果：bench 在記錄任何數字之前就失敗了，請看 log。）\n")
        return
    print("| 結果 | ID | 項目 | 指標 | 實測 | 門檻 | 資料 | 樣本 | 備註 |")
    print("|---|---|---|---|---|---|---|---|---|")
    for r in rows:
        threshold = r.get("threshold", 0)
        if threshold == 0 and r.get("pass"):
            verdict, th = "ℹ️", "—"
        else:
            verdict = "✅" if r.get("pass") else ("⚠️ KNOWN-MISS" if known else "❌")
            th = "< " + fmt(threshold, r.get("unit", ""))
        note = (r.get("note") or "").replace("|", "\\|")
        print(f"| {verdict} | {r['id']} | {r['case']} | {r['metric']} | {fmt(r['value'], r.get('unit', ''))} | {th} "
              f"| {r.get('data', '')} | {r.get('samples') or ''} | {note} |")
    print()


if __name__ == "__main__":
    main()
