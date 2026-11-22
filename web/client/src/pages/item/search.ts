// 影音詳情的 search params：分頁、字幕顯示模式、起始時間（可以加書籤或分享，docs/ui/README.md §3.3）。
// 不合法的值直接丟掉（web/client/README.md「篩選 ↔ URL」）。

export const ITEM_TABS = ["subtitles", "stages", "runs", "outputs"] as const;
export type ItemTab = (typeof ITEM_TABS)[number];

/** 字幕的顯示模式（LIB-R5、R12） */
export const CUE_MODES = [
  "source",
  "normalized",
  "corrected",
  "diff",
  "translated",
  "parallel",
  "edited",
] as const;
export type CueMode = (typeof CUE_MODES)[number];

export interface ItemSearch {
  tab?: ItemTab;
  mode?: CueMode;
  /** 起始時間（秒） */
  t?: number;
}

function oneOf<T extends string>(list: readonly T[], v: unknown): T | undefined {
  const s = typeof v === "number" ? String(v) : v;
  return typeof s === "string" && (list as readonly string[]).includes(s) ? (s as T) : undefined;
}

export function validateItemSearch(raw: Record<string, unknown>): ItemSearch {
  const out: ItemSearch = {};
  const tab = oneOf(ITEM_TABS, raw.tab);
  if (tab && tab !== "subtitles") out.tab = tab;
  const mode = oneOf(CUE_MODES, raw.mode);
  if (mode) out.mode = mode;
  const t = typeof raw.t === "string" ? Number(raw.t) : raw.t;
  if (typeof t === "number" && Number.isFinite(t) && t >= 0 && t < 1e7) out.t = Math.floor(t);
  return out;
}
