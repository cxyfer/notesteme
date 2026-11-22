// 佇列頁的篩選條件（存在 URL，web/client/README.md「篩選 ↔ URL」）：資源類別與任務狀態。
import type { components } from "../../api/schema";

export type QueueTask = components["schemas"]["QueueTask"];

/** 資源類別（internal/queue，PIPE-R8）；API 回傳其他類別時照樣顯示，但不能當篩選條件 */
export const RESOURCE_CLASSES = ["download", "asr", "llm", "frames"] as const;
export const TASK_STATES = ["leased", "pending"] as const;

export interface QueueSearch {
  class?: (typeof RESOURCE_CLASSES)[number];
  state?: (typeof TASK_STATES)[number];
}

function oneOf<T extends string>(list: readonly T[], v: unknown): T | undefined {
  return typeof v === "string" && (list as readonly string[]).includes(v) ? (v as T) : undefined;
}

export function validateQueueSearch(raw: Record<string, unknown>): QueueSearch {
  const out: QueueSearch = {};
  const c = oneOf(RESOURCE_CLASSES, raw.class);
  if (c) out.class = c;
  const s = oneOf(TASK_STATES, raw.state);
  if (s) out.state = s;
  return out;
}

/** 依條件篩選；執行中的排前面，同狀態維持 API 的順序（領取順序） */
export function filterTasks(tasks: readonly QueueTask[], s: QueueSearch): QueueTask[] {
  return tasks
    .filter((x) => (!s.class || x.resource_class === s.class) && (!s.state || x.state === s.state))
    .map((x, i) => ({ x, i }))
    .sort((a, b) => (a.x.state === b.x.state ? a.i - b.i : a.x.state === "leased" ? -1 : 1))
    .map(({ x }) => x);
}
