import sys, pathlib
from gherkin.parser import Parser
from gherkin.token_scanner import TokenScanner
ok = 0; n_sc = 0
for f in sorted(pathlib.Path(sys.argv[1]).glob("*.feature")):
    try:
        doc = Parser().parse(TokenScanner(f.read_text()))
        kids = doc["feature"]["children"]
        n_sc += sum(1 for k in kids if "scenario" in k)
        ok += 1
    except Exception as e:
        print(f.name, "->", str(e)[:300])
print(f"{ok} files parsed, {n_sc} scenarios/outlines")
