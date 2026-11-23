// 每個頁面的 axe 檢查（NFR-A11Y-1）：以管理員身分載入，API 都有資料。
import { screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "./axe";
import * as f from "./fixtures";
import { mockApi } from "./mockApi";
import { renderRoute } from "./render";

const routes: [string, string][] = [
  ["/login", "登入 Hub"],
  ["/", "瀏覽分享的字幕與筆記"],
  ["/videos/youtube/abc123", "<b>Talk</b>"],
  ["/videos/youtube/abc123?compare=v1,v2", "<b>Talk</b>"],
  ["/versions/v1", "字幕 · 校正 · en"],
  ["/takedown?version=v1", "下架申請"],
  ["/takedowns/t1", "下架申請的狀態"],
  ["/account", "帳號"],
  ["/admin/reports", "檢舉"],
  ["/admin/takedowns", "下架申請"],
  ["/admin/users", "使用者"],
  ["/admin/audit", "稽核紀錄"],
  ["/nope", "找不到這個頁面"],
];

describe("a11y", () => {
  it.each(routes)("%s", async (path, heading) => {
    mockApi({
      "GET /api/v1/me": f.meHandler(f.admin),
      "GET /api/v1/auth/providers": () => f.providers,
      "GET /api/v1/versions": () => ({ versions: [f.item, f.unverified] }),
      "GET /api/v1/versions/{id}": () => f.version,
      "GET /api/v1/versions/{id}/download": ({ params }) =>
        new Response(params.id === "v1" ? f.cuesA : f.cuesB),
      "GET /api/v1/videos/{platform}/{external_id}": () => f.videoPage,
      "GET /api/v1/takedowns/{id}": () => ({ id: "t1", state: "open" }),
      "GET /api/v1/me/tokens": () => ({ tokens: [] }),
      "GET /api/v1/me/shares": () => ({ shares: f.shares }),
      "GET /api/v1/admin/reports": () => ({ reports: [f.report] }),
      "GET /api/v1/admin/takedowns": () => ({ takedowns: [f.takedown] }),
      "GET /api/v1/admin/users": () => ({ users: f.users }),
      "GET /api/v1/admin/audit": () => ({
        entries: [
          {
            actor: "root",
            action: "user.suspend",
            target_kind: "user",
            target_id: "u2",
            at: "2026-11-22T00:00:00Z",
          },
        ],
      }),
    });
    renderRoute(path);
    await screen.findByRole("heading", { level: 1, name: heading }, { timeout: 5000 });
    if (path.includes("compare"))
      await screen.findByRole("heading", { level: 2, name: "並排比較" }, { timeout: 5000 });
    await waitFor(() => expect(document.querySelector("[role=status] .spinner__ring")).toBeNull());
    await expectNoA11yViolations();
  });
});
