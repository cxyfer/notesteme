"""從 vault 的真實筆記與 SRT 產生 mockup 資料（docs/ui/mockups/data.js）。"""
import json, re, sys, pathlib

VAULT = pathlib.Path("/mnt/d/Workspace/@Career/@Library/Video Notes")
OUT = pathlib.Path(sys.argv[1])

# (筆記路徑, 平台, 標題語言, 頻道顯示名, 模擬狀態)
ITEMS = [
    ("Channel/最佳拍檔/2026-11-11_NVIDIA 長上下文論文的邊界：壓縮可行，不等於模型不需要長上下文.md", "youtube", "zh-Hant", "最佳拍檔", {"kind": "transcribing", "done": 3, "total": 5, "source": "whisper-1"}),
    ("Channel/最佳拍檔/2026-11-10_Jeff Dean 對話 李飞飞：RSI 被過度炒作了嗎？.md", "youtube", "zh-Hant", "最佳拍檔", {"kind": "done"}),
    ("Channel/最佳拍檔/2026-11-09_Anthropic Agent Memory：Dreaming 如何讓 Agent 從經驗學習.md", "youtube", "zh-Hant", "最佳拍檔", {"kind": "done"}),
    ("Channel/码农高天/2026-11-07_你损失63亿美金，是因为员工去别的公司面试？.md", "youtube", "zh-Hans", "码农高天", {"kind": "awaiting_agent"}),
    ("Channel/码农高天/2026-10-17_【python】t-string详解，从实现到使用.md", "youtube", "zh-Hans", "码农高天", {"kind": "done"}),
    ("Channel/码农高天/2026-10-03_【Code Review】AI时代的CR还有意义么？一起看一个模板解析库.md", "youtube", "zh-Hans", "码农高天", {"kind": "done"}),
    ("Channel/原子能/2026-10-14_做好人类的最后一次代码交接.md", "youtube", "zh-Hans", "原子能", {"kind": "done"}),
    ("Channel/原子能/2026-05-09_再见了，所有的爬虫战士.md", "youtube", "zh-Hans", "原子能", {"kind": "done"}),
    ("Channel/原子能/2025-11-21_深度解读Cloudflare故障，互联网的根基有多脆弱？【让编程再次伟大#49】.md", "youtube", "zh-Hans", "原子能", {"kind": "failed", "chunk": 7, "total": 16}),
    ("Channel/原子能/2025-08-09_我们都只是在假装着做Agile【让编程再次伟大#43】.md", "youtube", "zh-Hans", "原子能", {"kind": "correcting", "done": 8, "total": 16}),
    ("YouTube/PostgreSQL in 100 Seconds - 2026-11-11.md", "youtube", "en", "Fireship", {"kind": "done", "translate": True}),
]
DETAIL = "Channel/原子能/2025-08-09_我们都只是在假装着做Agile【让编程再次伟大#43】.md"

def ts(s):
    h, m, rest = s.split(":")
    sec, ms = rest.split(",")
    return int(h) * 3600 + int(m) * 60 + int(sec) + int(ms) / 1000

def parse_srt(path):
    cues = []
    for block in re.split(r"\n\s*\n", path.read_text(encoding="utf-8-sig").strip()):
        lines = block.strip().splitlines()
        if len(lines) < 3 or "-->" not in lines[1]:
            continue
        a, b = [x.strip() for x in lines[1].split("-->")]
        cues.append({"s": round(ts(a), 2), "e": round(ts(b), 2), "t": " ".join(lines[2:]).strip()})
    return cues

def density(cues, duration, buckets):
    """每格的平均說話速度（字數／秒，依重疊時間加權），沉默會拉低數值。"""
    width = duration / buckets
    acc = [0.0] * buckets
    for c in cues:
        span = max(c["e"] - c["s"], 0.2)
        rate = len(c["t"]) / span
        i0, i1 = int(c["s"] / width), int(c["e"] / width)
        for i in range(max(i0, 0), min(i1, buckets - 1) + 1):
            lo, hi = max(c["s"], i * width), min(c["e"], (i + 1) * width)
            if hi > lo:
                acc[i] += rate * (hi - lo) / width
    peak = sorted(acc)[int(buckets * 0.95)] or 1
    return [round(min(v / peak, 1.0), 3) for v in acc]

def note_info(rel):
    p = VAULT / rel
    text = p.read_text(encoding="utf-8")
    fm = lambda k: (re.search(rf"^{k}:\s*\"?([^\"\n]+)\"?", text, re.M) or [None, None])[1]
    dur = re.search(r"\|\s*長度\s*\|[^|\n]*（([\d.]+)\s*秒）", text)
    source = fm("source") or ""
    vid = re.search(r"(?:youtu\.be/|v=)([\w-]{11})", source)
    chapters = [{"lv": len(m[1]), "title": m[2].strip(), "t": int(m[4])}
                for m in re.finditer(r"^(#{2,3}) (.+?) · \[(\d+:\d+)\]\([^)]*?t=(\d+)s?\)", text, re.M)]
    frames = []
    for m in re.finditer(r"!\[\[([^\]]+?\.(?:jpg|png))\]\]", text):
        t = re.search(r"(?:^| - )(\d{2})-(\d{2})(?: - |-)", m[1])
        if t:
            frames.append(int(t[1]) * 60 + int(t[2]))
    stem = p.stem
    srt = p.parent / "_Subtitles" / f"{stem}.srt"
    if not srt.exists():
        found = list((p.parent / "_Assets").glob(f"{stem.rsplit(' - ', 1)[0]}/*.srt"))
        srt = found[0] if found else None
    cues = parse_srt(srt) if srt else []
    title = re.sub(r"^\d{4}-\d{2}-\d{2}_", "", stem)
    title = re.sub(r" - \d{4}-\d{2}-\d{2}$", "", title)
    return {
        "title": title, "published": fm("video_published") or stem[:10], "duration": float(dur[1]) if dur else None,
        "youtubeId": vid[1] if vid else None, "chapters": chapters, "frames": sorted(set(frames)), "cues": cues,
    }

items = []
for rel, platform, lang, channel, state in ITEMS:
    info = note_info(rel)
    d = info["duration"] or (info["cues"][-1]["e"] if info["cues"] else 600)
    items.append({
        "id": pathlib.Path(rel).stem, "title": info["title"], "lang": lang, "channel": channel, "platform": platform,
        "published": info["published"], "duration": round(d, 1), "youtubeId": info["youtubeId"],
        "density": density(info["cues"], d, 120), "chapters": [c["t"] for c in info["chapters"] if c["lv"] == 2],
        "frames": info["frames"], "state": state,
    })

info = note_info(DETAIL)
d = info["duration"]
detail = {
    "id": pathlib.Path(DETAIL).stem, "title": info["title"], "lang": "zh-Hans", "channel": "原子能",
    "channelFull": "Peter Pang（原子能）", "platform": "youtube", "published": info["published"], "duration": d,
    "youtubeId": info["youtubeId"], "density": density(info["cues"], d, 360), "chapters": info["chapters"],
    "frames": info["frames"], "cueCount": len(info["cues"]),
    "cues": [c for c in info["cues"] if 352 <= c["s"] <= 452], "playhead": 384.7,
    "state": {"kind": "correcting", "done": 8, "total": 16},
}
OUT.write_text("// 由 docs/ui/mockups/tools/gen_data.py 從 vault 產生，內含真實字幕片段，未經確認前不要 commit。\n"
               f"window.NOTESTEME_MOCK = {json.dumps({'items': items, 'detail': detail}, ensure_ascii=False)};\n", encoding="utf-8")
for it in items:
    print(f"{it['channel']:6} {it['duration']:>7} frames={len(it['frames']):2} ch={len(it['chapters']):2} dens={sum(1 for v in it['density'] if v>0):3} {it['title'][:30]}")
print("detail cues", len(detail["cues"]), "chapters", len(detail["chapters"]), "frames", len(detail["frames"]))
