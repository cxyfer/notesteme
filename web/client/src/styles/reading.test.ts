// 閱讀區版面（design-language.md §4.3、§5.2）：jsdom 不套用樣式表，所以直接讀 m4.css 的規則斷言。
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const css = readFileSync("src/styles/m4.css", "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

/** 回傳選擇器清單完全相同的那條規則的宣告 */
function rule(selectors: string): Record<string, string> {
  const want = selectors.split(",").map((s) => s.trim());
  for (const m of css.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
    const sel = m[1]!.split(",").map((s) => s.trim());
    if (sel.length === want.length && sel.every((s, i) => s === want[i])) {
      const decl: Record<string, string> = {};
      for (const d of m[2]!.split(";")) {
        const i = d.indexOf(":");
        if (i > 0) decl[d.slice(0, i).trim()] = d.slice(i + 1).trim();
      }
      return decl;
    }
  }
  throw new Error(`m4.css 沒有 ${selectors}`);
}

describe("閱讀區（.note-view）", () => {
  it("預設（拉丁字母）：Source Serif 4、16px、行高 1.6、最多 66 字元", () => {
    const r = rule(".note-view");
    expect(r["--family"]).toBe('"Source Serif 4", "Noto Serif TC"');
    expect(r["font-size"]).toBe("var(--font-16)");
    expect(r["line-height"]).toBe("1.6");
    expect(r["max-width"]).toBe("66ch");
    // 只改 --family（§4.2），不直接寫 font-family：全域的 * 規則會蓋掉
    expect(r["font-family"]).toBeUndefined();
  });

  it("中文：行高 1.8、最多 38 字（38em）", () => {
    const r = rule(".note-view:lang(zh)");
    expect(r["line-height"]).toBe("1.8");
    expect(r["max-width"]).toBe("38em");
  });

  it("简中改用 Noto Serif SC；子元素也要蓋過 tokens.css 的 :lang()", () => {
    const r = rule(
      ".note-view:lang(zh-Hans), .note-view :lang(zh-Hans), .note-view:lang(zh-CN), .note-view :lang(zh-CN), .note-view:lang(zh-SG), .note-view :lang(zh-SG), .note-view:lang(zh-MY), .note-view :lang(zh-MY)",
    );
    expect(r["--family"]).toBe('"Source Serif 4", "Noto Serif SC"');
  });

  it("標題、粗體與表頭用 600（§4.3 不用 700）", () => {
    const r = rule(
      ".note-view h1, .note-view h2, .note-view h3, .note-view h4, .note-view h5, .note-view h6, .note-view strong, .note-view b, .note-view th",
    );
    expect(r["font-weight"]).toBe("600");
  });

  it("不再有舊的 72ch／行高 1.7", () => {
    expect(css).not.toMatch(/72ch/);
    expect(css).not.toMatch(/line-height:\s*1\.7\b/);
  });
});
