#!/usr/bin/env bash
# 更新內嵌的 OpenCC 字典（ADR-0018）。只給維護者手動執行；CI 與測試都不連網（NFR-TEST-1）。
#
# 用法：scripts/opencc/update.sh <tag>      例如 ver.1.4.2
#
# 做的事：
#   1. 下載上游 release 的 opencc-<版本>-resources.zip 到全新的暫存目錄（不在 repo 內解壓縮）。
#   2. 讀取 zip 內的 manifest.json，檢查需要的檔案的 sha256。
#   3. 只複製需要的 10 份設定與 17 份字典（原封不動）到 internal/zhconv/opencc/，
#      並寫入 manifest.json（只保留這些檔案的條目）、LICENSE、VERSION。
# 做完後：更新 zhconv.DataVersion、NOTICE.md 的來源說明與 zip sha256，從上游同一個 tag 取得
# test/testcases/testcases.json 放到 internal/zhconv/testdata/，再執行 go test ./internal/zhconv。
set -euo pipefail

tag="${1:?用法：$0 <tag>，例如 ver.1.4.2}"
version="${tag#ver.}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
dest="$repo_root/internal/zhconv/opencc"
url="https://github.com/BYVoid/OpenCC/releases/download/${tag}/opencc-v${version}-resources.zip"

tables=(s2t s2tw s2twp s2hk t2s tw2s tw2sp hk2s t2tw t2hk)

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

echo "下載 $url"
curl --fail --location --silent --show-error --output "$work/resources.zip" "$url"
echo "zip sha256：$(sha256sum "$work/resources.zip" | cut -d' ' -f1)"

mkdir "$work/src"
unzip -q "$work/resources.zip" -d "$work/src"
src="$(dirname "$(find "$work/src" -name manifest.json -print -quit)")"

# 組出要保留的檔案清單：10 份設定與它們引用的字典（解析 json 中的 "file"，含巢狀 group）。
python3 -I - "$src" "$dest" "$tag" "${tables[@]}" <<'PY'
import hashlib, json, os, shutil, sys

src, dest, tag, *tables = sys.argv[1:]
manifest = json.load(open(os.path.join(src, "manifest.json"), encoding="utf-8"))

def files_of(node):
    if isinstance(node, dict):
        if "file" in node:
            yield node["file"]
        for v in node.values():
            yield from files_of(v)
    elif isinstance(node, list):
        for v in node:
            yield from files_of(v)

keep = set()
for t in tables:
    name = t + ".json"
    keep.add(name)
    keep.update(files_of(json.load(open(os.path.join(src, name), encoding="utf-8"))))

entries = {}
for name in sorted(keep):
    path = os.path.join(src, name)
    data = open(path, "rb").read()
    want = manifest["entries"][name]["sha256"]
    got = hashlib.sha256(data).hexdigest()
    if got != want:
        sys.exit(f"{name} 的 sha256 與上游 manifest 不符：{got} != {want}")
    entries[name] = manifest["entries"][name]

for name in os.listdir(dest):
    if name.endswith((".txt", ".json")):
        os.remove(os.path.join(dest, name))
for name in sorted(keep):
    shutil.copyfile(os.path.join(src, name), os.path.join(dest, name))

out = {k: v for k, v in manifest.items() if k != "entries"}
out["entries"] = entries
with open(os.path.join(dest, "manifest.json"), "w", encoding="utf-8") as f:
    json.dump(out, f, indent=2, sort_keys=True, ensure_ascii=False)
    f.write("\n")
with open(os.path.join(dest, "VERSION"), "w", encoding="utf-8") as f:
    f.write(tag + "\n")
print(f"保留 {len(entries)} 個檔案")
PY

if [ -f "$src/LICENSE" ]; then
  cp "$src/LICENSE" "$dest/LICENSE"
fi
echo "完成。接著更新 zhconv.DataVersion、NOTICE.md 與 testdata/testcases.json，再執行 go test ./internal/zhconv。"
