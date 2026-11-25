// 閱讀區的字型（design-language.md §4.1）：Source Serif 4 ＋ Noto Serif TC／SC，Fontsource 自架（CSP font-src 'self'）。
// 依內容的語言動態載入：中文字型的 @font-face 宣告（unicode-range 清單）本身就有數十 KB，
// 所以拉丁字母一律載入、繁中只載入 TC、简中只載入 SC，日文與韓文不載入中文字型（字形不同，改用系統字型）。
// 載入的只是宣告：瀏覽器要等閱讀區真的用到某個字（落在某個 unicode-range 切片）才下載那個切片。
import { useEffect } from "react";

export type ReadingFontSet = "latin" | "tc" | "sc";

const HANS = /^zh-(?:hans|cn|sg|my)(?:-|$)/i;

/** 這個語言的閱讀區要哪幾組字型；空字串（沒有標 lang）時由呼叫端傳入介面語言 */
export function readingFontsFor(lang: string): ReadingFontSet[] {
  const l = lang.trim().toLowerCase();
  if (l === "zh" || l.startsWith("zh-")) return ["latin", HANS.test(l) ? "sc" : "tc"];
  return ["latin"];
}

const LOADERS: Record<ReadingFontSet, () => Promise<unknown>> = {
  latin: () => import("./fonts/latin"),
  tc: () => import("./fonts/tc"),
  sc: () => import("./fonts/sc"),
};

/** 依筆記的 lang（沒有時用 `<html lang>`）載入閱讀字型；同一組只會載入一次（模組快取） */
export function useReadingFonts(lang: string | undefined) {
  useEffect(() => {
    const l = lang || document.documentElement.lang || "zh-Hant";
    for (const set of readingFontsFor(l)) void LOADERS[set]().catch(() => undefined);
  }, [lang]);
}
