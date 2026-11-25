// 把瀏覽器 e2e 的結果整理成 Markdown（browser.yml 寫進 job summary）：
//   - e2e/.out/axe/results.jsonl：每頁 × 寬度 × 主題的 axe 結果（serious／critical 的違規數、incomplete 數）；
//   - e2e/.out/perf/*.json：LCP 與捲動的量測。
// 用法：node scripts/e2e-summary.mjs [標題]
import { existsSync, readdirSync, readFileSync } from "node:fs";

const out = new URL("../e2e/.out/", import.meta.url);
const title = process.argv[2] ?? "Browser e2e";
const lines = [`## ${title}`, ""];

const axeFile = new URL("axe/results.jsonl", out);
if (existsSync(axeFile)) {
  const rows = readFileSync(axeFile, "utf8")
    .split("\n")
    .filter(Boolean)
    .map((l) => JSON.parse(l));
  const cols = [...new Set(rows.map((r) => `${r.engine} ${r.width}`))].sort();
  const routes = [...new Set(rows.map((r) => r.route))];
  const bad = (r) =>
    r.violations
      .filter((v) => v.impact === "serious" || v.impact === "critical")
      .reduce((n, v) => n + v.nodes, 0);
  let failures = 0;
  let incomplete = 0;
  lines.push(
    `axe（完整規則，含 color-contrast 與 target-size）：每格是「淺色／深色」的 serious＋critical 節點數；✅ 為 0。`,
    "",
    `| 頁面 | ${cols.join(" | ")} |`,
    `|---|${cols.map(() => "---").join("|")}|`,
  );
  for (const route of routes) {
    const cells = cols.map((c) => {
      const pick = (theme) =>
        rows.find((r) => r.route === route && `${r.engine} ${r.width}` === c && r.theme === theme);
      const l = pick("light");
      const d = pick("dark");
      if (!l && !d) return "—";
      const nl = l ? bad(l) : 0;
      const nd = d ? bad(d) : 0;
      incomplete += (l?.incomplete.length ?? 0) + (d?.incomplete.length ?? 0);
      if (nl + nd === 0) return "✅";
      failures++;
      return `❌ ${nl}／${nd}`;
    });
    lines.push(`| \`${route}\` | ${cells.join(" | ")} |`);
  }
  const rules = {};
  for (const r of rows) {
    for (const v of r.violations) {
      if (v.impact === "serious" || v.impact === "critical") rules[v.id] = (rules[v.id] ?? 0) + 1;
    }
  }
  lines.push(
    "",
    `共 ${rows.length} 次檢查，${failures} 格有問題；incomplete（axe 無法判定、需人工確認）共 ${incomplete} 項。`,
  );
  if (Object.keys(rules).length > 0) {
    lines.push(
      "",
      "違規的規則（出現次數）：" +
        Object.entries(rules)
          .map(([k, n]) => `\`${k}\` ${n}`)
          .join("、"),
    );
  }
  lines.push("");
}

const perfDir = new URL("perf/", out);
if (existsSync(perfDir)) {
  lines.push(
    "### NFR-PERF-10",
    "",
    "| 項目 | 引擎 | 數值 | 門檻 | 判定 | 備註 |",
    "|---|---|---|---|---|---|",
  );
  for (const f of readdirSync(perfDir)
    .filter((f) => f.endsWith(".json"))
    .sort()) {
    const r = JSON.parse(readFileSync(new URL(f, perfDir), "utf8"));
    lines.push(
      `| ${r.case} | ${r.engine} | ${r.value} ${r.unit} | ${r.threshold} ${r.unit} | ${r.pass ? "✅" : "❌"} | ${r.note ?? ""} |`,
    );
  }
  lines.push("");
}

console.log(lines.join("\n"));
