import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { defaultRoutes, item } from "../../test/fixtures";
import { mockApi } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";
import { toApiQuery, validateLibrarySearch } from "./filters";

describe("媒體庫：等你決定（DIG）", () => {
  it("blocked=awaiting_user 換成最接近的 API 條件", () => {
    const s = validateLibrarySearch({ blocked: "awaiting_user", status: "segment:done" });
    expect(s.blocked).toBe("awaiting_user");
    expect(toApiQuery(s).status).toEqual([
      "summary:done",
      "summary:skipped",
      "segment:blocked",
      "segment:done",
    ]);
    expect(validateLibrarySearch({ blocked: "budget" }).blocked).toBeUndefined();
  });

  it("彙整的連結打開等你決定的檢視；確認後一次繼續已載入的影音（axe 無違規）", async () => {
    const ids = Array.from({ length: 101 }, (_, i) => `item-${i + 1}`);
    const bodies: { ids: string[] }[] = [];
    const api = mockApi({
      ...defaultRoutes(),
      "GET /api/items": () => ({ items: ids.map((_, i) => item(i + 1)), total: ids.length }),
      "POST /api/items/continue": (req) => {
        const b = req.body as { ids: string[] };
        bodies.push(b);
        return {
          continued: b.ids.slice(1),
          skipped: [{ id: b.ids[0], code: "not_awaiting_user" }],
        };
      },
    });
    signIn();
    const { container } = renderRoute("/library?blocked=awaiting_user");
    const user = userEvent.setup();
    const button = await screen.findByRole("button", { name: "繼續處理已載入的 101 部" });
    expect(screen.getByRole("button", { name: "等你決定" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    const q = api.calls("GET", "/api/items")[0]!.url.searchParams;
    expect(q.getAll("status")).toEqual(["summary:done", "summary:skipped", "segment:blocked"]);
    expect(q.get("blocked")).toBeNull();
    await expectNoA11yViolations(container);

    await user.click(button);
    const dialog = await screen.findByRole("dialog", { name: "繼續處理 101 部？" });
    await user.click(within(dialog).getByRole("button", { name: "繼續處理" }));
    expect(await screen.findByText("已繼續處理 99 部，略過 2 部")).toBeTruthy();
    expect(bodies.map((b) => b.ids.length)).toEqual([100, 1]);
    await waitFor(() => expect(api.calls("GET", "/api/items").length).toBeGreaterThan(1));
  });
});
