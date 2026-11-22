// 事件型別 → 要失效的 TanStack Query key（ADR-0008 決策 5）。新增事件或資源時更新這張表。
//
// | 事件            | 失效的 query                          | 說明 |
// |-----------------|---------------------------------------|------|
// | stage.status    | items.detail(id)、items.lists、queue  | 列表的列原地更新，順序不變 |
// | item.created    | queue                                 | 不讓列表失效：媒體庫只顯示「有 N 個新項目」（README §3.4，UI-1 用 useServerEvent 計數） |
// | item.deleted    | items.detail(id)、items.lists、trash、queue | |
// | item.restored   | items.detail(id)、items.lists、trash  | |
// | item.purged     | trash                                 | 影音已不存在 |
//
// 每次（重新）連上時，所有前綴都失效：斷線期間的事件收不到（匯流排會斷開跟不上的訂閱者）。
import type { QueryKey } from "@tanstack/react-query";

import { queryKeys } from "../api/queryKeys";
import type { EventType, ServerEvent } from "./types";

export const invalidationTable: Record<EventType, (ev: ServerEvent) => QueryKey[]> = {
  "stage.status": (ev) => [
    queryKeys.items.detail(ev.item_id),
    queryKeys.items.lists(),
    queryKeys.queue,
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
};

/** 重新連上時要失效的前綴。 */
export const reconnectInvalidations: QueryKey[] = [
  queryKeys.items.all,
  queryKeys.queue,
  queryKeys.trash,
];
