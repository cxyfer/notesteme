// 設定檔定義的 vault 與 hook（VAULT-R1、R10）：設定 › 輸出唯讀列出，處理方案的輸出目標選用。
// 兩者只在設定檔修改，執行期間不會變，所以 5 分鐘內不重讀。
import { useQuery } from "@tanstack/react-query";

import { api } from "./client";
import { queryKeys } from "./queryKeys";

const STALE_MS = 5 * 60_000;

export function useVaults() {
  return useQuery({
    queryKey: queryKeys.vaults,
    queryFn: ({ signal }) => api.get("/api/vaults", { signal }),
    staleTime: STALE_MS,
  });
}

export function useHooks() {
  return useQuery({
    queryKey: queryKeys.hooks,
    queryFn: ({ signal }) => api.get("/api/hooks", { signal }),
    staleTime: STALE_MS,
  });
}
