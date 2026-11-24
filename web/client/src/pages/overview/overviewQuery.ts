// 總覽（GET /api/overview）的讀取：總覽頁與設定 › 系統的磁碟用量共用同一個 query key 與讀法。
import { api } from "../../api/client";
import { isApiError } from "../../api/errors";
import type { components } from "../../api/schema";

export type Overview = components["schemas"]["OverviewResult"];

/** 瀏覽器的 IANA 時區；取不到時讓伺服器用預算時區 */
export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone ?? "";
  } catch {
    return "";
  }
}

/** 伺服器不認得瀏覽器的時區（422）時改用預算時區重讀一次 */
export async function fetchOverview(tz: string, signal: AbortSignal): Promise<Overview> {
  try {
    return await api.get("/api/overview", { query: tz ? { tz } : {}, signal });
  } catch (err) {
    if (tz && isApiError(err) && err.status === 422) return api.get("/api/overview", { signal });
    throw err;
  }
}
