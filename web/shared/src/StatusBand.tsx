// 時間帶的步驟帶（design-language.md §6.1）：Client（web/client）與之後的 Hub（web/hub）共用。
//
// 這個檔案只依賴 React：不依賴 router、API client 或 i18n 實例，所有文字都由呼叫端用 props 傳入；
// 顏色用設計 token 的 CSS 變數（web/client/src/styles/tokens.css 的名稱），由使用端提供。
//
// 精簡版（variant="compact"，UI-1）：底軌加上每個步驟一條 3px 的帶子，用在媒體庫的列，role="img"。
//   有 onSeek 時（pages.md §1、§3：點時間帶打開詳情並定位到該時間），role="img" 的元素不變、不加互動，
//   另外疊一個透明的 <button>（aria-label 由呼叫端提供）接收點擊：點擊位置的比例乘上影音長度；
//   用鍵盤按下時沒有位置，傳 0（從頭打開）。
// 完整版（variant="full"，UI-2）：底軌 72px、時間刻度、6px 的步驟帶與右側逐列對齊的標籤、播放頭；
// 整條是 role="slider"（aria-valuetext 是時間碼），點擊、方向鍵移動播放頭，hover 顯示該時間的說明。
// 新的圖層用新的選用 props 加入，不改變既有 props 的意義。
// 完整版的選用互動（2026-11-25）：hover 的章節名稱（chapterAt）、Shift + ←／→（onShiftStep）、
// 在底軌上拖曳選取範圍（onSelectRange、selection）。

import {
  useRef,
  useState,
  type KeyboardEvent,
  type MouseEvent,
  type PointerEvent,
  type ReactNode,
} from "react";

/** 步驟帶的狀態：store 的步驟狀態（`blocked` 的原因由呼叫端放進 label）。 */
export type BandStatus =
  | "pending"
  | "running"
  | "waiting"
  | "awaiting_agent"
  | "done"
  | "skipped"
  | "failed"
  | "blocked";

/** 依時間範圍細分的狀態，例如 LLM 的 chunk（秒，from < to）。 */
export interface BandSegment {
  from: number;
  to: number;
  status: BandStatus;
}

export interface StatusBandStage {
  /** 穩定的識別（步驟名稱），當作 React key 與 data-stage */
  key: string;
  status: BandStatus;
  /** 這條帶子的文字（例如「校正：失敗」），當作 tooltip；整條的摘要另外用 StatusBandProps.label */
  label: string;
  /**
   * 依時間範圍的細分；省略時整條同一個狀態。
   * 需要 durationS，換算成百分比；超出 [0, durationS] 的部分會被裁掉。
   */
  segments?: readonly BandSegment[];
}

export interface StatusBandProps {
  stages: readonly StatusBandStage[];
  /** 整條帶子的文字摘要（role="img" 的 aria-label），例如「字幕完成，校正 9/16 段，翻譯未開始」 */
  label: string;
  /** 影音長度（秒）；畫 segments 時需要。null／undefined 時 segments 被忽略，整條用 stage.status */
  durationS?: number | null;
  /** compact：媒體庫的列（role="img"）；full：詳情頁頂部（role="slider"，需要 durationS 與 formatTime） */
  variant?: "compact" | "full";
  /** 底軌上的內容（說話密度、章節）；省略時畫一條點狀基線，代表還不知道說話密度 */
  track?: ReactNode;
  className?: string;
  /**
   * full：點擊、方向鍵移動播放頭時呼叫（秒，已限制在 [0, durationS]）。
   * compact：點擊帶子時呼叫（點擊位置的比例 × durationS；鍵盤按下時是 0）；需要 durationS 與 seekLabel，
   * 沒有長度時不提供點擊。
   */
  onSeek?: (seconds: number) => void;
  /** compact 有 onSeek 時，疊在帶子上的按鈕的 aria-label（例如「打開『…』並定位到點擊的時間」） */
  seekLabel?: string;
  /** compact 的按鈕的 tabIndex（列表的 roving focus 用） */
  seekTabIndex?: number;
  // ---- 以下只用在 full ----
  /** 播放頭的位置（秒） */
  position?: number;
  /** 時間碼的格式（呼叫端提供，web/shared 不依賴 i18n）；刻度、aria-valuetext、hover 都用它 */
  formatTime?: (seconds: number) => string;
  /** hover 時顯示在時間碼下方的說明（例如那個時間點的字幕句）；回傳空字串時不顯示 */
  describeTime?: (seconds: number) => string;
  /** describeTime 的內容語言（例如字幕的語言），設在說明元素的 lang（NFR-I18N-3） */
  describeLang?: string;
  /** ←／→ 移動的秒數，預設 5（design-language.md §6.1）；PageUp／PageDown 是它的 6 倍 */
  stepS?: number;
  /** full 的 aria-valuetext 前後的文字，例如「播放位置 01:05」；省略時只有時間碼 */
  valueText?: (formatted: string) => string;
  /** hover 時顯示在時間碼下方、describeTime 之前的章節名稱；回傳空字串時不顯示 */
  chapterAt?: (seconds: number) => string;
  /** Shift + ←／→ 時呼叫（例如跳到上一個或下一個章節）；省略時 Shift + 方向鍵和沒有 Shift 相同 */
  onShiftStep?: (dir: -1 | 1) => void;
  /**
   * 在底軌上拖曳選取時間範圍後呼叫（秒，from < to）。省略時不能拖曳選取。
   * 移動不到 DRAG_MIN_PX 的拖曳仍是點擊；選取結束的那次 click 不會移動播放頭。
   */
  onSelectRange?: (from: number, to: number) => void;
  /** 目前選取的範圍（秒），畫在底軌上；null／undefined 時不畫 */
  selection?: { from: number; to: number } | null;
}

/** 拖曳超過這個距離（px）才算選取範圍，避免手抖把點擊變成選取 */
export const DRAG_MIN_PX = 4;

function pct(v: number, d: number): string {
  return `${(Math.min(Math.max(v, 0), d) / d) * 100}%`;
}

/** 依時間範圍畫出的區段；沒有可用的 segments 時整條一個區段。 */
function bandSegments(
  stage: StatusBandStage,
  d: number | null | undefined,
): BandSegment[] {
  if (!stage.segments?.length || !d || !Number.isFinite(d) || d <= 0) {
    return [{ from: 0, to: 1, status: stage.status }];
  }
  return stage.segments
    .filter(
      (s) => Number.isFinite(s.from) && Number.isFinite(s.to) && s.to > s.from,
    )
    .map((s) => ({
      from: s.from / d,
      to: Math.min(s.to, d) / d,
      status: s.status,
    }));
}

export function StatusBand(props: StatusBandProps) {
  if (props.variant === "full") return <FullBand {...props} />;
  return <CompactBand {...props} />;
}

function CompactBand({
  stages,
  label,
  durationS,
  variant = "compact",
  track,
  className,
  onSeek,
  seekLabel,
  seekTabIndex,
}: StatusBandProps) {
  const d =
    durationS && Number.isFinite(durationS) && durationS > 0 ? durationS : 0;
  const seekable = !!onSeek && d > 0 && !!seekLabel;
  const cls = [
    "sband",
    `sband--${variant}`,
    seekable && "sband--seekable",
    className,
  ]
    .filter(Boolean)
    .join(" ");
  const bands = <CompactBands stages={stages} durationS={d} track={track} />;
  // 沒有點擊時維持原本的結構：最外層就是 role="img"
  if (!seekable)
    return (
      <div className={cls} role="img" aria-label={label}>
        {bands}
      </div>
    );
  return (
    <div className={cls}>
      <div className="sband__img" role="img" aria-label={label}>
        {bands}
      </div>
      <button
        type="button"
        className="sband__seek"
        aria-label={seekLabel}
        tabIndex={seekTabIndex}
        onClick={(e) => {
          const r = e.currentTarget.getBoundingClientRect();
          // 鍵盤（detail 0）或量不到寬度時沒有位置：從頭打開
          const t =
            e.detail === 0 || r.width <= 0
              ? 0
              : clamp(((e.clientX - r.left) / r.width) * d, d);
          onSeek(t);
        }}
      />
    </div>
  );
}

function CompactBands({
  stages,
  durationS,
  track,
}: Pick<StatusBandProps, "stages" | "durationS" | "track">) {
  // 略過的步驟不畫帶子（§6.1 的狀態表），文字版本由呼叫端的狀態欄提供。
  const visible = stages.filter((s) => s.status !== "skipped");
  return (
    <>
      <div className="sband__track" aria-hidden="true">
        {track ?? <span className="sband__baseline" />}
      </div>
      <div className="sband__bands" aria-hidden="true">
        {visible.map((s) => (
          <div
            className="sband__band"
            key={s.key}
            data-stage={s.key}
            title={s.label}
          >
            {bandSegments(s, durationS).map((seg, i) => (
              <span
                key={i}
                className={`sband__seg sband__seg--${seg.status}`}
                data-status={seg.status}
                style={{
                  left: pct(seg.from, 1),
                  width: pct(seg.to - seg.from, 1),
                }}
              />
            ))}
          </div>
        ))}
      </div>
    </>
  );
}

/** 刻度間距：讓整條大約有 4～10 個刻度 */
const TICK_STEPS = [
  5, 10, 15, 30, 60, 120, 300, 600, 900, 1200, 1800, 3600, 7200,
];

export function tickInterval(d: number): number {
  for (const step of TICK_STEPS) if (d / step <= 10) return step;
  return TICK_STEPS[TICK_STEPS.length - 1]!;
}

function clamp(v: number, d: number) {
  return Math.min(Math.max(v, 0), d);
}

function FullBand({
  stages,
  label,
  durationS,
  track,
  className,
  position = 0,
  onSeek,
  formatTime = (s) => String(Math.floor(s)),
  describeTime,
  describeLang,
  stepS = 5,
  valueText,
  chapterAt,
  onShiftStep,
  onSelectRange,
  selection,
}: StatusBandProps) {
  const d =
    durationS && Number.isFinite(durationS) && durationS > 0 ? durationS : 0;
  const visible = stages.filter((s) => s.status !== "skipped");
  const [hover, setHover] = useState<number | null>(null);
  // 拖曳選取：按下的位置與時間、是否已經移動到算選取；選取結束後吞掉同一次的 click
  const drag = useRef<{
    id: number;
    x: number;
    t: number;
    moved: boolean;
  } | null>(null);
  const swallowClick = useRef(false);
  const [dragging, setDragging] = useState<{ from: number; to: number } | null>(
    null,
  );
  const pos = clamp(position, d);
  const cls = ["sband", "sband--full", className].filter(Boolean).join(" ");

  function timeAt(
    e: MouseEvent<HTMLElement> | PointerEvent<HTMLElement>,
  ): number | null {
    const r = e.currentTarget.getBoundingClientRect();
    if (!d || r.width <= 0) return null;
    return clamp(((e.clientX - r.left) / r.width) * d, d);
  }

  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    if (!d || e.altKey || e.ctrlKey || e.metaKey) return;
    if (
      e.shiftKey &&
      onShiftStep &&
      (e.key === "ArrowLeft" || e.key === "ArrowRight")
    ) {
      e.preventDefault();
      onShiftStep(e.key === "ArrowLeft" ? -1 : 1);
      return;
    }
    const next = {
      ArrowLeft: pos - stepS,
      ArrowDown: pos - stepS,
      ArrowRight: pos + stepS,
      ArrowUp: pos + stepS,
      PageDown: pos - stepS * 6,
      PageUp: pos + stepS * 6,
      Home: 0,
      End: d,
    }[e.key];
    if (next === undefined) return;
    e.preventDefault();
    onSeek?.(clamp(next, d));
  }

  const ticks: number[] = [];
  if (d)
    for (let t = 0, step = tickInterval(d); t < d; t += step) ticks.push(t);
  // 正在處理或失敗的時間範圍，也淡淡標在底軌上（§6.1 的「處理範圍」圖層）
  const ranges = d
    ? visible.flatMap((s) =>
        (s.segments ?? [])
          .filter((seg) => seg.status === "running" || seg.status === "failed")
          .map((seg) => ({ ...seg, key: s.key })),
      )
    : [];
  const formatted = formatTime(pos);
  const hoverText = hover !== null ? describeTime?.(hover) : undefined;
  const hoverChapter = hover !== null ? chapterAt?.(hover) : undefined;
  const shown = dragging ?? selection;

  return (
    <div className={cls}>
      <div className="sband__ticks" aria-hidden="true">
        {ticks.map((t) => (
          <span key={t} className="sband__tick" style={{ left: pct(t, d) }}>
            {formatTime(t)}
          </span>
        ))}
        {d > 0 && (
          <span
            className="sband__tick sband__tick--end"
            style={{ left: "100%" }}
          >
            {formatTime(d)}
          </span>
        )}
      </div>
      <div className="sband__body">
        <div
          className="sband__slider"
          role="slider"
          tabIndex={d ? 0 : -1}
          aria-label={label}
          aria-valuemin={0}
          aria-valuemax={Math.round(d)}
          aria-valuenow={Math.round(pos)}
          aria-valuetext={valueText ? valueText(formatted) : formatted}
          aria-disabled={d ? undefined : true}
          onKeyDown={onKeyDown}
          onClick={(e) => {
            if (swallowClick.current) {
              swallowClick.current = false;
              return;
            }
            const t = timeAt(e);
            if (t !== null) onSeek?.(t);
          }}
          onPointerDown={(e) => {
            swallowClick.current = false;
            if (!onSelectRange || e.button !== 0 || !e.isPrimary) return;
            const t = timeAt(e);
            if (t === null) return;
            drag.current = { id: e.pointerId, x: e.clientX, t, moved: false };
            e.currentTarget.setPointerCapture?.(e.pointerId);
          }}
          onPointerMove={(e) => {
            const t = timeAt(e);
            setHover(t);
            const g = drag.current;
            if (!g || g.id !== e.pointerId || t === null) return;
            if (!g.moved && Math.abs(e.clientX - g.x) < DRAG_MIN_PX) return;
            g.moved = true;
            setDragging({ from: Math.min(g.t, t), to: Math.max(g.t, t) });
          }}
          onPointerUp={(e) => {
            const g = drag.current;
            drag.current = null;
            if (!g || g.id !== e.pointerId) return;
            if (e.currentTarget.hasPointerCapture?.(e.pointerId))
              e.currentTarget.releasePointerCapture(e.pointerId);
            if (!g.moved) return;
            swallowClick.current = true;
            setDragging(null);
            const t = timeAt(e) ?? g.t;
            const from = Math.min(g.t, t);
            const to = Math.max(g.t, t);
            if (to > from) onSelectRange?.(from, to);
          }}
          onPointerCancel={() => {
            drag.current = null;
            setDragging(null);
          }}
          onPointerLeave={() => setHover(null)}
        >
          <div className="sband__track" aria-hidden="true">
            {track ?? <span className="sband__baseline" />}
            {d > 0 && shown && shown.to > shown.from && (
              <span
                className="sband__selection"
                style={{
                  left: pct(shown.from, d),
                  width: pct(Math.min(shown.to, d) - shown.from, d),
                }}
              />
            )}
            {ranges.map((r, i) => (
              <span
                key={`${r.key}-${i}`}
                className={`sband__range sband__range--${r.status}`}
                style={{
                  left: pct(r.from, d),
                  width: pct(Math.min(r.to, d) - r.from, d),
                }}
              />
            ))}
          </div>
          <div className="sband__bands" aria-hidden="true">
            {visible.map((s) => (
              <div className="sband__band" key={s.key} data-stage={s.key}>
                {bandSegments(s, d).map((seg, i) => (
                  <span
                    key={i}
                    className={`sband__seg sband__seg--${seg.status}`}
                    data-status={seg.status}
                    style={{
                      left: pct(seg.from, 1),
                      width: pct(seg.to - seg.from, 1),
                    }}
                  />
                ))}
              </div>
            ))}
          </div>
          {d > 0 && (
            <span
              className="sband__playhead"
              aria-hidden="true"
              style={{ left: pct(pos, d) }}
            />
          )}
          {hover !== null && (
            <span
              className="sband__hover"
              aria-hidden="true"
              style={{ left: pct(hover, d) }}
            >
              <span className="sband__hover-time">{formatTime(hover)}</span>
              {hoverChapter && (
                <span className="sband__hover-chapter">{hoverChapter}</span>
              )}
              {hoverText && (
                <span
                  className="sband__hover-text"
                  lang={describeLang || undefined}
                >
                  {hoverText}
                </span>
              )}
            </span>
          )}
        </div>
        <div className="sband__labels">
          {visible.map((s) => (
            <span className="sband__label" key={s.key} data-stage={s.key}>
              {s.label}
            </span>
          ))}
        </div>
      </div>
    </div>
  );
}
