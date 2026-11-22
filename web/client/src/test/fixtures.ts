// 測試資料：GET /api/items 的列與 GET /api/items/{id} 的詳情。
import type { components } from "../api/schema";

type Item = components["schemas"]["Item"];
type Detail = components["schemas"]["ItemDetail"];
type Stage = components["schemas"]["Stage"];

export function item(n: number, over: Partial<Item> = {}): Item {
  return {
    id: `item-${n}`,
    ref: `yt:vid${String(n).padStart(8, "0")}`,
    platform: "youtube",
    external_id: `vid${String(n).padStart(8, "0")}`,
    url: `https://www.youtube.com/watch?v=vid${String(n).padStart(8, "0")}`,
    title: `影片 ${n}`,
    duration_s: 600 + n,
    published_at: "2026-11-01T00:00:00Z",
    lang_mixed: false,
    overrides: {},
    created_at: "2026-11-20T00:00:00Z",
    channel_id: "UCchan",
    ...over,
  };
}

export function stage(name: string, status: string, over: Partial<Stage> = {}): Stage {
  return {
    stage: name,
    status,
    not_before: null,
    actions: null,
    config_snapshot_id: "",
    started_at: null,
    finished_at: null,
    attempts: 0,
    ...over,
  };
}

export function detail(it: Item, stages: Stage[] = []): Detail {
  return { ...it, stages, sources: [] };
}
