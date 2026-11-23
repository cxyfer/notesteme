// 字幕全文搜尋（SRCH）的純函式：URL 參數、查詢驗證、命中範圍、跳轉時間。
// highlights 是 UTF-16 位移（SRCH-R6），直接用 String.prototype.slice；text 一律當純文字，不當 HTML。

export interface SearchParams {
  q?: string;
  lang?: string;
}

/** API 的上限：去掉前後空白後 1～200 個 code point（SRCH-R5） */
export const MAX_QUERY = 200;

function str(v: unknown): string | undefined {
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  return undefined;
}

/** route 的 validateSearch：只留 q 與 lang；太長的 q 原樣保留，由表單顯示錯誤 */
export function validateSearchParams(raw: Record<string, unknown>): SearchParams {
  const out: SearchParams = {};
  const q = str(raw.q)?.trim();
  if (q) out.q = q.slice(0, 1000);
  const lang = str(raw.lang)?.trim();
  if (lang && /^[A-Za-z0-9-]{1,35}$/.test(lang)) out.lang = lang;
  return out;
}

export type QueryProblem = "empty" | "tooLong";

/** 送出前的檢查（控制字元交給伺服器的 422） */
export function queryProblem(q: string): QueryProblem | undefined {
  const s = q.trim();
  if (s === "") return "empty";
  if ([...s].length > MAX_QUERY) return "tooLong";
  return undefined;
}

export interface Span {
  start: number;
  end: number;
}

export interface Piece {
  text: string;
  hit: boolean;
}

/**
 * 依 highlights 把 text 切成片段。伺服器保證依序、不重疊；這裡仍防禦：
 * 夾在 [0, text.length]、排序、丟掉空的與重疊的範圍。
 */
export function splitHighlights(text: string, spans: readonly Span[] | null | undefined): Piece[] {
  const clean = (spans ?? [])
    .map((s) => ({
      start: Math.max(0, Math.min(text.length, Math.floor(s.start))),
      end: Math.max(0, Math.min(text.length, Math.floor(s.end))),
    }))
    .filter((s) => s.end > s.start)
    .sort((a, b) => a.start - b.start);
  const out: Piece[] = [];
  let at = 0;
  for (const s of clean) {
    if (s.start < at) continue;
    if (s.start > at) out.push({ text: text.slice(at, s.start), hit: false });
    out.push({ text: text.slice(s.start, s.end), hit: true });
    at = s.end;
  }
  if (at < text.length || out.length === 0) out.push({ text: text.slice(at), hit: false });
  return out;
}

/** "HH:MM:SS.mmm" → 秒；格式不對時是 undefined */
export function parseClock(s: string): number | undefined {
  const m = /^(\d+):(\d{2}):(\d{2})(?:\.(\d{1,3}))?$/.exec(s);
  if (!m) return undefined;
  const ms = m[4] ? Number(m[4].padEnd(3, "0")) : 0;
  return Number(m[1]) * 3600 + Number(m[2]) * 60 + Number(m[3]) + ms / 1000;
}

/**
 * 影音詳情的 t 只接受整數秒（會 floor）：取 ceil(start) 才不會落在前一句；
 * ceil 超過這一句的結束時就退回 floor(start)。
 */
export function jumpSecond(start: string, end: string): number {
  const s = parseClock(start) ?? 0;
  const e = parseClock(end) ?? s;
  const up = Math.ceil(s);
  return up < e || up === s ? up : Math.floor(s);
}

/** 連續屬於同一部影音的結果放成一組（結果已依影音排序，SRCH-R7） */
export function groupByItem<T extends { item: { id: string } }>(rows: readonly T[]): T[][] {
  const groups: T[][] = [];
  for (const r of rows) {
    const last = groups.at(-1);
    if (last && last[0]!.item.id === r.item.id) last.push(r);
    else groups.push([r]);
  }
  return groups;
}
