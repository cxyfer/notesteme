// 時間帶的步驟帶（design-language.md §6.1）：Client（web/client）與之後的 Hub（web/hub）共用。
//
// 這個檔案只依賴 React：不依賴 router、API client 或 i18n 實例，所有文字都由呼叫端用 props 傳入；
// 顏色用設計 token 的 CSS 變數（web/client/src/styles/tokens.css 的名稱），由使用端提供。
//
// UI-1 只用精簡版（variant="compact"）：底軌加上每個步驟一條 3px 的帶子，用在媒體庫的列。
// UI-2 的完整版（底軌 72px、時間刻度、說話密度、章節、畫面、播放頭）在這個元件上擴充：
// 新的圖層用新的選用 props 加入，不改變既有 props 的意義。

import type { ReactNode } from "react";

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
  /** 目前只有 compact；UI-2 加入 full */
  variant?: "compact";
  /** 底軌上的內容（UI-2 的說話密度、章節）；省略時畫一條點狀基線，代表還不知道說話密度 */
  track?: ReactNode;
  className?: string;
}

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

export function StatusBand({
  stages,
  label,
  durationS,
  variant = "compact",
  track,
  className,
}: StatusBandProps) {
  // 略過的步驟不畫帶子（§6.1 的狀態表），文字版本由呼叫端的狀態欄提供。
  const visible = stages.filter((s) => s.status !== "skipped");
  const cls = ["sband", `sband--${variant}`, className]
    .filter(Boolean)
    .join(" ");
  return (
    <div className={cls} role="img" aria-label={label}>
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
    </div>
  );
}
