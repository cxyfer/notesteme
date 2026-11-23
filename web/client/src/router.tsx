// 路由（TanStack Router，程式碼式路由）：所有路由都定義在這個檔案，慣例見 web/client/README.md「路由」。
//
//   root
//   ├─ /login                 登入（不需要 session）
//   └─ app（layout，需要 session；AppShell）
//      ├─ /                   M1–M3 導到 /library（docs/ui/README.md §2）
//      ├─ /library
//      ├─ /queue
//      ├─ /items/$id
//      ├─ /add                書籤小工具與分享的落地頁：打開「新增」抽屜後換成 /library（ENTRY-R2，lazy）
//      ├─ /settings           導到第一個分區
//      ├─ /settings/$section
//      ├─ /prompts、/prompts/$id     Prompt 庫（編輯頁含 CodeMirror，lazy 載入）
//      ├─ /profiles、/profiles/$id   處理方案
//      └─ /styles、/styles/$id       字幕樣式
//
// 新增頁面：在這裡加一個 createRoute、加進 routeTree，並把路徑加進 src/test/a11yRoutes.ts。
import type { QueryClient } from "@tanstack/react-query";
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  lazyRouteComponent,
  Outlet,
  redirect,
  type RouterHistory,
} from "@tanstack/react-router";

import { validateAddEntrySearch } from "./add/entry";
import { hasSessionHint, safeRedirect } from "./auth/session";
import { validateItemSearch } from "./pages/item/search";
import { validateLibrarySearch } from "./pages/library/filters";
import { LibraryPage } from "./pages/LibraryPage";
import { LoginPage } from "./pages/LoginPage";
import { NotFoundPage } from "./pages/NotFoundPage";
import { ProfileEditorPage } from "./pages/profiles/ProfileEditorPage";
import { ProfilesPage } from "./pages/profiles/ProfilesPage";
import { PromptsPage } from "./pages/prompts/PromptsPage";
import { validateQueueSearch } from "./pages/queue/filters";
import { QueuePage } from "./pages/queue/QueuePage";
import { SETTINGS_SECTIONS, SettingsPage } from "./pages/SettingsPage";
import { StyleEditorPage } from "./pages/styles/StyleEditorPage";
import { StylesPage } from "./pages/styles/StylesPage";
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
  // next 由伺服器的頁面路由帶來（ENTRY-R2、R5），原樣交給 POST /api/auth/login 檢查，UI 只用回應中的 next（ENTRY-R6）
  validateSearch: (search: Record<string, unknown>): { redirect?: string; next?: string } => {
    const out: { redirect?: string; next?: string } = {};
    const r = safeRedirect(search.redirect, "");
    if (r) out.redirect = r;
    if (typeof search.next === "string" && search.next !== "") out.next = search.next;
    return out;
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
  // 篩選條件在 URL（LIB-R2）：不合法的值丟掉，見 pages/library/filters.ts
  validateSearch: validateLibrarySearch,
  component: LibraryPage,
});
const queueRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/queue",
  // 資源類別與任務狀態的篩選在 URL
  validateSearch: validateQueueSearch,
  component: QueuePage,
});
export const itemRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/items/$id",
  // 分頁、字幕模式、起始時間在 URL；頁面本身是 lazy route（播放器、字幕列表、diff 不進初始 bundle）
  validateSearch: validateItemSearch,
  component: lazyRouteComponent(() => import("./pages/item/ItemPage"), "ItemPage"),
});

const addEntryRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/add",
  validateSearch: validateAddEntrySearch,
  component: lazyRouteComponent(() => import("./add/AddEntryPage"), "AddEntryPage"),
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

// ---- UI-3a：Prompt 庫、處理方案、字幕樣式 ----
const promptsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/prompts",
  component: PromptsPage,
});
export const promptRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/prompts/$id",
  // CodeMirror 很大：編輯頁只在進入時才載入，不算進初始 bundle（NFR-PERF-10）
  component: lazyRouteComponent(
    () => import("./pages/prompts/PromptEditorPage"),
    "PromptEditorPage",
  ),
});
const profilesRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/profiles",
  component: ProfilesPage,
});
export const profileRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/profiles/$id",
  component: ProfileEditorPage,
});
const stylesRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/styles",
  component: StylesPage,
});
export const styleRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/styles/$id",
  component: StyleEditorPage,
});

export const routeTree = rootRoute.addChildren([
  loginRoute,
  appRoute.addChildren([
    indexRoute,
    libraryRoute,
    queueRoute,
    itemRoute,
    addEntryRoute,
    settingsIndexRoute,
    settingsRoute,
    promptsRoute,
    promptRoute,
    profilesRoute,
    profileRoute,
    stylesRoute,
    styleRoute,
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
