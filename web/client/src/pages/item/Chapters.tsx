// 章節（pages.md §4、design-language.md §6.1 的「章節」圖層；CHAP-R7 的 GET /api/items/{id} chapters）：
//   - ChapterTrack：時間帶底軌上的章節起點線（裝飾，底軌是 aria-hidden；可及的版本是 ChapterList）。
//   - ChapterList：播放器下的章節清單，點擊跳播，目前的章節標 aria-current。
//   - RangeBar：時間帶拖曳選取範圍後的「複製這段字幕」。
// API 的 Chapter 沒有層級，所以「### 小節只畫下半段」做不到：每個章節都畫整條線（README 10.26 第 2 點）。
import { useTranslation } from "react-i18next";

import { Button, useToast } from "../../components";
import { formatTimecode } from "../../i18n";
import type { components } from "../../api/schema";
import { parseCueTime, type Cue } from "./cues";

export interface ChapterView {
  title: string;
  start: number;
  end: number;
}

/** API 的章節（HH:MM:SS.mmm）→ 秒；時間不正確的章節略過，依開始時間排序 */
export function toChapters(api: readonly components["schemas"]["Chapter"][] | null | undefined) {
  return (api ?? [])
    .map((c) => ({ title: c.title, start: parseCueTime(c.start), end: parseCueTime(c.end) }))
    .filter((c) => Number.isFinite(c.start) && Number.isFinite(c.end))
    .sort((a, b) => a.start - b.start);
}

/** t 所在的章節（最後一個已開始、而且還沒結束的）；不在任何章節內時回傳 -1 */
export function chapterIndexAt(chapters: readonly ChapterView[], t: number): number {
  let found = -1;
  for (let i = 0; i < chapters.length; i++) {
    if (chapters[i]!.start <= t + 0.001) found = i;
    else break;
  }
  if (found < 0) return -1;
  const c = chapters[found]!;
  // 結束時間不大於開始時間時視為延續到下一章
  return c.end > c.start && t >= c.end ? -1 : found;
}

export function ChapterTrack({
  chapters,
  durationS,
}: {
  chapters: readonly ChapterView[];
  durationS: number | null | undefined;
}) {
  const d = durationS && Number.isFinite(durationS) && durationS > 0 ? durationS : 0;
  return (
    <>
      {/* track 取代 StatusBand 預設的點狀基線：還不知道說話密度，所以照樣畫 */}
      <span className="sband__baseline" />
      {d > 0 &&
        chapters
          .filter((c) => c.start > 0 && c.start < d)
          .map((c, i) => (
            <span
              key={`${c.start}-${i}`}
              className="item__chapter-line"
              data-testid="chapter-line"
              style={{ left: `${(c.start / d) * 100}%` }}
            />
          ))}
    </>
  );
}

export function ChapterList({
  chapters,
  time,
  onSeek,
  lang,
}: {
  chapters: readonly ChapterView[];
  time: number;
  onSeek: (seconds: number) => void;
  /** 章節標題的語言（不知道時省略） */
  lang?: string;
}) {
  const { t } = useTranslation();
  if (!chapters.length) return null;
  const current = chapterIndexAt(chapters, time);
  return (
    <section className="chapters" aria-labelledby="chapters-title">
      <h2 id="chapters-title" className="section__title">
        {t("item.chapters.title")}
      </h2>
      <ol className="chapters__list">
        {chapters.map((c, i) => {
          const tc = formatTimecode(c.start);
          return (
            <li key={`${c.start}-${i}`}>
              <button
                type="button"
                className="chapters__item"
                aria-current={i === current ? "true" : undefined}
                aria-label={t("item.chapters.jump", { title: c.title, time: tc })}
                onClick={() => onSeek(c.start)}
              >
                <span className="chapters__name" lang={lang || undefined}>
                  {c.title}
                </span>
                <span className="chapters__time num">{tc}</span>
              </button>
            </li>
          );
        })}
      </ol>
    </section>
  );
}

/** 和 [from, to) 有重疊的句子（OUT-R3 的語意），每句一行、不含時間碼（和 txt 下載相同） */
export function cuesInRange(cues: readonly Cue[], from: number, to: number): string {
  return cues
    .filter((c) => c.end > from && c.start < to)
    .map((c) => c.text.replace(/\s*\n\s*/g, " "))
    .join("\n");
}

export function RangeBar({
  range,
  cues,
  onClear,
}: {
  range: { from: number; to: number };
  /** 目前顯示模式的字幕；還沒有字幕時是 undefined */
  cues: readonly Cue[] | undefined;
  onClear: () => void;
}) {
  const { t } = useTranslation();
  const toast = useToast();
  const text = cues ? cuesInRange(cues, range.from, range.to) : "";
  const count = text ? text.split("\n").length : 0;

  async function copy() {
    try {
      // navigator.clipboard 只在安全的來源（https、localhost）存在
      if (!navigator.clipboard) throw new Error("clipboard unavailable");
      await navigator.clipboard.writeText(text);
      toast.show({ title: t("item.range.copied", { count }) });
    } catch {
      toast.show({ title: t("item.range.copyFailed") });
    }
  }

  return (
    <div className="item__range" role="group" aria-label={t("item.range.label")}>
      <span className="num">
        {t("item.range.selected", {
          from: formatTimecode(range.from),
          to: formatTimecode(range.to),
        })}
      </span>
      <Button variant="outline" disabled={!count} onClick={() => void copy()}>
        {count ? t("item.range.copy", { count }) : t("item.range.noCues")}
      </Button>
      <Button variant="text" onClick={onClear}>
        {t("item.range.clear")}
      </Button>
    </div>
  );
}
