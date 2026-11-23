// UI-7 的測試資料：總覽（LIB-R21）、批次操作（LIB-R13～R20）、垃圾桶。
import type { components } from "../api/schema";

type S = components["schemas"];

export function overview(over: Partial<S["OverviewResult"]> = {}): S["OverviewResult"] {
  return {
    now: {
      items: [
        {
          id: "item-1",
          ref: "yt:abc123",
          title: "影片 1",
          stage: "correct",
          progress: { done: 9, total: 16 },
        },
      ],
      total: 4,
      more: 3,
    },
    needs_you: {
      items: [
        {
          id: "item-2",
          ref: "yt:def456",
          title: "超出預算的影片",
          stage: "translate",
          status: "blocked",
          reason: "budget_blocked",
          message: "本月預算已用完。",
          actions: ["調整預算", "查看成本"],
        },
      ],
      total: 1,
    },
    today: [
      { at: "2026-11-24T06:02:00Z", type: "item.created", item: "yt:abc123", title: "影片 1" },
      { at: "2026-11-24T05:40:00Z", type: "stage.status", item: "yt:def456", title: "影片 2" },
    ],
    recent_done: [
      { id: "item-3", ref: "yt:ghi789", title: "完成的影片", done_at: "2026-11-24T04:00:00Z" },
    ],
    month: {
      month: "2026-11",
      spent_usd: 3.42,
      reserved_usd: 0,
      monthly_usd: 20,
      timezone: "Asia/Taipei",
    },
    disk: {
      data_bytes: 2_000_000_000,
      media_bytes: 1_500_000_000,
      artifacts_bytes: 400_000_000,
      uploads_bytes: 0,
      free_bytes: 50_000_000_000,
      measured_at: "2026-11-24T06:00:00Z",
    },
    notices: [{ code: "terms_pending", message: "有 3 個術語等你確認。", actions: ["去審核"] }],
    ...over,
  };
}

export function emptyOverview(): S["OverviewResult"] {
  return overview({
    now: { items: [], total: 0, more: 0 },
    needs_you: { items: [], total: 0 },
    today: [],
    recent_done: [],
    notices: [],
  });
}
