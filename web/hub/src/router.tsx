// Hub UI 的路由（TanStack Router，程式碼式路由；頁面清單見 docs/ui/pages.md §14）。
//
//   root（HubShell：頂端列）
//   ├─ /login                         登入（列出已啟用的登入方式）
//   ├─ /                              瀏覽與篩選（HREP-R16；篩選在 URL）
//   ├─ /videos/$platform/$externalId  影片頁（HREP-R17；?part=、?compare=a,b）       lazy
//   ├─ /versions/$id                  版本頁（來源紀錄、衍生關係、下載、投票、檢舉）  lazy
//   ├─ /takedown、/takedowns/$id      下架申請表單與查詢（不需要登入，HACC-R15）       lazy
//   ├─ /account                       帳號（登入方式、Hub token、我的分享）            lazy
//   └─ /admin/{reports,takedowns,users,audit}  管理（moderate／admin）                 lazy
//
// 不需要 session 的判斷：Hub 有 GET /api/v1/me（useMe）。任何其他 API 回 401 unauthorized 時導到 /login。
import type { QueryClient } from "@tanstack/react-query";
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  lazyRouteComponent,
  redirect,
  type RouterHistory,
} from "@tanstack/react-router";

import { setUnauthorizedHandler } from "./api/client";
import { safeRedirect } from "./auth/session";
import { BrowsePage } from "./pages/browse/BrowsePage";
import { validateBrowseSearch } from "./pages/browse/filters";
import { LoginPage } from "./pages/LoginPage";
import { validateTakedownSearch } from "./pages/takedown/search";
import { validateVideoSearch } from "./pages/video/search";
import { NotFoundPage } from "./pages/NotFoundPage";
import { HubShell } from "./shell/HubShell";

export interface RouterContext {
  queryClient: QueryClient;
}

const rootRoute = createRootRouteWithContext<RouterContext>()({
  component: HubShell,
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

export const browseRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  validateSearch: validateBrowseSearch,
  component: BrowsePage,
});

const lazy = <T extends string>(load: () => Promise<Record<T, () => React.ReactNode>>, name: T) =>
  lazyRouteComponent(load, name);

export const videoRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/videos/$platform/$externalId",
  validateSearch: validateVideoSearch,
  component: lazy(() => import("./pages/video/VideoPage"), "VideoPage"),
});
export const versionRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/versions/$id",
  component: lazy(() => import("./pages/version/VersionPage"), "VersionPage"),
});
export const takedownRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/takedown",
  validateSearch: validateTakedownSearch,
  component: lazy(() => import("./pages/takedown/TakedownPage"), "TakedownPage"),
});
export const takedownStatusRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/takedowns/$id",
  component: lazy(() => import("./pages/takedown/TakedownStatusPage"), "TakedownStatusPage"),
});
export const accountRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/account",
  component: lazy(() => import("./pages/account/AccountPage"), "AccountPage"),
});
const adminIndexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/admin",
  beforeLoad: () => {
    throw redirect({ to: "/admin/reports" });
  },
});
const adminReportsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/admin/reports",
  component: lazy(() => import("./pages/admin/ReportsPage"), "ReportsPage"),
});
const adminTakedownsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/admin/takedowns",
  component: lazy(() => import("./pages/admin/TakedownsPage"), "TakedownsPage"),
});
const adminUsersRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/admin/users",
  component: lazy(() => import("./pages/admin/UsersPage"), "UsersPage"),
});
const adminAuditRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/admin/audit",
  component: lazy(() => import("./pages/admin/AuditPage"), "AuditPage"),
});

export const routeTree = rootRoute.addChildren([
  loginRoute,
  browseRoute,
  videoRoute,
  versionRoute,
  takedownRoute,
  takedownStatusRoute,
  accountRoute,
  adminIndexRoute,
  adminReportsRoute,
  adminTakedownsRoute,
  adminUsersRoute,
  adminAuditRoute,
]);

export function createAppRouter(queryClient: QueryClient, history?: RouterHistory) {
  const router = createRouter({
    routeTree,
    context: { queryClient },
    defaultPreload: "intent",
    scrollRestoration: true,
    ...(history ? { history } : {}),
  });
  setUnauthorizedHandler(() => {
    const here = router.state.location;
    if (here.pathname === "/login") return;
    void router.navigate({ to: "/login", search: { redirect: here.href } });
  });
  return router;
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
