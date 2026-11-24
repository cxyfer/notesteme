// 登入頁背景的淡化時間帶（pages.md §13）：固定的示意資料、純裝飾（aria-hidden、不能點），
// 登入前不能露出任何實際內容，所以不呼叫任何 API；不用「處理中」（有動畫）的狀態。
import { StatusBand, type StatusBandStage } from "@shared";

// 登入頁背景的示意資料：一部 10 分鐘的影音，字幕與分段完成、校正做到一半、翻譯還沒開始。
// 不是任何真實影音；文字為空（整條在 aria-hidden 中，不會被讀出）。
const RIBBON_DURATION_S = 600;
const RIBBON_STAGES: readonly StatusBandStage[] = [
  { key: "transcript", status: "done", label: "" },
  { key: "segment", status: "done", label: "" },
  {
    key: "correct",
    status: "pending",
    label: "",
    segments: [
      { from: 0, to: 330, status: "done" },
      { from: 330, to: 600, status: "pending" },
    ],
  },
  { key: "translate", status: "pending", label: "" },
];

/** 登入頁背景的淡化時間帶：純裝飾，不能聚焦、不能點，也不被讀出。 */
export function LoginRibbon() {
  return (
    <div className="login__ribbon" aria-hidden="true">
      <StatusBand stages={RIBBON_STAGES} label="" durationS={RIBBON_DURATION_S} />
    </div>
  );
}
