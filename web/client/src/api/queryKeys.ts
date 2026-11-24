// TanStack Query 的 query key 都從這裡產生，SSE 失效表（src/events/invalidation.ts）也用同一組。
// 新增資源時在這裡加一個 key 家族，前綴相同的 key 可以一起失效。
export const queryKeys = {
  items: {
    all: ["items"] as const,
    lists: () => [...queryKeys.items.all, "list"] as const,
    list: (filters: Record<string, unknown>) => [...queryKeys.items.lists(), filters] as const,
    details: () => [...queryKeys.items.all, "detail"] as const,
    detail: (id: string) => [...queryKeys.items.details(), id] as const,
    // 選影音用的清單（ItemPicker）：不放在 lists() 底下，避免和媒體庫的 infinite query 共用前綴
    picker: (q: string) => [...queryKeys.items.all, "picker", q] as const,
    // 字幕、執行紀錄、輸出不放在 detail 底下：每個 stage.status 都讓 detail 失效，上千句的字幕不能跟著重抓
    allCues: () => [...queryKeys.items.all, "cues"] as const,
    cuesOf: (id: string) => [...queryKeys.items.allCues(), id] as const,
    cues: (id: string, q: { variant?: string; lang?: string }) =>
      [...queryKeys.items.cuesOf(id), q] as const,
    allRuns: () => [...queryKeys.items.all, "runs"] as const,
    runs: (id: string) => [...queryKeys.items.allRuns(), id] as const,
    allOutputs: () => [...queryKeys.items.all, "outputs"] as const,
    outputs: (id: string) => [...queryKeys.items.allOutputs(), id] as const,
    deletionPreview: (id: string) => [...queryKeys.items.all, "deletion-preview", id] as const,
    /** 佇列頁「需要你處理」的計數（GET /api/items?status=…&limit=1 的 total） */
    allAttention: () => [...queryKeys.items.all, "attention"] as const,
    attention: (stage: string) => [...queryKeys.items.allAttention(), stage] as const,
    // UI-4：畫面與筆記（frames、note、publish 完成時失效，見 src/events/invalidation.ts）
    allFrames: () => [...queryKeys.items.all, "frames"] as const,
    frames: (id: string) => [...queryKeys.items.allFrames(), id] as const,
    allNotes: () => [...queryKeys.items.all, "note"] as const,
    note: (id: string) => [...queryKeys.items.allNotes(), id] as const,
  },
  profiles: ["profiles"] as const,
  // 處理方案的單筆與版本（前綴是 profiles，列表失效時一起失效）
  profile: (id: string) => [...queryKeys.profiles, "detail", id] as const,
  profileVersions: (id: string) => [...queryKeys.profiles, "versions", id] as const,
  prompts: {
    all: ["prompts"] as const,
    list: () => [...queryKeys.prompts.all, "list"] as const,
    versions: (id: string) => [...queryKeys.prompts.all, "versions", id] as const,
  },
  assStyles: {
    all: ["ass-styles"] as const,
    list: (lang = "", sourceLang = "") =>
      [...queryKeys.assStyles.all, "list", lang, sourceLang] as const,
    detail: (id: string, lang = "", sourceLang = "") =>
      [...queryKeys.assStyles.all, "detail", id, lang, sourceLang] as const,
  },
  queue: ["queue"] as const,
  trash: ["trash"] as const,
  settings: (section: string) => ["settings", section] as const,
  tokens: ["tokens"] as const,
  sessions: ["sessions"] as const,
  system: ["system"] as const,
  glossary: (scope: string, id: string) => ["glossary", scope, id] as const,
  termCandidates: (channel: string) => ["termCandidates", channel] as const,
  // ---- UI-4：訂閱、成本、筆記模板、通知 ----
  subscriptions: {
    all: ["subscriptions"] as const,
    list: () => [...queryKeys.subscriptions.all, "list"] as const,
    detail: (id: string) => [...queryKeys.subscriptions.all, "detail", id] as const,
    entries: (id: string, decision: string) =>
      [...queryKeys.subscriptions.all, "entries", id, decision] as const,
  },
  costs: {
    all: ["costs"] as const,
    summary: (q: Record<string, string>) => [...queryKeys.costs.all, "summary", q] as const,
    entries: (q: Record<string, string>) => [...queryKeys.costs.all, "entries", q] as const,
  },
  prices: ["prices"] as const,
  noteTemplates: ["note-templates"] as const,
  notifiers: ["notifiers"] as const,
  notifications: ["notifications"] as const,
  vaults: ["vaults"] as const,
  hooks: ["hooks"] as const,
  // ---- UI-5：字幕全文搜尋 ----
  search: (q: string, lang: string) => ["search", q, lang] as const,
  // 跨頻道的術語審核清單（GLOS-R5）：前綴與 termCandidates 相同，決定後一起失效
  termCandidatesAll: ["termCandidates"] as const,
  // 彙整設定（DIG-R8）
  digests: ["digests"] as const,
  termReview: (state: string, channel: string) =>
    ["termCandidates", "review", state, channel] as const,
  // M5-S9：Hub 的連線設定（HSHR-R1）
  hubSettings: ["hubSettings"] as const,
  // ---- UI-7：總覽（LIB-R21）；tz 是瀏覽器的時區 ----
  overviewAll: ["overview"] as const,
  overview: (tz: string) => ["overview", tz] as const,
};
