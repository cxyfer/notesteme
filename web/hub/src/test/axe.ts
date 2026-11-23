// axe 檢查（NFR-A11Y-1）：serious 與 critical 等級的問題一律失敗。
// jsdom 不會排版，color-contrast 規則無法執行，改由 src/styles/contrast.test.ts 檢查 token 的對比。
import axe from "axe-core";
import { expect } from "vitest";

export async function expectNoA11yViolations(node: Element = document.body) {
  const results = await axe.run(node, {
    rules: { "color-contrast": { enabled: false } },
    resultTypes: ["violations"],
    // 嵌入的 YouTube／Bilibili 播放器是跨來源的 iframe，axe 進不去（jsdom 裡也不是真的 frame）
    iframes: false,
  });
  const serious = results.violations.filter(
    (v) => v.impact === "serious" || v.impact === "critical",
  );
  const report = serious
    .map(
      (v) =>
        `${v.impact} ${v.id}: ${v.help}\n  ${v.nodes.map((n) => n.target.join(" ")).join("\n  ")}`,
    )
    .join("\n");
  expect(serious, report).toEqual([]);
  return results;
}
