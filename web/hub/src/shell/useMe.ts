// 目前的帳號：GET /api/v1/me。401 代表沒有登入（站台可見度是 public 時仍可瀏覽），回 null，不導到登入頁。
import { useQuery } from "@tanstack/react-query";

import { api } from "../api/client";
import { isApiError } from "../api/errors";
import { queryKeys } from "../api/queryKeys";
import type { Me } from "../api/types";

export async function fetchMe(): Promise<Me | null> {
  try {
    return await api.get("/api/v1/me", { quiet401: true });
  } catch (e) {
    if (isApiError(e) && e.status === 401) return null;
    throw e;
  }
}

export function useMe() {
  return useQuery({ queryKey: queryKeys.me, queryFn: fetchMe, staleTime: 60_000 });
}
