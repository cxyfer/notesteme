// 重新驗證 design-language.md §3.3 的對比表（WCAG 2.2 AA）。jsdom 不會排版，axe 的 color-contrast 規則
// 無法執行，所以直接從 tokens.css 讀出原色計算。
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// vitest 的工作目錄是 web/client。
const css = readFileSync("src/styles/tokens.css", "utf8");
const color = (name: string) => {
  const m = new RegExp(`--color-${name}:\\s*(#[0-9a-f]{6})`, "i").exec(css);
  if (!m) throw new Error(`tokens.css 沒有 --color-${name}`);
  return m[1]!;
};

function luminance(hex: string) {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r! + 0.7152 * g! + 0.0722 * b!;
}
function contrast(a: string, b: string) {
  const [x, y] = [luminance(a), luminance(b)].sort((p, q) => q - p);
  return (x! + 0.05) / (y! + 0.05);
}

describe("token 對比", () => {
  // [前景, 背景, 最低對比]：文字 4.5:1；焦點框等非文字元件 3:1。
  it.each([
    ["ink", "sheet", 4.5],
    ["ink", "opal", 4.5],
    ["pewter", "sheet", 4.5],
    ["pewter", "opal", 4.5],
    ["signal", "sheet", 4.5],
    ["fault", "sheet", 4.5],
    ["ink", "cue", 4.5],
    ["sheet", "ink", 4.5], // 主要按鈕與 toast 的文字
    ["paper", "booth", 4.5],
    ["paper", "panel", 4.5],
    ["pewter-dim", "panel", 4.5],
    ["signal-lit", "panel", 4.5],
    ["fault-lit", "panel", 4.5],
    ["booth", "paper", 4.5], // 深色主題的主要按鈕
    ["cue", "booth", 3], // 深色主題的焦點框
    ["ink", "opal", 3], // 淺色主題的焦點框
  ])("%s 於 %s ≥ %s", (fg, bg, min) => {
    expect(contrast(color(fg), color(bg))).toBeGreaterThanOrEqual(min);
  });

  it("Cue 於 Opal 不足 3:1（規則：淺色主題的黃色記號一律加 Ink 邊）", () => {
    expect(contrast(color("cue"), color("opal"))).toBeLessThan(3);
  });
});
