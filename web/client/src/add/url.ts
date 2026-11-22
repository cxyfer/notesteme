// 新增抽屜的輸入解析。

/** 單一行的 http(s) 網址（「在任何地方貼上網址」與送出辨識前的檢查）。Podcast feed 可能在任何網域，所以不看網域。 */
export function isHttpUrl(s: string): boolean {
  const v = s.trim();
  if (!v || /\s/.test(v)) return false;
  try {
    const u = new URL(v);
    return (u.protocol === "http:" || u.protocol === "https:") && u.hostname !== "";
  } catch {
    return false;
  }
}

/**
 * Bilibili 分P 的編號：「1, 3, 5-7」→ [1, 3, 5, 6, 7]（去重、排序）。
 * 格式不對、編號小於 1 或範圍超過 1000 個時回傳 undefined。
 */
export function parseParts(s: string): number[] | undefined {
  const out = new Set<number>();
  const tokens = s
    .split(/[,，、\s]+/)
    .map((x) => x.trim())
    .filter(Boolean);
  if (!tokens.length) return undefined;
  for (const tok of tokens) {
    const m = /^(\d{1,4})(?:\s*[-–~]\s*(\d{1,4}))?$/.exec(tok);
    if (!m) return undefined;
    const a = Number(m[1]);
    const b = m[2] !== undefined ? Number(m[2]) : a;
    if (a < 1 || b < a || b - a > 1000) return undefined;
    for (let i = a; i <= b; i++) out.add(i);
  }
  if (out.size > 1000) return undefined;
  return [...out].sort((x, y) => x - y);
}
