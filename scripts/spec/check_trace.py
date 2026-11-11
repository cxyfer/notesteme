import re, sys, pathlib
root = pathlib.Path(sys.argv[1])
defined_rules, defined_stories = set(), set()
for md in (root / "docs/specs").glob("*-*.md"):
    t = md.read_text()
    defined_rules |= set(re.findall(r"^\| ([A-Z]+-R\d+) \|", t, re.M))
    defined_stories |= set(re.findall(r"^\| ([A-Z]+-\d{2}) \|", t, re.M))
used = set(); problems = []
for f in (root / "features").glob("*.feature"):
    for i, line in enumerate(f.read_text().splitlines(), 1):
        if line.strip().startswith("@"):
            for tag in re.findall(r"@([A-Z]+-(?:R\d+|\d{2}))\b", line):
                used.add(tag)
                if tag not in defined_rules | defined_stories:
                    problems.append(f"{f.name}:{i}: unknown tag @{tag}")
print("\n".join(problems) or "no unknown tags")
missing_r = sorted(defined_rules - used, key=lambda x: (x.split('-')[0], int(x.rsplit('-R',1)[1])))
missing_s = sorted(defined_stories - used)
print("rules without scenario:", ", ".join(missing_r) or "none")
print("stories without scenario:", ", ".join(missing_s) or "none")
print(f"{len(defined_rules)} rules, {len(defined_stories)} stories")
