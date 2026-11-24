import { QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { render } from "@testing-library/react";

import { createQueryClient } from "../api/queryClient";
import { ToastProvider } from "../components";
import { createAppRouter } from "../router";

/** 設定 Hub 的 CSRF cookie（非 GET 請求會帶上它）。 */
export function setCsrf(token = "csrf-test") {
  document.cookie = `__Host-notesteme_hub_csrf=${token}; path=/; secure`;
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
