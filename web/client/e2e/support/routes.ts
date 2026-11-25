// 把 src/test/a11yRoutes.ts（vitest 的每頁 axe 清單，資料來自 mockApi 的 fixture）對應到真的 serve 與 benchdata 的資料：
// 路徑裡的 fixture ID（yt:abc123、item-1、s1）換成 e2eseed 建立的影音與訂閱；只在 fixture 出現的文字（ready）換成真資料的文字。
// 不含 fixture ID 的路由原樣使用（標題相同），所以 a11yRoutes 新增頁面時這裡自動涵蓋；
// 新增的路由帶了 fixture ID 而這裡沒有對應時，routes.spec.ts 會失敗。
import { a11yRoutes, type A11yRoute } from "../../src/test/a11yRoutes";
import type { E2EEnv } from "./global-setup";

export interface Resolved {
  path: string;
  heading: string | RegExp;
  ready?: string | RegExp;
}

export interface E2ERoute {
  /** a11yRoutes 的原始路徑（測試名稱用） */
  source: string;
  auth: boolean;
  /** 真資料的路徑與就緒條件：env 在 global setup 之後才有，測試裡才解析 */
  at: (env: E2EEnv) => Resolved;
}

/** 路徑含這些片段時一定要有對應（fixture 的 ID） */
export const FIXTURE_ID = /\/items\/|\/subscriptions\/[^?]/;

type Override = Resolved;

function overrides(env: E2EEnv): Record<string, Override> {
  const en = `/items/${encodeURIComponent(env.enItemRef)}`;
  // bilibili 的 ref 含 /p1，用 ID
  const note = `/items/${env.noteItem}`;
  const failed = `/items/${encodeURIComponent(env.failedItems[0]!)}`;
  const enTitle = /^Video 0 about/;
  const noteTitle = /^Video 1 about/;
  return {
    "/items/yt:abc123": { path: en, heading: enTitle, ready: "開場" },
    "/items/item-1?tab=stages": { path: `${failed}?tab=stages`, heading: /^Video \d+ about/ },
    "/items/item-1?tab=runs": { path: `${note}?tab=runs`, heading: noteTitle },
    "/items/item-1?tab=outputs": { path: `${note}?tab=outputs`, heading: noteTitle },
    "/items/item-1?tab=frames": { path: `${note}?tab=frames`, heading: noteTitle, ready: "架構圖" },
    "/items/item-1?tab=note": { path: `${note}?tab=note`, heading: noteTitle, ready: "重點" },
    "/items/item-1?mode=diff": { path: `${note}?mode=diff`, heading: noteTitle },
    "/subscriptions": { path: "/subscriptions", heading: "訂閱", ready: "E2E 頻道" },
    "/subscriptions/s1": {
      path: `/subscriptions/${encodeURIComponent(env.subscription)}`,
      heading: "E2E 頻道",
    },
    "/costs": { path: "/costs", heading: "成本", ready: enTitle },
    "/costs?group_by=stage": { path: "/costs?group_by=stage", heading: "成本", ready: "計價表" },
    "/note-templates": { path: "/note-templates", heading: "筆記模板", ready: "Video Notes" },
    "/note-templates/video-notes": { path: "/note-templates/video-notes", heading: /Video Notes/ },
    "/search?q=ordered%20log": { path: "/search?q=database", heading: "搜尋字幕" },
    "/": { path: "/", heading: "總覽", ready: "需要你處理" },
    "/trash": { path: "/trash", heading: "垃圾桶", ready: env.trashedTitle },
  };
}

export function e2eRoutes(): E2ERoute[] {
  return a11yRoutes.map((r: A11yRoute) => ({
    source: r.path,
    auth: r.auth,
    // fixture 的 ready 文字在真資料不存在：沒有對應的路由只等標題
    at: (env: E2EEnv) => overrides(env)[r.path] ?? { path: r.path, heading: r.heading },
  }));
}

export function unmappedFixtureRoutes(env: E2EEnv): string[] {
  const o = overrides(env);
  return a11yRoutes.filter((r) => FIXTURE_ID.test(r.path) && !o[r.path]).map((r) => r.path);
}

export function slug(path: string) {
  return (
    path
      .replace(/^\//, "")
      .replace(/[^A-Za-z0-9_-]+/g, "_")
      .replace(/_+$/, "") || "root"
  );
}
