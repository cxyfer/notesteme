// 步驟狀態 → 畫面上的狀態（StatusBadge、StatusBand、狀態欄的文字）。
import type { components } from "../../api/schema";
import type { Status } from "../../components";
import type { BandStatus } from "@shared";

import { STAGES, type ErrorClass, type Stage } from "./filters";

export type ApiStage = components["schemas"]["Stage"];
export type ApiItem = components["schemas"]["Item"];
export type ApiItemDetail = components["schemas"]["ItemDetail"];

/** 精簡版時間帶畫的步驟（design-language.md §6.1：字幕、校正、翻譯） */
export const BAND_STAGES = [
  "transcript",
  "correct",
  "translate",
] as const satisfies readonly Stage[];

/** blocked 依原因對應到三種狀態；認不得的原因一律當成「需要你處理」 */
export function badgeStatus(s: Pick<ApiStage, "status" | "block_reason">): Status {
  switch (s.status) {
    case "blocked":
      if (s.block_reason === "budget_blocked") return "budget_blocked";
      if (s.block_reason === "awaiting_user") return "awaiting_user";
      return "needs_action";
    case "pending":
    case "running":
    case "waiting":
    case "awaiting_agent":
    case "done":
    case "skipped":
    case "failed":
      return s.status;
    default:
      return "pending";
  }
}

export function bandStatus(s: Pick<ApiStage, "status"> | undefined): BandStatus {
  switch (s?.status) {
    case "running":
    case "waiting":
    case "awaiting_agent":
    case "done":
    case "skipped":
    case "failed":
    case "blocked":
      return s.status;
    default:
      return "pending";
  }
}

/** 狀態欄要顯示哪一個步驟：例外狀態優先（失敗 > 需要處理 > 等待 Agent > 處理中 > 稍後檢查），否則是最後一個有進展的步驟 */
const PRIORITY: Record<string, number> = {
  failed: 6,
  blocked: 5,
  awaiting_agent: 4,
  running: 3,
  waiting: 2,
};

export interface Headline {
  stage: Stage;
  /** 沒有任何執行紀錄時是 undefined（剛新增，還沒開始） */
  run?: ApiStage;
  status: Status;
}

export function headline(stages: readonly ApiStage[] | null | undefined): Headline {
  const known = (stages ?? []).filter((s): s is ApiStage & { stage: Stage } =>
    (STAGES as readonly string[]).includes(s.stage),
  );
  let best: (ApiStage & { stage: Stage }) | undefined;
  for (const s of known) {
    const p = PRIORITY[s.status] ?? 0;
    if (p > 0 && (!best || p > (PRIORITY[best.status] ?? 0))) best = s;
  }
  if (best) return { stage: best.stage, run: best, status: badgeStatus(best) };
  // 沒有例外：依步驟順序取最後一個不是 pending／skipped 的步驟
  const ordered = [...known].sort((a, b) => STAGES.indexOf(a.stage) - STAGES.indexOf(b.stage));
  const progressed = ordered.filter((s) => s.status === "done");
  const last = progressed.at(-1);
  if (last) return { stage: last.stage, run: last, status: "done" };
  return { stage: "metadata", run: ordered[0], status: "pending" };
}

/** 需要使用者注意的列：失敗或停下來等使用者 */
export function needsAttention(h: Headline): boolean {
  return h.run?.status === "failed" || h.run?.status === "blocked";
}

/** stage.error_class → ErrorCallout 的類別 */
export function calloutCategory(
  errorClass: string | undefined,
): "retryable" | "wait" | "needs_action" | "fatal" {
  switch (errorClass as ErrorClass | undefined) {
    case "retryable":
      return "retryable";
    case "wait":
      return "wait";
    case "permanent":
      return "fatal";
    default:
      return "needs_action";
  }
}
