import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoA11yViolations } from "../../test/axe";
import { defaultRoutes, detail, item, stage } from "../../test/fixtures";
import { mockApi } from "../../test/mockApi";
import { renderRoute, signIn } from "../../test/render";

function withBetter(estimate: number | null) {
  return () => ({
    ...detail(item(1), [
      stage("metadata", "done"),
      stage("transcript", "done"),
      stage("correct", "done"),
    ]),
    better_source: {
      source: "youtube-manual",
      lang: "en",
      kind: "manual",
      found_at: "2026-11-21T00:00:00Z",
      estimate_usd: estimate,
    },
    reprobe: {
      next_at: "2026-11-22T00:00:00Z",
      until: "2026-11-28T00:00:00Z",
      runs: 1,
      sources: true,
      sponsorblock: false,
    },
  });
}

describe("影音詳情：更好的字幕來源（SRC-10）", () => {
  it("無法估算時明寫；改用更好的字幕重跑 = 從字幕步驟重新處理（axe 無違規）", async () => {
    const api = mockApi({
      ...defaultRoutes(),
      "GET /api/items/{id}": withBetter(null),
      "POST /api/items/{id}/rerun": () => detail(item(1)),
    });
    signIn();
    const { container } = renderRoute("/items/item-1");
    const notice = await screen.findByRole("region", { name: "有更好的字幕來源" });
    expect(within(notice).getByText(/en 的人工字幕（youtube-manual）/)).toBeTruthy();
    expect(within(notice).getByText(/無法預估費用/)).toBeTruthy();
    expect(within(notice).queryByText(/\$0\.00/)).toBeNull();
    expect(screen.getByText(/之後會再檢查字幕來源：/)).toBeTruthy();
    await expectNoA11yViolations(container);

    const user = userEvent.setup();
    await user.click(within(notice).getByRole("button", { name: "改用更好的字幕重跑" }));
    const dialog = await screen.findByRole("dialog", { name: "重新處理" });
    await user.click(within(dialog).getByRole("button", { name: /^從.+重新處理$/ }));
    await waitFor(() => expect(api.calls("POST", "/api/items/item-1/rerun")).toHaveLength(1));
    const body = JSON.parse(api.calls("POST", "/api/items/item-1/rerun")[0]!.init.body as string);
    expect(body.from).toBe("transcript");
  });

  it("有預估時顯示金額", async () => {
    mockApi({ ...defaultRoutes(), "GET /api/items/{id}": withBetter(0.42) });
    signIn();
    renderRoute("/items/item-1");
    const notice = await screen.findByRole("region", { name: "有更好的字幕來源" });
    expect(within(notice).getByText(/預估花費 US\$0\.42/)).toBeTruthy();

    // SRC 的 @ui 場景：「改用更好的字幕重跑」在執行前顯示預估的 LLM 花費
    const user = userEvent.setup();
    await user.click(within(notice).getByRole("button", { name: "改用更好的字幕重跑" }));
    const dialog = await screen.findByRole("dialog", { name: "重新處理" });
    expect(
      within(dialog).getByText("費用預估：用目前的設定從字幕步驟做到最後，預估花費 US$0.42。"),
    ).toBeTruthy();
    // 換了起點，預估就不適用
    await user.selectOptions(within(dialog).getByLabelText("從哪個步驟開始"), "correct");
    expect(within(dialog).getByText("費用預估：重新處理目前無法預估花費。")).toBeTruthy();
  });
});
