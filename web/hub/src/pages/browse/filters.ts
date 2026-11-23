// 瀏覽頁的篩選（HREP-R16）：存在 URL（docs/ui/README.md §3.3），原樣當作 GET /api/v1/versions 的參數。
// 值的驗證交給伺服器（422 的 errors[].path 是 query.<參數>），這裡只留下認得的鍵與字串值。
export const TEXT_FILTERS = [
  "channel",
  "video",
  "lang",
  "transcribe_model",
  "correct_model",
  "translate_model",
] as const;
export const PLATFORMS = ["youtube", "bilibili", "podcast"] as const;
export const KINDS = ["transcript", "chapters", "note", "frames"] as const;
export const VARIANTS = [
  "source",
  "normalized",
  "segmented",
  "corrected",
  "translated",
  "edited",
] as const;

export interface BrowseSearch {
  platform?: string;
  channel?: string;
  video?: string;
  lang?: string;
  kind?: string;
  variant?: string;
  transcribe_model?: string;
  correct_model?: string;
  translate_model?: string;
  exclude_unverified?: "true";
  sort?: "new";
}

const KEYS = ["platform", "kind", "variant", ...TEXT_FILTERS] as const;

export function validateBrowseSearch(search: Record<string, unknown>): BrowseSearch {
  const out: BrowseSearch = {};
  for (const k of KEYS) {
    const v = search[k];
    if (typeof v === "string" && v.trim() !== "") out[k] = v.trim().slice(0, 256);
  }
  if (search.exclude_unverified === "true" || search.exclude_unverified === true)
    out.exclude_unverified = "true";
  if (search.sort === "new") out.sort = "new";
  return out;
}

/** 有沒有任何篩選條件（排序不算） */
export function hasFilters(s: BrowseSearch): boolean {
  return Object.entries(s).some(([k, v]) => k !== "sort" && v !== undefined);
}
