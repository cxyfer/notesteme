// 媒體庫的批次操作（LIB-R13～R20、L-M10～M15）：POST /api/items/batch 的呼叫與分批執行。
//
//   - 先 dry_run（一次送全部，最多 500 部）取得數量、預估費用、預算與刪除的影響範圍。
//   - 實際執行分批送：可以顯示進度，「取消」在目前這批完成後停止，沒送出的影音在結果中標成 cancelled。
//     代價是每批各寫一筆 items.batch 事件。
//   - 每部是獨立的交易（LIB-R15）：部分失敗時逐項列出原因。
import { api } from "../../api/client";
import type { components } from "../../api/schema";

export type BatchAction =
  "rerun" | "change_profile" | "delete" | "mark_read" | "mark_unread" | "export_bundle";
/** 介面上的動作：批次 API 的動作，加上「等你決定」的繼續處理（POST /api/items/continue，DIG-R7） */
export type BulkAction = BatchAction | "continue";
export type BatchResult = components["schemas"]["BatchResult"];
export type BatchItem = components["schemas"]["BatchItem"];

/** 伺服器的上限（LIB-R13） */
export const MAX_IDS = 500;

export interface BatchParams {
  from?: string;
  profile?: string;
  delete_outputs?: boolean;
}

/** 只留這個動作用得到的參數（delete_outputs 只在實際執行時送） */
export function paramsFor(action: BatchAction, p: BatchParams, dry: boolean): BatchParams {
  if (action === "rerun") return { from: p.from };
  if (action === "change_profile") return { from: p.from, profile: p.profile };
  if (action === "delete" && !dry && p.delete_outputs) return { delete_outputs: true };
  return {};
}

export function dryRun(action: BatchAction, ids: readonly string[], params: BatchParams) {
  return api.post("/api/items/batch", {
    body: { action, ids: [...ids], ...paramsFor(action, params, true), dry_run: true },
  }) as Promise<BatchResult>;
}

/** 一批的大小：刪除每部最多等 30 秒（L-M15），一批少一點，取消才來得及；繼續處理的 API 上限是 100 */
export function chunkSize(action: BulkAction) {
  return action === "delete" ? 10 : 50;
}

export interface RunOutcome {
  items: BatchItem[];
  succeeded: number;
  failed: number;
  cancelled: number;
  error?: unknown;
}

/**
 * 分批執行；shouldStop() 為 true 時不再送下一批。某一批的請求失敗（網路、4xx、5xx）時也停下。
 * 停下的那一批起都沒有結果，標成 cancelled（出錯的那批伺服器可能做了一部分，以重新載入後的列表為準）。
 */
export async function runChunks(
  ids: readonly string[],
  size: number,
  send: (chunk: string[]) => Promise<BatchItem[]>,
  hooks: {
    onProgress?: (done: number, total: number) => void;
    shouldStop?: () => boolean;
    cancelledMessage: string;
  },
): Promise<RunOutcome> {
  const items: BatchItem[] = [];
  let error: unknown;
  let i = 0;
  for (; i < ids.length; i += size) {
    if (hooks.shouldStop?.()) break;
    try {
      items.push(...(await send(ids.slice(i, i + size))));
    } catch (err) {
      error = err;
      break;
    }
    hooks.onProgress?.(Math.min(i + size, ids.length), ids.length);
  }
  const rest = ids.slice(i);
  for (const id of rest)
    items.push({ id, ref: id, ok: false, code: "cancelled", message: hooks.cancelledMessage });
  const succeeded = items.filter((x) => x.ok).length;
  return { items, succeeded, failed: items.length - succeeded, cancelled: rest.length, error };
}

export async function sendBatch(action: BatchAction, chunk: string[], params: BatchParams) {
  const out = (await api.post("/api/items/batch", {
    body: { action, ids: chunk, ...paramsFor(action, params, false) },
  })) as BatchResult;
  return out.items ?? [];
}

/** 繼續處理（DIG-R7）的結果換成批次的逐項格式；略過的原因由呼叫端翻譯 */
export async function sendContinue(chunk: string[], skipMessage: (code: string) => string) {
  const out = await api.post("/api/items/continue", { body: { ids: chunk } });
  const items: BatchItem[] = (out?.continued ?? []).map((id) => ({ id, ref: id, ok: true }));
  for (const s of out?.skipped ?? [])
    items.push({ id: s.id, ref: s.id, ok: false, code: s.code, message: skipMessage(s.code) });
  return items;
}
