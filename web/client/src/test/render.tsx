import { createMemoryHistory } from "@tanstack/react-router";
import { RouterProvider } from "@tanstack/react-router";
import { QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";

import { createQueryClient } from "../api/queryClient";
import { ToastProvider } from "../components";
import { createAppRouter } from "../router";

/** 設定 CSRF cookie，模擬「已登入」（後端沒有 session 路由，前端只看這個提示）。 */
export function signIn(token = "csrf-test") {
  document.cookie = `notesteme_csrf=${token}; path=/`;
}

export function renderRoute(path: string) {
  const queryClient = createQueryClient();
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [path] }));
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>,
  );
  return { ...utils, router, queryClient };
}
