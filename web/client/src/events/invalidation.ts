// 事件型別 → 要失效的 TanStack Query key（ADR-0008 決策 5）。新增事件或資源時更新這張表。
//
// | 事件            | 失效的 query                          | 說明 |
// |-----------------|---------------------------------------|------|
// | stage.status    | items.detail(id)、items.runs(id)、queue；產生字幕的步驟 done 時加 items.cuesOf(id)；publish done 時加 items.outputs(id) | 不讓列表失效：列表的步驟狀態來自每列的 detail，只有那一列原地更新（LIB-R9）。字幕只在有新版本時重抓（UI-2） |
// | item.created    | queue                                 | 不讓列表失效：媒體庫只顯示「有 N 個新項目」（README §3.4，UI-1 用 useServerEvent 計數） |
// | item.deleted    | items.detail(id)、items.lists、trash、queue | |
// | item.restored   | items.detail(id)、items.lists、trash  | |
// | item.purged     | trash                                 | 影音已不存在 |
// | subscription.checked／check_failed | subscriptions（列表、單筆、條目） | UI-4 |
// | stage.status 的 frames／note／publish done | items.frames(id)、items.note(id) | UI-4：畫面與筆記分頁 |
//
// 每次（重新）連上時，detail、字幕、執行紀錄、輸出、queue、trash 失效：斷線期間的事件收不到（匯流排會斷開跟不上的訂閱者）。
// 列表不自動重抓（會插入新項目、打亂捲動位置）；媒體庫用 useStreamReconnect 提示「列表可能不是最新的」，
// 由使用者決定何時重新載入。
import type { QueryKey } from "@tanstack/react-query";

import { queryKeys } from "../api/queryKeys";
import type { EventType, ServerEvent } from "./types";

/** 完成時會讓畫面或筆記分頁失效的步驟（publish 會改寫入狀態） */
const NOTE_STAGES = new Set(["frames", "note", "publish"]);

/** 完成時會產生新字幕版本的步驟 */
export const CUE_STAGES = new Set(["transcript", "normalize", "segment", "correct", "translate"]);

export const invalidationTable: Record<EventType, (ev: ServerEvent) => QueryKey[]> = {
  "stage.status": (ev) => [
    queryKeys.items.detail(ev.item_id),
    queryKeys.items.runs(ev.item_id),
    queryKeys.queue,
    ...(ev.status === "done" && CUE_STAGES.has(ev.stage ?? "")
      ? [queryKeys.items.cuesOf(ev.item_id)]
      : []),
    ...(ev.status === "done" && ev.stage === "publish"
      ? [queryKeys.items.outputs(ev.item_id)]
      : []),
    ...(ev.status === "done" && NOTE_STAGES.has(ev.stage ?? "")
      ? [queryKeys.items.frames(ev.item_id), queryKeys.items.note(ev.item_id)]
      : []),
  ],
  "item.created": () => [queryKeys.queue],
  "item.deleted": (ev) => [
    queryKeys.items.detail(ev.item_id),
    queryKeys.items.lists(),
    queryKeys.trash,
    queryKeys.queue,
  ],
  "item.restored": (ev) => [
    queryKeys.items.detail(ev.item_id),
    queryKeys.items.lists(),
    queryKeys.trash,
  ],
  "item.purged": () => [queryKeys.trash],
  "subscription.checked": () => [queryKeys.subscriptions.all],
  "subscription.check_failed": () => [queryKeys.subscriptions.all],
};

/** 重新連上時要失效的前綴。 */
export const reconnectInvalidations: QueryKey[] = [
  queryKeys.items.details(),
  queryKeys.items.allCues(),
  queryKeys.items.allRuns(),
  queryKeys.items.allOutputs(),
  queryKeys.items.allFrames(),
  queryKeys.items.allNotes(),
  queryKeys.subscriptions.all,
  queryKeys.queue,
  queryKeys.trash,
];
