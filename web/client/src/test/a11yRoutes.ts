// 每個頁面的 axe 檢查清單（NFR-A11Y-1）：新增頁面時把路由與頁面標題加進來。
// src/test/routes.a11y.test.tsx 會逐一渲染並檢查。
export interface A11yRoute {
  path: string;
  /** 頁面渲染完成時會出現的 h1（繁中） */
  heading: string;
  /** 是否需要 session（app layout 底下的頁面） */
  auth: boolean;
  /** 資料載入完成時會出現的文字（有的話等它出現再檢查） */
  ready?: string;
}

export const a11yRoutes: A11yRoute[] = [
  { path: "/login", heading: "notesteme", auth: false },
  { path: "/library", heading: "媒體庫", auth: true },
  { path: "/library?platform=youtube&q=test", heading: "媒體庫", auth: true },
  { path: "/queue", heading: "佇列", auth: true, ready: "下載" },
  { path: "/queue?class=llm&state=pending", heading: "佇列", auth: true, ready: "下載" },
  { path: "/items/yt:abc123", heading: "影片 1", auth: true, ready: "第一句" },
  { path: "/items/item-1?tab=stages", heading: "影片 1", auth: true, ready: "無法完成：驗證失敗" },
  { path: "/items/item-1?tab=runs", heading: "影片 1", auth: true, ready: "當時生效的設定" },
  { path: "/items/item-1?tab=outputs", heading: "影片 1", auth: true, ready: "匯入修正過的字幕" },
  { path: "/items/item-1?tab=frames", heading: "影片 1", auth: true, ready: "架構圖" },
  { path: "/items/item-1?tab=note", heading: "影片 1", auth: true, ready: "Video Notes/影片 1.md" },
  { path: "/items/item-1?mode=diff", heading: "影片 1", auth: true, ready: "刪除的文字" },
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
    "bookmarklet",
    "login",
  ].map((s) => ({ path: `/settings/${s}`, heading: "設定", auth: true })),
  // ENTRY-R2：/add 打開「新增」抽屜後換成媒體庫（無效的網址不會呼叫辨識）
  { path: "/add?url=javascript%3Aalert(1)", heading: "媒體庫", auth: true },
  { path: "/prompts", heading: "Prompt 庫", auth: true },
  { path: "/prompts/correct-default", heading: "correct-default", auth: true },
  { path: "/profiles", heading: "處理方案", auth: true },
  { path: "/profiles/default", heading: "default", auth: true },
  { path: "/styles", heading: "字幕樣式", auth: true },
  { path: "/styles/default", heading: "default", auth: true },
  // UI-4
  { path: "/subscriptions", heading: "訂閱", auth: true, ready: "码农高天" },
  { path: "/subscriptions/s1", heading: "码农高天", auth: true, ready: "新影片" },
  { path: "/costs", heading: "成本", auth: true, ready: "影片 1" },
  { path: "/costs?group_by=stage", heading: "成本", auth: true, ready: "計價表" },
  { path: "/note-templates", heading: "筆記模板", auth: true, ready: "Video Notes 預設" },
  { path: "/note-templates/video-notes", heading: "Video Notes 預設", auth: true },
  // UI-5
  { path: "/search", heading: "搜尋字幕", auth: true },
  { path: "/search?q=ordered%20log", heading: "搜尋字幕", auth: true, ready: "ordered log" },
  { path: "/no-such-page", heading: "找不到這個頁面", auth: false },
];
