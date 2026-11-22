// 每個頁面的 axe 檢查清單（NFR-A11Y-1）：新增頁面時把路由與頁面標題加進來。
// src/test/routes.a11y.test.tsx 會逐一渲染並檢查。
export interface A11yRoute {
  path: string;
  /** 頁面渲染完成時會出現的 h1（繁中） */
  heading: string;
  /** 是否需要 session（app layout 底下的頁面） */
  auth: boolean;
}

export const a11yRoutes: A11yRoute[] = [
  { path: "/login", heading: "notesteme", auth: false },
  { path: "/library", heading: "媒體庫", auth: true },
  { path: "/library?platform=youtube&q=test", heading: "媒體庫", auth: true },
  { path: "/queue", heading: "佇列", auth: true },
  { path: "/items/yt:abc123", heading: "影音詳情", auth: true },
  // 設定的每個分區（UI-3b）；有 API 替身資料的 axe 檢查在 src/pages/settings/settings.test.tsx
  ...[
    "services",
    "accounts",
    "language",
    "outputs",
    "agent",
    "notifications",
    "budget",
    "glossary",
    "system",
    "config",
    "appearance",
    "login",
  ].map((s) => ({ path: `/settings/${s}`, heading: "設定", auth: true })),
  { path: "/no-such-page", heading: "找不到這個頁面", auth: false },
];
