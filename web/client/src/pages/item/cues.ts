// 字幕句的時間與對齊：API 的時間是 HH:MM:SS.mmm 字串（OUT-R3），畫面用秒。
// 不同變體（segmented 與 corrected、原文與譯文）的句子不一定一對一（校正可能合併或拆句），
// 所以依「時間重疊」分組：互相重疊的句子串成一組，一組畫成一列。
import type { components } from "../../api/schema";

export type ApiCue = components["schemas"]["Cue"];

export interface Cue {
  index: number;
  start: number;
  end: number;
  text: string;
  timing?: string;
}

/** HH:MM:SS(.mmm)、MM:SS 或秒數 → 秒；不正確時回傳 NaN */
export function parseCueTime(s: string): number {
  if (!s.includes(":")) return Number(s);
  const parts = s.split(":");
  if (parts.length > 3 || parts.some((p) => p.trim() === "")) return NaN;
  let total = 0;
  for (const p of parts) total = total * 60 + Number(p);
  return total;
}

export function toCues(api: readonly ApiCue[] | null | undefined): Cue[] {
  return (api ?? [])
    .map((c) => ({
      index: c.index,
      start: parseCueTime(c.start),
      end: parseCueTime(c.end),
      text: c.text,
      timing: c.timing,
    }))
    .filter((c) => Number.isFinite(c.start) && Number.isFinite(c.end));
}

/** 在 start 已排序的句子中找 t 所在（或最後一個已開始）的句子；t 在第一句之前回傳 -1 */
export function cueAt(cues: readonly Cue[], t: number): number {
  let lo = 0;
  let hi = cues.length - 1;
  let found = -1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (cues[mid]!.start <= t) {
      found = mid;
      lo = mid + 1;
    } else hi = mid - 1;
  }
  return found;
}

export interface CueGroup {
  start: number;
  end: number;
  /** 第一組變體（例如校正前、原文）的句子 */
  a: Cue[];
  /** 第二組變體（例如校正後、譯文）的句子 */
  b: Cue[];
}

/**
 * 依時間重疊把兩組句子分組。兩邊都依 start 排序後掃過去：
 * 一組從最早的句子開始，之後開始時間早於這組結束時間的句子（任一邊）都併進來。
 */
export function alignCues(a: readonly Cue[], b: readonly Cue[]): CueGroup[] {
  const xs = [...a].sort((p, q) => p.start - q.start);
  const ys = [...b].sort((p, q) => p.start - q.start);
  const groups: CueGroup[] = [];
  let i = 0;
  let j = 0;
  while (i < xs.length || j < ys.length) {
    const takeA = j >= ys.length || (i < xs.length && xs[i]!.start <= ys[j]!.start);
    const first = takeA ? xs[i++]! : ys[j++]!;
    const g: CueGroup = { start: first.start, end: first.end, a: [], b: [] };
    (takeA ? g.a : g.b).push(first);
    for (;;) {
      const nextA = i < xs.length && xs[i]!.start < g.end ? xs[i] : undefined;
      const nextB = j < ys.length && ys[j]!.start < g.end ? ys[j] : undefined;
      const next = nextA && (!nextB || nextA.start <= nextB.start) ? nextA : nextB;
      if (!next) break;
      if (next === nextA) {
        g.a.push(next);
        i++;
      } else {
        g.b.push(next);
        j++;
      }
      g.end = Math.max(g.end, next.end);
    }
    groups.push(g);
  }
  return groups;
}

/** 一組句子的文字：CJK 直接接起來，其他語言用空白 */
export function joinText(cues: readonly Cue[], lang?: string): string {
  const sep = lang && /^(zh|ja|ko)\b/i.test(lang) ? "" : " ";
  return cues.map((c) => c.text).join(sep);
}
