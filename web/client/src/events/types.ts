// GET /api/events 的事件（internal/events/bus.go）。SSE 用具名事件：`event: <type>`，data 是這個 JSON。
export const EVENT_TYPES = [
  "stage.status",
  "item.created",
  "item.deleted",
  "item.restored",
  "item.purged",
] as const;
export type EventType = (typeof EVENT_TYPES)[number];

export interface ServerEvent {
  type: EventType;
  /** 影音的短代號（例如 yt:abc123xxxxx） */
  item: string;
  /** 影音的內部 ID */
  item_id: string;
  stage?: string;
  status?: string;
  title?: string;
}

export function parseEvent(data: string): ServerEvent | undefined {
  try {
    const v = JSON.parse(data) as Partial<ServerEvent>;
    if (typeof v.type !== "string" || !(EVENT_TYPES as readonly string[]).includes(v.type))
      return undefined;
    return { item: "", item_id: "", ...v } as ServerEvent;
  } catch {
    return undefined;
  }
}
