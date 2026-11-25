// 真實瀏覽器裡的 axe（NFR-A11Y-1、A11Y-2）：完整規則，包含 jsdom 關掉的 color-contrast 與 target-size
// （WCAG 2.2 的 2.5.8，24×24px）。serious 與 critical 一律失敗；incomplete（axe 無法判定，例如文字疊在圖片上）
// 不算失敗，但數量與節點寫進 e2e/.out/axe/results.jsonl 與測試附件，不默默略過。
import { appendFileSync, mkdirSync } from "node:fs";
import path from "node:path";

import AxeBuilder from "@axe-core/playwright";
import type { Page, TestInfo } from "@playwright/test";

import { outDir } from "./server";

export const REQUIRED_RULES = ["color-contrast", "target-size"] as const;

export interface AxeContext {
  route: string;
  width: number;
  theme: "light" | "dark";
}

interface NodeLike {
  target: unknown[];
  failureSummary?: string;
  html: string;
}

function describeNodes(nodes: NodeLike[]) {
  return nodes
    .slice(0, 8)
    .map(
      (n) =>
        `    ${n.target.map(String).join(" ")}\n      ${n.html.slice(0, 160)}${n.failureSummary ? `\n      ${n.failureSummary.replace(/\n/g, "\n      ")}` : ""}`,
    )
    .join("\n");
}

/** 跑一次 axe，回傳 serious／critical 的報告文字（空字串代表通過）。 */
export async function axeCheck(page: Page, info: TestInfo, ctx: AxeContext): Promise<string> {
  const results = await new AxeBuilder({ page })
    .options({
      rules: { "color-contrast": { enabled: true }, "target-size": { enabled: true } },
      resultTypes: ["violations", "incomplete"],
      // 嵌入的 YouTube 播放器是跨來源 iframe，而且 e2e 擋下了外部請求
      iframes: false,
    })
    .analyze();
  // 規則要真的檢查到節點（inapplicable 代表這一頁沒有可檢查的元素，等於沒檢查）
  const ran = new Set(
    [...results.passes, ...results.violations, ...results.incomplete].map((r) => r.id),
  );
  const missing = REQUIRED_RULES.filter((r) => !ran.has(r));
  const serious = results.violations.filter(
    (v) => v.impact === "serious" || v.impact === "critical",
  );
  const engine = info.project.name;
  mkdirSync(path.join(outDir, "axe"), { recursive: true });
  appendFileSync(
    path.join(outDir, "axe", "results.jsonl"),
    JSON.stringify({
      engine,
      ...ctx,
      violations: results.violations.map((v) => ({
        id: v.id,
        impact: v.impact,
        nodes: v.nodes.length,
      })),
      incomplete: results.incomplete.map((v) => ({
        id: v.id,
        impact: v.impact,
        nodes: v.nodes.length,
      })),
      // 兩條規則實際有沒有執行到節點
      ruleState: Object.fromEntries(
        REQUIRED_RULES.map((r) => [
          r,
          results.violations.some((v) => v.id === r)
            ? "violation"
            : results.incomplete.some((v) => v.id === r)
              ? "incomplete"
              : results.passes.some((v) => v.id === r)
                ? "pass"
                : "inapplicable",
        ]),
      ),
    }) + "\n",
  );
  if (results.incomplete.length > 0) {
    await info.attach(`axe-incomplete-${ctx.theme}-${ctx.width}.txt`, {
      body: results.incomplete
        .map((v) => `${v.impact} ${v.id}: ${v.help}\n${describeNodes(v.nodes)}`)
        .join("\n"),
      contentType: "text/plain",
    });
  }
  const where = `${ctx.route}（${engine}、${ctx.width}px、${ctx.theme === "light" ? "淺色" : "深色"}）`;
  const lines: string[] = [];
  if (missing.length > 0) lines.push(`${where}：axe 的 ${missing.join("、")} 沒有檢查到任何元素`);
  for (const v of serious) {
    lines.push(`${where} ${v.impact} ${v.id}：${v.help}\n${describeNodes(v.nodes)}`);
  }
  return lines.join("\n");
}
