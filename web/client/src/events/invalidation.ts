// 事件型別 → 要失效的 TanStack Query key（ADR-0008 決策 5）。新增事件或資源時更新這張表。
//
// | 事件            | 失效的 query                          | 說明 |
// |-----------------|---------------------------------------|------|
// | stage.status    | items.detail(id)、queue               | 不讓列表失效：列表的步驟狀態來自每列的 detail，只有那一列原地更新（LIB-R9） |
// | item.created    | queue                                 | 不讓列表失效：媒體庫只顯示「有 N 個新項目」（README §3.4，UI-1 用 useServerEvent 計數） |
// | item.deleted    | items.detail(id)、items.lists、trash、queue | |
// | item.restored   | items.detail(id)、items.lists、trash  | |
// | item.purged     | trash                                 | 影音已不存在 |
//
// 每次（重新）連上時，detail、queue、trash 失效：斷線期間的事件收不到（匯流排會斷開跟不上的訂閱者）。
// 列表不自動重抓（會插入新項目、打亂捲動位置）；媒體庫用 useStreamReconnect 提示「列表可能不是最新的」，
// 由使用者決定何時重新載入。
import type { QueryKey } from "@tanstack/react-query";

import { queryKeys } from "../api/queryKeys";
import type { EventType, ServerEvent } from "./types";

export const invalidationTable: Record<EventType, (ev: ServerEvent) => QueryKey[]> = {
  "stage.status": (ev) => [queryKeys.items.detail(ev.item_id), queryKeys.queue],
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
};

/** 重新連上時要失效的前綴。 */
export const reconnectInvalidations: QueryKey[] = [
  queryKeys.items.details(),
  queryKeys.queue,
  queryKeys.trash,
];
