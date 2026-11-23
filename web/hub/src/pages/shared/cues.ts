// 版本內容：以 GET /api/v1/versions/:id/download 取得（需要 download 權限；/content 要 regenerate，
// 一般成員沒有，所以比較與預覽都用 download，見 docs/ui/README.md 10.17）。內容是不可信的輸入：只取需要的欄位、
// 型別不對的句子丟掉、數量有上限，文字一律當純文字顯示。
import { requestText } from "../../api/client";

export interface Cue {
  start: number; // 秒
  end: number;
  text: string;
}

export const MAX_CUES = 20_000;

export function parseCuesJSON(raw: string): { lang: string; cues: Cue[] } {
  const data: unknown = JSON.parse(raw);
  if (!data || typeof data !== "object") throw new Error("cues-json：不是物件");
  const d = data as { lang?: unknown; cues?: unknown };
  const list = Array.isArray(d.cues) ? d.cues.slice(0, MAX_CUES) : [];
  const cues: Cue[] = [];
  for (const c of list) {
    if (!c || typeof c !== "object") continue;
    const { start_ms, end_ms, text } = c as Record<string, unknown>;
    if (typeof start_ms !== "number" || typeof end_ms !== "number" || typeof text !== "string")
      continue;
    if (!Number.isFinite(start_ms) || !Number.isFinite(end_ms) || end_ms < start_ms) continue;
    cues.push({ start: start_ms / 1000, end: end_ms / 1000, text });
  }
  return { lang: typeof d.lang === "string" ? d.lang.slice(0, 35) : "", cues };
}

export function downloadPath(id: string) {
  return `/api/v1/versions/${encodeURIComponent(id)}/download`;
}

export async function fetchCues(id: string, signal?: AbortSignal) {
  return parseCuesJSON(
    await requestText(downloadPath(id), { query: { format: "cues-json" }, signal }),
  );
}

/** 時間點所在的句子（沒有時是前一句）；cues 依開始時間排序 */
export function cueAt(cues: readonly Cue[], t: number): number {
  let lo = 0;
  let hi = cues.length - 1;
  let ans = -1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (cues[mid]!.start <= t) {
      ans = mid;
      lo = mid + 1;
    } else hi = mid - 1;
  }
  return ans;
}
