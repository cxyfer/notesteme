// UI-5 的測試資料：搜尋、術語審核、彙整、系統告警與備份。
import type { components } from "../api/schema";

type S = components["schemas"];

export function searchResult(n: number, over: Partial<S["SearchResult"]> = {}): S["SearchResult"] {
  return {
    item: { id: `item-${n}`, ref: `yt:item${n}`, title: `影片 ${n}` },
    lang: "en",
    variant: "corrected",
    cue: 2,
    start: "00:00:04.000",
    end: "00:00:08.000",
    text: "we keep an ordered log of events",
    highlights: [{ start: 11, end: 22 }],
    before: "first we write",
    after: "and replay it",
    ...over,
  };
}

export function termReview(
  id: string,
  term: string,
  channel = "c1",
  over: Partial<S["TermCandidateReview"]> = {},
): S["TermCandidateReview"] {
  return {
    id,
    term,
    state: "pending",
    decided_term: null,
    decided_scope: null,
    decided_at: null,
    channel: { id: channel, key: `youtube:UC${channel}`, title: `頻道 ${channel}` },
    item: { id: "item-1", ref: "yt:item1", title: "影片 1" },
    created_at: "2026-11-20T00:00:00Z",
    ...over,
  };
}

export function digest(id: string, over: Partial<S["DigestInfo"]> = {}): S["DigestInfo"] {
  return {
    id,
    period: "daily",
    weekday: null,
    at: "08:00",
    timezone: "Asia/Taipei",
    scope: "subscriptions",
    notifiers: ["ops"],
    enabled: true,
    last_run_at: null,
    next_run_at: "2026-11-25T00:00:00Z",
    created_at: "2026-11-20T00:00:00Z",
    ...over,
  };
}
