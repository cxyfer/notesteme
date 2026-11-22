// 路由（TanStack Router，程式碼式路由）：所有路由都定義在這個檔案，慣例見 web/client/README.md「路由」。
//
//   root
//   ├─ /login                 登入（不需要 session）
//   └─ app（layout，需要 session；AppShell）
//      ├─ /                   M1–M3 導到 /library（docs/ui/README.md §2）
//      ├─ /library
//      ├─ /queue
//      ├─ /items/$id
//      ├─ /settings           導到第一個分區
//      └─ /settings/$section
//
// 新增頁面：在這裡加一個 createRoute、加進 routeTree，並把路徑加進 src/test/a11yRoutes.ts。
import type { QueryClient } from "@tanstack/react-query";
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  Outlet,
  redirect,
  type RouterHistory,
} from "@tanstack/react-router";

import { hasSessionHint, safeRedirect } from "./auth/session";
import { ItemPage } from "./pages/ItemPage";
import { LibraryPage } from "./pages/LibraryPage";
import { LoginPage } from "./pages/LoginPage";
import { NotFoundPage } from "./pages/NotFoundPage";
import { QueuePage } from "./pages/QueuePage";
import { SETTINGS_SECTIONS, SettingsPage } from "./pages/SettingsPage";
import { AppShell } from "./shell/AppShell";

export interface RouterContext {
  queryClient: QueryClient;
}

const rootRoute = createRootRouteWithContext<RouterContext>()({
  component: Outlet,
  notFoundComponent: NotFoundPage,
});

export const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  validateSearch: (search: Record<string, unknown>): { redirect?: string } => {
    const r = safeRedirect(search.redirect, "");
    return r ? { redirect: r } : {};
  },
  component: LoginPage,
});

const appRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "app",
  beforeLoad: ({ location }) => {
    if (!hasSessionHint()) {
      throw redirect({ to: "/login", search: { redirect: location.href } });
    }
  },
  component: AppShell,
});

const indexRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/",
  beforeLoad: () => {
    throw redirect({ to: "/library", replace: true });
  },
});

const libraryRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/library",
  component: LibraryPage,
});
const queueRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/queue",
  component: QueuePage,
});
export const itemRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/items/$id",
  component: ItemPage,
});

const settingsIndexRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/settings",
  beforeLoad: () => {
    throw redirect({
      to: "/settings/$section",
      params: { section: SETTINGS_SECTIONS[0] },
      replace: true,
    });
  },
});

export const settingsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/settings/$section",
  component: SettingsPage,
});

export const routeTree = rootRoute.addChildren([
  loginRoute,
  appRoute.addChildren([
    indexRoute,
    libraryRoute,
    queueRoute,
    itemRoute,
    settingsIndexRoute,
    settingsRoute,
  ]),
]);

export function createAppRouter(queryClient: QueryClient, history?: RouterHistory) {
  return createRouter({
    routeTree,
    context: { queryClient },
    defaultPreload: "intent",
    scrollRestoration: true,
    ...(history ? { history } : {}),
  });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
