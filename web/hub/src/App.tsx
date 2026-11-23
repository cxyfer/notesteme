import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { useState } from "react";

import { ToastProvider } from "./components";
import { createAppRouter } from "./router";

export function App({ queryClient }: { queryClient: QueryClient }) {
  const [router] = useState(() => createAppRouter(queryClient));
  return (
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>
  );
}
