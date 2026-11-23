// UI-4 的測試資料：訂閱、成本、計價表、筆記模板、通知、畫面與筆記。
import type { components } from "../api/schema";

type View = components["schemas"]["View"];

export function subscription(id: string, over: Partial<View> = {}): View {
  return {
    id,
    kind: "channel",
    platform: "youtube",
    external_id: "UCabc",
    url: "https://www.youtube.com/channel/UCabcdefghijklmnopqrstuv",
    title: "码农高天",
    profile: null,
    overrides: {},
    schedule: "6h",
    backfill: { mode: "none" },
    filters: { exclude_shorts: true, live: "wait" },
    enabled: true,
    created_at: "2026-11-01T00:00:00Z",
    last_checked_at: "2026-11-22T13:40:00Z",
    next_check_at: "2026-11-22T19:40:00Z",
    last_error: null,
    last_error_class: null,
    ...over,
  };
}

export function subscriptionEntries() {
  return {
    entries: [
      {
        ref: "yt:aaaaaaaaaaa",
        platform: "youtube",
        external_id: "aaaaaaaaaaa",
        part: 0,
        title: "新影片",
        published_at: "2026-11-20T00:00:00Z",
        duration_s: 365,
        live_status: null,
        decision: "created" as const,
        reason: null,
        item_id: "item-1",
        seen_at: "2026-11-20T01:00:00Z",
      },
      {
        ref: "yt:bbbbbbbbbbb",
        platform: "youtube",
        external_id: "bbbbbbbbbbb",
        part: 0,
        title: "一部 Shorts",
        published_at: "2026-11-19T00:00:00Z",
        duration_s: 58,
        live_status: null,
        decision: "skipped" as const,
        reason: "shorts",
        item_id: null,
        seen_at: "2026-11-20T01:00:00Z",
      },
    ],
  };
}

export function costSummary(over: Partial<components["schemas"]["Summary"]> = {}) {
  return {
    from: "2026-11-01",
    to: "2026-11-23",
    timezone: "Asia/Taipei",
    total_usd: 3.42,
    reserved_usd: 0.12,
    unknown_calls: 2,
    groups: [
      { key: "2026-11-21", label: "2026-11-21", usd: 1.2, reserved_usd: 0, calls: 10 },
      { key: "2026-11-22", label: "2026-11-22", usd: 2.22, reserved_usd: 0.12, calls: 14 },
    ],
    budget: {
      month: "2026-11",
      monthly_usd: 20,
      spent_usd: 3.42,
      reserved_usd: 0.12,
      projected_usd: 9.8,
    },
    notices: [],
    ...over,
  };
}

export function costEntry(n: number, over: Partial<components["schemas"]["Entry"]> = {}) {
  return {
    at: `2026-11-22T0${n % 10}:00:00Z`,
    item_id: `item-${n}`,
    ref: `yt:vid${String(n).padStart(8, "0")}`,
    item_title: `影片 ${n}`,
    channel: "UCchan",
    stage: "correct",
    provider: "anthropic",
    model: "claude-test",
    units: 12000,
    unit_type: "tokens",
    usd: 0.0123,
    state: "actual",
    attempt: 1,
    outcome: "ok",
    unit_price: {},
    ...over,
  };
}

export function priceList() {
  return {
    builtin_checked_at: "2026-11-01",
    builtin_stale: false,
    prices: [
      {
        provider: "anthropic",
        model: "claude-test",
        unit_type: "input_mtok",
        usd_per_unit: 3,
        source: "builtin",
        checked_at: "2026-11-01",
        stale: false,
      },
      {
        provider: "openai",
        model: "gpt-test",
        unit_type: "output_mtok",
        usd_per_unit: 10,
        source: "override",
        checked_at: "2026-07-01",
        stale: true,
      },
    ],
  };
}

export function noteTemplate(
  id: string,
  over: Partial<components["schemas"]["NoteTemplateBody"]> = {},
) {
  return {
    id,
    name: id === "video-notes" ? "Video Notes 預設" : `模板 ${id}`,
    version: 1,
    builtin: id === "video-notes",
    body: "{{.Frontmatter}}\n# {{.Title}}\n{{.InfoTable}}\n{{.Sections}}\n{{.SelfCheck}}\n",
    read_button: "",
    checks: ["frontmatter", "heading_timestamps", "images_exist", "image_names", "self_check"],
    sha256: "abc",
    created_at: "2026-11-20T00:00:00Z",
    ...over,
  };
}

export function notifier(id: string, over: Partial<components["schemas"]["Info"]> = {}) {
  return {
    id,
    kind: "webhook",
    config: { url_host: "hooks.example.com", url_last4: "abcd", secret_last4: "wxyz" },
    events: ["item.failed", "item.blocked"],
    enabled: true,
    created_at: "2026-11-20T00:00:00Z",
    updated_at: "2026-11-20T00:00:00Z",
    ...over,
  };
}

export function framesList() {
  return {
    target: 2,
    range: { min: 2, max: 4 },
    candidate_count: 3,
    selected_count: 2,
    shortfall: false,
    frames: [
      {
        n: 1,
        at: "00:00:05.000",
        score: 0.8,
        selected: true,
        excluded: null,
        caption: "架構圖",
        chapter: "開場",
        width: 1920,
        height: 1080,
      },
      {
        n: 2,
        at: "00:01:00.000",
        score: 0.2,
        selected: false,
        excluded: "sponsor",
        caption: "",
        chapter: "",
        width: 1920,
        height: 1080,
      },
      {
        n: 3,
        at: "00:02:00.000",
        score: 0.6,
        selected: true,
        excluded: null,
        caption: "結論",
        chapter: "結語",
        width: 1920,
        height: 1080,
      },
    ],
  };
}

export const NOTE_MD = [
  "---",
  "title: 影片 1",
  "---",
  "# 影片 1",
  "",
  "| 欄位 | 值 |",
  "| --- | --- |",
  "| 頻道 | UCchan |",
  "",
  "## 開場 [00:05](https://www.youtube.com/watch?v=vid00000001&t=5)",
  "",
  "第一段 **重點** 與 [連結](javascript:alert(1)) 和 <script>alert(1)</script>",
  "",
  "![[影片 1 - 00-05 - frame_0001.jpg]]",
  "*00:05 — 架構圖*",
  "",
  "> [!note] 補充",
  "> 引用的內容",
  "",
  "## 自我檢驗",
  "",
  "- 問題一",
  "- 問題二",
  "",
].join("\n");

export function noteBody(over: Partial<components["schemas"]["NoteBody"]> = {}) {
  return {
    lang: "zh-Hant",
    markdown: NOTE_MD,
    frontmatter: { title: "影片 1" },
    checks: [
      { id: "frontmatter", ok: true },
      { id: "heading_timestamps", ok: false, message: "標題沒有時間戳（補充說明）" },
    ],
    stale: false,
    outputs: [
      {
        target: "vault",
        vault: "main",
        kind: "note",
        path: "Video Notes/影片 1.md",
        state: "written",
        commit: "abc1234",
      },
    ],
    ...over,
  };
}
