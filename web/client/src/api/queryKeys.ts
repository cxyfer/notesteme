// TanStack Query 的 query key 都從這裡產生，SSE 失效表（src/events/invalidation.ts）也用同一組。
// 新增資源時在這裡加一個 key 家族，前綴相同的 key 可以一起失效。
export const queryKeys = {
  items: {
    all: ["items"] as const,
    lists: () => [...queryKeys.items.all, "list"] as const,
    list: (filters: Record<string, unknown>) => [...queryKeys.items.lists(), filters] as const,
    details: () => [...queryKeys.items.all, "detail"] as const,
    detail: (id: string) => [...queryKeys.items.details(), id] as const,
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
  system: ["system"] as const,
  glossary: (scope: string, id: string) => ["glossary", scope, id] as const,
  termCandidates: (channel: string) => ["termCandidates", channel] as const,
};
