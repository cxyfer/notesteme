import re, sys, pathlib
root = pathlib.Path(sys.argv[1])
steps_md = (root / "docs/specs/steps.md").read_text()
pats = []
for m in re.finditer(r"^\| `((?:Given|When|Then) [^`]+)` \|", steps_md, re.M):
    s = m.group(1)
    body = s.split(" ", 1)[1]
    rx = re.escape(body)
    rx = rx.replace(r"\{string\}", r'"[^"]*"').replace(r"\{int\}", r"-?\d+").replace(r"\{word\}", r"\S+")
    # 允許數量後的名詞單複數
    rx = re.sub(r"(items|cues|versions|tasks|requests|times|attempts|bytes|parts)", lambda mm: mm.group(1)[:-1] + "s?" if mm.group(1) != "times" else "times?", rx)
    rx = rx.replace("attempts?", "attempts?")
    pats.append((s, re.compile("^" + rx + "$")))
bad = 0; total = 0; used = set()
for f in sorted((root / "features").glob("*.feature")):
    lines = f.read_text().splitlines()
    tags_pending = []; manual = False; in_doc = False
    for i, line in enumerate(lines, 1):
        t = line.strip()
        if t.startswith('"""'):
            in_doc = not in_doc; continue
        if in_doc: continue
        if t.startswith("@"):
            tags_pending += t.split(); continue
        if t.startswith(("Scenario", "Background", "Feature", "Examples")):
            if t.startswith(("Scenario", "Background")):
                manual = "@manual" in tags_pending
            tags_pending = [] if not t.startswith("Examples") else tags_pending
            continue
        m = re.match(r"^(Given|When|Then|And|But) (.*)$", t)
        if not m or manual: continue
        text = m.group(2)
        # outline placeholder：以任意值代入
        probe = re.sub(r"<[a-z_]+>", "1", text)
        total += 1
        hit = [s for s, rx in pats if rx.match(probe)]
        if not hit:
            bad += 1; print(f"{f.name}:{i}: {text}")
        else:
            used.update(hit)
print(f"\n{total} steps checked, {bad} unmatched, {len(pats)} patterns, {len(pats)-len(used)} unused")
for s, _ in pats:
    if s not in used: print("  unused:", s)
