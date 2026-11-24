// 路由（TanStack Router，程式碼式路由）：所有路由都定義在這個檔案，慣例見 web/client/README.md「路由」。
//
//   root
//   ├─ /login                 登入（不需要 session）
//   └─ app（layout，需要 session；AppShell）
//      ├─ /                   總覽（UI-7，lazy；M1–M3 曾導到 /library）
//      ├─ /library            媒體庫（收尾 D1 起 lazy）
//      ├─ /queue              佇列（lazy）
//      ├─ /items/$id
//      ├─ /add                書籤小工具與分享的落地頁：打開「新增」抽屜後換成 /library（ENTRY-R2，lazy）
//      ├─ /settings           導到第一個分區
//      ├─ /settings/$section
//      ├─ /prompts、/prompts/$id     Prompt 庫（編輯頁含 CodeMirror，lazy 載入）
//      ├─ /profiles、/profiles/$id   處理方案（2026-11-25 起 lazy）
//      ├─ /styles、/styles/$id       字幕樣式（2026-11-25 起 lazy）
//      ├─ /subscriptions、/subscriptions/$id   訂閱（UI-4，lazy）
//      ├─ /costs                     成本（UI-4，lazy；篩選在 URL）
//      ├─ /note-templates、/note-templates/$id 筆記模板（UI-4，lazy）
//      ├─ /search                    字幕全文搜尋（UI-5，lazy；查詢在 URL）
//      └─ /trash                     垃圾桶（UI-7，lazy）
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
import { validateCostsSearch } from "./pages/costs/search";
import { isInsecureNoAuth } from "./auth/insecure";
import { hasSessionHint, safeRedirect } from "./auth/session";
import { validateItemSearch } from "./pages/item/search";
import { validateLibrarySearch } from "./pages/library/filters";
import { LoginPage } from "./pages/LoginPage";
import { NotFoundPage } from "./pages/NotFoundPage";
import { PromptsPage } from "./pages/prompts/PromptsPage";
import { validateQueueSearch } from "./pages/queue/filters";
import { validateSearchParams } from "./pages/search/model";
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
  beforeLoad: async ({ location, context }) => {
    // 無驗證模式（AUTH-R3）沒有 CSRF cookie，不導到登入頁。
    if (!hasSessionHint() && !(await isInsecureNoAuth(context.queryClient))) {
      throw redirect({ to: "/login", search: { redirect: location.href } });
    }
  },
  component: AppShell,
});

// UI-7：總覽（LIB-09）是首頁
const indexRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/",
  component: lazyRouteComponent(() => import("./pages/overview/OverviewPage"), "OverviewPage"),
});

const libraryRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/library",
  // 篩選條件在 URL（LIB-R2）：不合法的值丟掉，見 pages/library/filters.ts
  validateSearch: validateLibrarySearch,
  component: lazyRouteComponent(() => import("./pages/LibraryPage"), "LibraryPage"),
});
const queueRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/queue",
  // 資源類別與任務狀態的篩選在 URL；頁面 lazy 載入（收尾 D1 加了重試與取消）
  validateSearch: validateQueueSearch,
  component: lazyRouteComponent(() => import("./pages/queue/QueuePage"), "QueuePage"),
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
  // 處理方案與字幕樣式的頁面只在進入時才載入（初始 bundle 預算，NFR-PERF-10）
  component: lazyRouteComponent(() => import("./pages/profiles/ProfilesPage"), "ProfilesPage"),
});
export const profileRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/profiles/$id",
  component: lazyRouteComponent(
    () => import("./pages/profiles/ProfileEditorPage"),
    "ProfileEditorPage",
  ),
});
const stylesRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/styles",
  component: lazyRouteComponent(() => import("./pages/styles/StylesPage"), "StylesPage"),
});
export const styleRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/styles/$id",
  component: lazyRouteComponent(() => import("./pages/styles/StyleEditorPage"), "StyleEditorPage"),
});

// ---- UI-4：訂閱、成本、筆記模板（都是 lazy route，不進初始 bundle）----
const subscriptionsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/subscriptions",
  // ?subscribe=<網址>：「新增」抽屜貼上頻道時的「建立訂閱」，打開新增訂閱的抽屜並帶入網址
  validateSearch: (search: Record<string, unknown>): { subscribe?: string } =>
    typeof search.subscribe === "string" && search.subscribe.trim()
      ? { subscribe: search.subscribe.trim().slice(0, 2048) }
      : {},
  component: lazyRouteComponent(
    () => import("./pages/subscriptions/SubscriptionsPage"),
    "SubscriptionsPage",
  ),
});
export const subscriptionRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/subscriptions/$id",
  component: lazyRouteComponent(
    () => import("./pages/subscriptions/SubscriptionPage"),
    "SubscriptionPage",
  ),
});
const costsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/costs",
  // 分組與日期範圍在 URL（README §3.3）
  validateSearch: validateCostsSearch,
  component: lazyRouteComponent(() => import("./pages/costs/CostsPage"), "CostsPage"),
});

const noteTemplatesRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/note-templates",
  component: lazyRouteComponent(
    () => import("./pages/noteTemplates/NoteTemplatesPage"),
    "NoteTemplatesPage",
  ),
});
export const noteTemplateRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/note-templates/$id",
  component: lazyRouteComponent(
    () => import("./pages/noteTemplates/NoteTemplateEditorPage"),
    "NoteTemplateEditorPage",
  ),
});

// ---- UI-5：字幕全文搜尋（lazy）----
const searchRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/search",
  validateSearch: validateSearchParams,
  component: lazyRouteComponent(() => import("./pages/search/SearchPage"), "SearchPage"),
});

// ---- UI-7：垃圾桶（lazy）----
const trashRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/trash",
  component: lazyRouteComponent(() => import("./pages/trash/TrashPage"), "TrashPage"),
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
    subscriptionsRoute,
    subscriptionRoute,
    costsRoute,
    noteTemplatesRoute,
    noteTemplateRoute,
    searchRoute,
    trashRoute,
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
