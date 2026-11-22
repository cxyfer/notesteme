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
  queue: ["queue"] as const,
  trash: ["trash"] as const,
  settings: (section: string) => ["settings", section] as const,
  tokens: ["tokens"] as const,
  system: ["system"] as const,
  glossary: (scope: string, id: string) => ["glossary", scope, id] as const,
  termCandidates: (channel: string) => ["termCandidates", channel] as const,
};
