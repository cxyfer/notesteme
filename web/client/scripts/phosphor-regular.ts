// Phosphor Icons 的每個圖示定義（dist/defs/<名稱>.es.js）都帶 6 種字重（thin、light、regular、bold、fill、duotone），
// design-language.md §5.4 只用 Regular（Icon 元件固定 weight="regular"）。打包前只留 regular 那一項，
// 每個圖示約小三分之二，初始 JS 才放得下導覽與狀態的圖示（NFR-PERF-10）。
// 格式不符預期時原樣保留（src/components/icon.test.tsx 會檢查真的瘦身了）。
import type { Plugin } from "vite";

const DEFS = /[\\/]@phosphor-icons[\\/]react[\\/]dist[\\/]defs[\\/](\w+)\.es\.js$/;
const MAP = /new Map\(\[[\s\S]*\n\]\)/;
/** 除了 regular 之外還要保留 fill 的圖示：§6.4「完成」是實心勾（StatusBadge） */
const KEEP_FILL = new Set(["CheckCircle"]);

const entry = (weight: string) =>
  new RegExp(`\\n {2}\\[\\n {4}"${weight}",\\n[\\s\\S]*?\\n {2}\\]`);

/** 只留下 regular（與 KEEP_FILL 的 fill）的圖示定義；匯出給測試用 */
export function keepRegularWeight(code: string, name = ""): string {
  const weights = KEEP_FILL.has(name) ? ["fill", "regular"] : ["regular"];
  const kept = weights.map((w) => entry(w).exec(code)?.[0]);
  if (kept.some((k) => !k) || !MAP.test(code)) return code;
  return code.replace(MAP, () => `new Map([${kept.join(",")}\n])`);
}

export function phosphorRegular(): Plugin {
  return {
    name: "notesteme:phosphor-regular",
    enforce: "pre",
    transform(code, id) {
      const m = DEFS.exec(id.split("?")[0]!);
      if (!m) return null;
      return { code: keepRegularWeight(code, m[1]), map: null };
    },
  };
}
