import { screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { a11yRoutes } from "./a11yRoutes";
import { expectNoA11yViolations } from "./axe";
import { defaultRoutes } from "./fixtures";
import { mockApi } from "./mockApi";
import { renderRoute, signIn } from "./render";

describe("每個頁面沒有 serious／critical 的 axe 問題", () => {
  // 頁面會讀 API：一律用替身，不連網路
  beforeEach(() => {
    mockApi(defaultRoutes());
  });
  for (const r of a11yRoutes) {
    it(r.path, async () => {
      if (r.auth) signIn();
      const { container } = renderRoute(r.path);
      expect(await screen.findByRole("heading", { level: 1, name: r.heading })).toBeTruthy();
      if (r.ready) expect((await screen.findAllByText(r.ready)).length).toBeGreaterThan(0);
      // 每頁只有一個 <main>，且有 skip link 可以到達（登入頁與 404 不需要導覽）
      expect(container.querySelectorAll("main")).toHaveLength(1);
      await expectNoA11yViolations(container);
    });
  }
});
