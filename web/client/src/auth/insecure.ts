// AUTH-R3：serve 以 --insecure-no-auth 啟動時沒有 session（也就沒有 CSRF cookie），
// 免驗證的 GET /api/auth/methods 回 insecure_no_auth: true。登入頁與它共用同一個查詢。
import type { QueryClient } from "@tanstack/react-query";

import { api } from "../api/client";

export const authMethodsQuery = {
  queryKey: ["auth-methods"],
  queryFn: () => api.get("/api/auth/methods"),
  retry: false,
  staleTime: Infinity,
} as const;

/** 伺服器是否在無驗證模式；查詢失敗（例如舊版伺服器、離線）一律當成不是。 */
export async function isInsecureNoAuth(qc: QueryClient): Promise<boolean> {
  try {
    const m = await qc.fetchQuery(authMethodsQuery);
    return m.insecure_no_auth === true;
  } catch {
    return false;
  }
}
