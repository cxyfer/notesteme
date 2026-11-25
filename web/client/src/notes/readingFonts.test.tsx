// 閱讀區字型（design-language.md §4.1）：依語言選字型組、Fontsource CSS 的瘦身、只從筆記的轉接檔載入。
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

import { slimFontsourceCss } from "../../scripts/fontsource-slim";
import { readingFontsFor } from "./readingFonts";

const FS = "node_modules/@fontsource";

describe("閱讀字型：依語言載入", () => {
  it.each([
    ["zh-Hant", ["latin", "tc"]],
    ["zh-TW", ["latin", "tc"]],
    ["zh-HK", ["latin", "tc"]],
    ["zh", ["latin", "tc"]],
    ["zh-Hans", ["latin", "sc"]],
    ["zh-Hans-CN", ["latin", "sc"]],
    ["zh-CN", ["latin", "sc"]],
    ["zh-sg", ["latin", "sc"]],
    ["en", ["latin"]],
    ["en-US", ["latin"]],
    ["ja", ["latin"]],
    ["ko", ["latin"]],
  ])("%s → %j", (lang, sets) => {
    expect(readingFontsFor(lang)).toEqual(sets);
  });
});

describe("閱讀字型：Fontsource CSS 瘦身", () => {
  it("中文字型只留 woff2 的中文 unicode-range 切片", () => {
    for (const pkg of ["noto-serif-tc", "noto-serif-sc"])
      for (const w of [400, 600]) {
        const id = `/x/node_modules/@fontsource/${pkg}/${w}.css`;
        const raw = readFileSync(`${FS}/${pkg}/${w}.css`, "utf8");
        const out = slimFontsourceCss(id, raw);
        const faces = out.match(/@font-face/g)?.length ?? 0;
        expect(faces).toBeGreaterThan(90);
        // 每個 @font-face 都有 unicode-range 與 font-display: swap
        expect(out.match(/unicode-range:/g)?.length).toBe(faces);
        expect(out.match(/font-display: swap/g)?.length).toBe(faces);
        expect(out).not.toMatch(/\.woff\)/);
        expect(out).not.toMatch(/-(latin|latin-ext|cyrillic|vietnamese)-\d+-normal/);
        // 原本有 latin 等切片，瘦身確實去掉了東西
        expect(raw).toMatch(/-latin-\d+-normal/);
      }
  });

  it("Source Serif 4 保留 latin 切片，只去掉 woff 備援", () => {
    const id = `/x/node_modules/@fontsource/source-serif-4/latin-400.css`;
    const out = slimFontsourceCss(id, readFileSync(`${FS}/source-serif-4/latin-400.css`, "utf8"));
    expect(out).toMatch(/source-serif-4-latin-400-normal\.woff2/);
    expect(out).not.toMatch(/\.woff\)/);
    expect(out).toMatch(/font-display: swap/);
  });

  it("中文字型只從 Client 的筆記轉接檔載入（不進初始 CSS，也不進 web/shared）", () => {
    const main = readFileSync("src/main.tsx", "utf8");
    expect(main).not.toMatch(/source-serif|noto-serif|readingFonts/);
    expect(readFileSync("src/notes/NoteMarkdown.tsx", "utf8")).toMatch(/useReadingFonts/);
    expect(readFileSync("../shared/src/notes/NoteMarkdown.tsx", "utf8")).not.toMatch(
      /fontsource|readingFonts/,
    );
    // 只用 §4.3 的 400／600，沒有其他字重
    const imports = ["latin", "tc", "sc"]
      .map((f) => readFileSync(`src/notes/fonts/${f}.ts`, "utf8"))
      .join("\n");
    for (const m of imports.matchAll(/@fontsource\/[\w-]+\/([\w-]+)\.css/g))
      expect(m[1]).toMatch(/^(latin(-ext)?-)?(400|600)(-italic)?$/);
  });
});
