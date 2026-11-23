// 測試資料：GET /api/items 的列與 GET /api/items/{id} 的詳情。
import type { components } from "../api/schema";
import {
  costEntry,
  costSummary,
  noteTemplate,
  notifier,
  priceList,
  subscription,
  subscriptionEntries,
} from "./m4Fixtures";

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
    subscription_id: null,
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

type Cue = components["schemas"]["Cue"];
type Run = components["schemas"]["Run"];
type QueueStatus = components["schemas"]["QueueStatus"];

/** 秒 → HH:MM:SS.mmm（API 的時間格式） */
export function tc(s: number): string {
  const ms = Math.round(s * 1000);
  const h = Math.floor(ms / 3_600_000);
  const m = Math.floor((ms % 3_600_000) / 60_000);
  const sec = Math.floor((ms % 60_000) / 1000);
  const rest = ms % 1000;
  const p = (n: number, w = 2) => String(n).padStart(w, "0");
  return `${p(h)}:${p(m)}:${p(sec)}.${p(rest, 3)}`;
}

export function cue(
  index: number,
  start: number,
  end: number,
  text: string,
  over: Partial<Cue> = {},
): Cue {
  return { index, start: tc(start), end: tc(end), text, ...over };
}

export function run(stageName: string, over: Partial<Run> = {}): Run {
  return {
    id: `run-${stageName}`,
    stage: stageName,
    status: "done",
    started_at: "2026-11-20T00:00:00Z",
    finished_at: "2026-11-20T00:01:05Z",
    cache_key: "ck",
    impl_version: 1,
    config: { language: { targets: ["zh-Hant"] }, stages: {} },
    ...over,
  };
}

export function queueStatus(over: Partial<QueueStatus> = {}): QueueStatus {
  return {
    paused: false,
    lanes: [
      { class: "download", limit: 2, running: 1, waiting: 0, paused: false },
      { class: "asr", limit: 1, running: 0, waiting: 0, paused: false },
      { class: "llm", limit: 4, running: 0, waiting: 1, paused: false },
      { class: "frames", limit: 2, running: 0, waiting: 0, paused: false },
    ],
    tasks: [
      {
        id: "task-1",
        item_id: "item-1",
        item: "yt:vid00000001",
        stage: "transcript",
        resource_class: "download",
        state: "leased",
        attempts: 1,
        not_before: null,
      },
      {
        id: "task-2",
        item_id: "item-2",
        item: "yt:vid00000002",
        stage: "correct",
        resource_class: "llm",
        state: "pending",
        attempts: 0,
        not_before: null,
      },
    ],
    ...over,
  };
}

/** 每頁 axe 檢查用的基本替身：詳情、字幕、執行紀錄、輸出、佇列、列表都有資料，不連網路 */
export function defaultRoutes() {
  return {
    "GET /api/items": () => ({ items: [item(1)], total: 1 }),
    "GET /api/items/{id}": (req: { params: Record<string, string> }) =>
      detail(
        item(1, {
          id: req.params.id === "yt:abc123" ? "item-1" : req.params.id,
          original_lang: "zh-Hans",
        }),
        [
          stage("metadata", "done"),
          stage("transcript", "done", { executor: "local" }),
          stage("normalize", "done"),
          stage("segment", "done"),
          stage("correct", "failed", {
            error_class: "permanent",
            message: "驗證失敗",
            actions: ["重試"],
          }),
        ],
      ),
    "GET /api/items/{id}/cues": () => ({
      variant: "corrected",
      lang: "zh-Hans",
      cues: [cue(1, 0, 2, "第一句"), cue(2, 2, 4, "第二句", { timing: "silence" })],
    }),
    "GET /api/items/{id}/runs": () => ({
      runs: [run("correct", { status: "failed" }), run("transcript")],
    }),
    "GET /api/items/{id}/outputs": () => ({ outputs: [] }),
    "GET /api/queue": () => queueStatus(),
    "GET /api/profiles": () => ({ profiles: [] }),
    // UI-4
    "GET /api/subscriptions": () => ({ subscriptions: [subscription("s1")] }),
    "GET /api/subscriptions/{id}": () => subscription("s1"),
    "GET /api/subscriptions/{id}/entries": () => subscriptionEntries(),
    "GET /api/costs": () => costSummary(),
    "GET /api/costs/entries": () => ({ entries: [costEntry(1)] }),
    "GET /api/prices": () => priceList(),
    "GET /api/note-templates": () => ({ templates: [noteTemplate("video-notes")] }),
    "GET /api/notifiers": () => ({ notifiers: [notifier("ops")] }),
    "GET /api/notifications": () => ({ notifications: [] }),
    "GET /api/settings/budget": () => ({ monthly_usd: 20, warn_at_percent: 80, timezone: "UTC" }),
  };
}
