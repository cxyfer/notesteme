// 影片頁的 URL 參數（router 在初始 chunk 匯入，不能放在 lazy 的頁面檔案中）。
export interface VideoSearch {
  part?: number;
  compare?: string;
}

export function validateVideoSearch(s: Record<string, unknown>): VideoSearch {
  const out: VideoSearch = {};
  const part = Number(s.part);
  if (Number.isInteger(part) && part >= 1 && part <= 9999) out.part = part;
  if (typeof s.compare === "string" && /^[\w-]{1,64},[\w-]{1,64}$/.test(s.compare))
    out.compare = s.compare;
  return out;
}
