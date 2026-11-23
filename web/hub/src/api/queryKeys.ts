// TanStack Query 的 key：同一份資料只有一個 key，變更後依前綴失效。
export const queryKeys = {
  me: ["me"] as const,
  providers: ["providers"] as const,
  versions: (filters: Record<string, string | undefined>) => ["versions", filters] as const,
  version: (id: string) => ["version", id] as const,
  versionText: (id: string, format: string) => ["version", id, "text", format] as const,
  video: (platform: string, externalId: string, part: number | undefined) =>
    ["video", platform, externalId, part ?? null] as const,
  takedown: (id: string) => ["takedown", id] as const,
  tokens: ["me", "tokens"] as const,
  shares: ["me", "shares"] as const,
  adminReports: (state: string) => ["admin", "reports", state] as const,
  adminTakedowns: (state: string) => ["admin", "takedowns", state] as const,
  adminUsers: ["admin", "users"] as const,
  adminAudit: ["admin", "audit"] as const,
};
