// 時間帶的說話密度（design-language.md §6.1 的「說話密度」圖層；pages.md §4）。
//
//   - 依字幕句的時間分佈計算：每句的字數（不含空白的 code point）平均分到它的時間範圍，
//     每個區間的值是「字數／秒，依重疊時間加權」。字數依語言而不同，所以只畫相對高低，不顯示絕對數字。
//   - 固定用原始字幕（source，沒有時用 normalized）：不跟著字幕分頁的顯示模式變，切換模式時輪廓不會跳動。
//   - 輪廓是裝飾（StatusBand 的底軌在 aria-hidden 內）；文字版本是 densitySummary（最密集的區間與最長的沉默），
//     由詳情頁放進時間帶的 aria-describedby。
import type { Cue } from "./cues";

/** 輪廓的區間數（和時間帶的寬度無關，SVG 用 preserveAspectRatio="none" 拉伸） */
export const DENSITY_BINS = 240;
/** 沒有字幕的時間至少這麼長（秒）才算沉默；短的停頓不算 */
export const MIN_SILENCE_S = 5;

function charCount(text: string): number {
  let n = 0;
  for (const ch of text) if (!/\s/u.test(ch)) n++;
  return n;
}

/**
 * 每個區間的說話密度（字數／秒，依重疊時間加權），已做 1-2-1 平滑。
 * 沒有長度或沒有字幕時回傳空陣列。
 */
export function speechDensity(
  cues: readonly Cue[],
  durationS: number,
  bins = DENSITY_BINS,
): number[] {
  if (!(durationS > 0) || !cues.length || bins < 1) return [];
  const w = durationS / bins;
  const sum = new Array<number>(bins).fill(0);
  for (const c of cues) {
    const start = Math.max(0, c.start);
    const end = Math.min(durationS, c.end);
    if (!(end > start)) continue;
    const rate = charCount(c.text) / (c.end - c.start);
    if (!rate) continue;
    const first = Math.min(bins - 1, Math.floor(start / w));
    const last = Math.min(bins - 1, Math.floor((end - 1e-9) / w));
    for (let i = first; i <= last; i++) {
      const overlap = Math.min(end, (i + 1) * w) - Math.max(start, i * w);
      if (overlap > 0) sum[i]! += rate * overlap;
    }
  }
  const raw = sum.map((s) => s / w);
  return raw.map((v, i) => {
    const a = raw[i - 1] ?? v;
    const b = raw[i + 1] ?? v;
    return (a + 2 * v + b) / 4;
  });
}

/** 平滑輪廓的 SVG 路徑（viewBox 是 0 0 bins 100；高度依最大值正規化，最高到 85%） */
export function densityPaths(values: readonly number[]): { area: string; line: string } | null {
  const max = Math.max(0, ...values);
  if (!values.length || !(max > 0)) return null;
  const pts = values.map((v, i) => `${i + 0.5},${(100 - (v / max) * 85).toFixed(2)}`);
  const n = values.length;
  const first = (100 - (values[0]! / max) * 85).toFixed(2);
  const last = (100 - (values[n - 1]! / max) * 85).toFixed(2);
  const line = `M0,${first} L${pts.join(" L")} L${n},${last}`;
  return { line, area: `${line} L${n},100 L0,100 Z` };
}

export interface DensitySummary {
  /** 最密集的區間（秒） */
  dense: { from: number; to: number };
  /** 最長的沉默（秒）；沒有長於 MIN_SILENCE_S 的沉默時是 null */
  silence: { from: number; to: number } | null;
}

/**
 * 文字版本：最密集的區間（長度約為全長的 1/20，至少一個區間）與最長的沉默（句子之間、開頭與結尾沒有字幕的時間）。
 */
export function densitySummary(
  cues: readonly Cue[],
  durationS: number,
  values: readonly number[],
): DensitySummary | null {
  if (!values.length || !(durationS > 0)) return null;
  const n = values.length;
  const w = durationS / n;
  const win = Math.max(1, Math.round(n / 20));
  let best = -1;
  let at = 0;
  let acc = 0;
  for (let i = 0; i < n; i++) {
    acc += values[i]!;
    if (i >= win) acc -= values[i - win]!;
    if (i >= win - 1 && acc > best) {
      best = acc;
      at = i - win + 1;
    }
  }
  if (!(best > 0)) return null;
  const sorted = [...cues].sort((a, b) => a.start - b.start);
  let silence: { from: number; to: number } | null = null;
  let cursor = 0;
  const consider = (from: number, to: number) => {
    if (to - from >= MIN_SILENCE_S && (!silence || to - from > silence.to - silence.from)) {
      silence = { from, to };
    }
  };
  for (const c of sorted) {
    consider(cursor, Math.min(c.start, durationS));
    cursor = Math.max(cursor, c.end);
  }
  consider(cursor, durationS);
  return { dense: { from: at * w, to: Math.min(durationS, (at + win) * w) }, silence };
}

/** 底軌上的說話密度輪廓（裝飾，父層是 aria-hidden 的底軌） */
export function DensityTrack({ values }: { values: readonly number[] }) {
  const paths = densityPaths(values);
  if (!paths) return null;
  return (
    <svg
      className="item__density"
      data-testid="density"
      viewBox={`0 0 ${values.length} 100`}
      preserveAspectRatio="none"
      focusable="false"
    >
      <path className="item__density-area" d={paths.area} />
      <path className="item__density-line" d={paths.line} />
    </svg>
  );
}
