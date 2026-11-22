import { QueryClient } from "@tanstack/react-query";

import { isApiError } from "./errors";

export function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        // 4xx 不重試（例如 401、404）；網路錯誤與 5xx 重試兩次。
        retry: (count, err) =>
          count < 2 && !(isApiError(err) && err.status >= 400 && err.status < 500),
        refetchOnWindowFocus: false, // 即時更新靠 SSE
      },
    },
  });
}
