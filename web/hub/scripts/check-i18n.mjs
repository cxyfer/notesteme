// NFR-I18N-1：三種語言的字典 key 完全一致、沒有空字串、插值參數一致。
// 複數後綴（_zero/_one/_two/_few/_many/_other）先正規化：中文只有 _other，英文另有 _one。
import { readFileSync } from "node:fs";

const dir = new URL("../src/i18n/locales/", import.meta.url);
const langs = ["zh-Hant", "zh-Hans", "en"];
const plural = /_(zero|one|two|few|many|other)$/;
const errors = [];

function flatten(obj, prefix = "", out = new Map()) {
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (v !== null && typeof v === "object") flatten(v, key, out);
    else out.set(key, v);
  }
  return out;
}
const params = (s) => [...s.matchAll(/\{\{\s*([\w.]+)\s*(?:,[^}]*)?\}\}/g)].map((m) => m[1]).sort();

const dicts = Object.fromEntries(
  langs.map((l) => [l, flatten(JSON.parse(readFileSync(new URL(`${l}.json`, dir), "utf8")))]),
);
// base key → { lang → [values] }
const bases = new Map();
for (const l of langs) {
  for (const [key, value] of dicts[l]) {
    if (typeof value !== "string") errors.push(`${l}: ${key} 不是字串`);
    else if (value.trim() === "") errors.push(`${l}: ${key} 是空字串`);
    const isPlural = plural.test(key);
    const base = key.replace(plural, "");
    if (!bases.has(base)) bases.set(base, {});
    const entry = bases.get(base);
    (entry[l] ??= []).push({ key, value: String(value), isPlural });
  }
}
for (const [base, entry] of bases) {
  for (const l of langs) {
    if (!entry[l]) {
      errors.push(`${l}: 缺少 ${base}`);
      continue;
    }
    if (entry[l].some((e) => e.isPlural) && !entry[l].some((e) => e.key.endsWith("_other"))) {
      errors.push(`${l}: 複數 key ${base} 缺少 _other`);
    }
  }
  const ref = entry[langs[0]]?.[0];
  if (!ref) continue;
  const want = JSON.stringify(params(ref.value));
  for (const l of langs) {
    for (const e of entry[l] ?? []) {
      // 複數形式可以省略 {{count}}（例如英文的 "one item"），其餘參數要一致。
      const got = params(e.value).filter((p) => !(e.isPlural && p === "count"));
      const exp = JSON.parse(want).filter((p) => !(e.isPlural && p === "count"));
      if (JSON.stringify(got) !== JSON.stringify(exp)) {
        errors.push(
          `${l}: ${e.key} 的參數 ${JSON.stringify(got)} 與 ${langs[0]} 的 ${JSON.stringify(exp)} 不同`,
        );
      }
    }
  }
}
if (errors.length) {
  console.error(`i18n 檢查失敗（${errors.length}）：\n  ` + errors.join("\n  "));
  process.exit(1);
}
console.log(`i18n OK：${langs.join("、")} 各 ${bases.size} 個 key`);
