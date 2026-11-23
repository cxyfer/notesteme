// 字幕樣式的表單模型：API 的 StyleBody 與還沒儲存的修改（Patch：只放改過的欄位）。
// 只送改過的欄位：後端依 lang 帶入的預設字型（font 為空時）不會因為打開表單就被寫死。
import type { components } from "../../api/schema";

export type Style = components["schemas"]["StyleBody"];
export type Line = components["schemas"]["ASSLine"];
export type Layout = components["schemas"]["ASSLayout"];
export type StylePatch = components["schemas"]["Patch"];
export type LinePatch = components["schemas"]["LinePatch"];
export type LayoutPatch = components["schemas"]["LayoutPatch"];

export type LineKey = "primary" | "translation";

export const SHOW_OPTIONS = ["both", "translation", "primary"] as const;
export const ORDER_OPTIONS = ["translation-first", "primary-first"] as const;
export const TRANSLATION_AT_OPTIONS = ["stacked", "top"] as const;
export const ALIGNMENTS = [1, 2, 3, 4, 5, 6, 7, 8, 9] as const;
export const BORDER_STYLES = [1, 3] as const;

/** 字型的建議清單（OUT-R7 依語言的預設，加上常見的西文字型）；仍可以輸入其他字型。 */
export const FONT_SUGGESTIONS = [
  "Noto Sans",
  "Noto Sans CJK TC",
  "Noto Sans CJK SC",
  "Noto Sans CJK JP",
  "Noto Sans CJK KR",
  "Arial",
  "Helvetica",
] as const;

/** 預覽與預設字型用的語言（目標語言、原文語言）；空字串是「不指定」。 */
export const LANG_OPTIONS = ["", "zh-Hant", "zh-Hans", "ja", "ko", "en"] as const;

const COLOR_RE = /^#[0-9A-Fa-f]{6}([0-9A-Fa-f]{2})?$/;

export function isColor(s: string) {
  return COLOR_RE.test(s);
}

/** 改過的欄位數；0 代表沒有未儲存的修改。 */
export function patchSize(p: StylePatch): number {
  return (
    Object.keys(p.primary ?? {}).length +
    Object.keys(p.translation ?? {}).length +
    Object.keys(p.layout ?? {}).length
  );
}

/** 去掉空的區塊；沒有任何修改時回傳 undefined。 */
export function compactPatch(p: StylePatch): StylePatch | undefined {
  const out: StylePatch = {};
  if (p.primary && Object.keys(p.primary).length) out.primary = p.primary;
  if (p.translation && Object.keys(p.translation).length) out.translation = p.translation;
  if (p.layout && Object.keys(p.layout).length) out.layout = p.layout;
  return patchSize(out) ? out : undefined;
}

/** 前端先檢查的欄位錯誤（後端也會檢查；這裡只是讓使用者不必送出才知道）。鍵是 API 的欄位路徑。 */
export function validatePatch(p: StylePatch): Record<string, "color" | "range"> {
  const errs: Record<string, "color" | "range"> = {};
  for (const key of ["primary", "translation"] as const) {
    const l = p[key];
    if (!l) continue;
    for (const f of ["color", "outline_color", "back_color"] as const) {
      const v = l[f];
      if (v !== undefined && !isColor(v)) errs[`${key}.${f}`] = "color";
    }
    if (l.size !== undefined && !(Number.isInteger(l.size) && l.size >= 1 && l.size <= 400))
      errs[`${key}.size`] = "range";
    for (const f of ["outline", "shadow"] as const) {
      const v = l[f];
      if (v !== undefined && !(Number.isFinite(v) && v >= 0 && v <= 50))
        errs[`${key}.${f}`] = "range";
    }
  }
  const m = p.layout?.margin_v;
  if (m !== undefined && !(Number.isInteger(m) && m >= 0 && m <= 1080))
    errs["layout.margin_v"] = "range";
  return errs;
}

/** 名稱規則（CreateAssStyleInputBody）：小寫英數、「.」「_」「-」，最長 64 字元。 */
export function validStyleName(name: string) {
  return /^[a-z0-9][a-z0-9._-]{0,63}$/.test(name);
}
