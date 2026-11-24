import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { defaultRoutes, detail, item, stage } from "../../test/fixtures";
import { mockApi } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";

type Detail = ReturnType<typeof detail>;

function open(over: Partial<Detail>) {
  mockApi({
    ...defaultRoutes(),
    "GET /api/items/{id}": () => ({
      ...detail(item(1), [stage("metadata", "done")]),
      ...over,
    }),
  });
  signIn();
  return renderRoute("/items/item-1");
}

describe("影音詳情：從 bundle 匯入（BND N24）", () => {
  it("顯示來源實例、版本與匯入時間；不可信字串當純文字（axe 無違規）", async () => {
    const { container } = open({
      imported_from: {
        instance_id: "<img src=x onerror=alert(1)>",
        app_version: "0.4.0",
        bundle_sha256: "ab".repeat(32),
        imported_at: "2026-11-20T08:00:00Z",
      },
    });
    expect(await screen.findByText("從 bundle 匯入")).toBeTruthy();
    expect(
      screen.getByText(/實例 <img src=x onerror=alert\(1\)>（Notesteme 0\.4\.0）/),
    ).toBeTruthy();
    expect(container.querySelector("img[src='x']")).toBeNull();
    await expectNoA11yViolations(container);
  });

  it("實例 ID 是空字串時寫「不明」", async () => {
    open({
      imported_from: {
        instance_id: "",
        app_version: "0.4.0",
        bundle_sha256: "",
        imported_at: "2026-11-20T08:00:00Z",
      },
    });
    expect(await screen.findByText(/實例 不明/)).toBeTruthy();
  });

  it("imported_from 為 null 時沒有這一欄", async () => {
    open({ imported_from: null });
    await screen.findByRole("heading", { level: 1, name: "影片 1" });
    expect(screen.queryByText("從 bundle 匯入")).toBeNull();
  });
});
