// Fontsource 的 CSS 在打包前先瘦身（design-language.md §4.1「只載入實際用到的字重與子集」）：
//   1. 只留 woff2：支援 es2022 的瀏覽器都支援 woff2，woff 備援只會讓 dist 與內嵌的 binary 變大。
//   2. Noto Serif TC／SC 只留中文的 unicode-range 切片（檔名是數字編號）：
//      latin、latin-ext、cyrillic、vietnamese 由字型堆疊中排在前面的 Source Serif 4 處理，永遠用不到。
import type { Plugin } from "vite";

const FONTSOURCE_CSS = /[\\/]@fontsource[\\/][^\\/]+[\\/][^\\/]+\.css$/;
const CJK_SERIF = /[\\/]@fontsource[\\/]noto-serif-(?:tc|sc)[\\/]/;
const WOFF_FALLBACK = /,\s*url\([^)]*\.woff\)\s*format\(['"]woff['"]\)/g;
const NON_CJK_FACE =
  /\/\*\s*noto-serif-(?:tc|sc)-(?:latin|latin-ext|cyrillic|vietnamese)-\d+-\w+\s*\*\/\s*@font-face\s*\{[^}]*\}\s*/g;

/** 把一份 Fontsource CSS 瘦身；匯出給測試用 */
export function slimFontsourceCss(id: string, code: string): string {
  let out = code.replace(WOFF_FALLBACK, "");
  if (CJK_SERIF.test(id)) out = out.replace(NON_CJK_FACE, "");
  return out;
}

export function fontsourceSlim(): Plugin {
  return {
    name: "notesteme:fontsource-slim",
    enforce: "pre",
    transform(code, id) {
      const path = id.split("?")[0]!;
      if (!FONTSOURCE_CSS.test(path)) return null;
      return { code: slimFontsourceCss(path, code), map: null };
    },
  };
}
