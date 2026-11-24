import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { expectNoA11yViolations } from "../test/axe";
import { defaultRoutes } from "../test/fixtures";
import { mockApi } from "../test/mockApi";
import { renderRoute, signIn } from "../test/render";

/** jsdom 沒有 matchMedia：模擬 767px 以下的視窗 */
function narrow(matches: boolean) {
  vi.stubGlobal(
    "matchMedia",
    (query: string) =>
      ({
        matches,
        media: query,
        addEventListener: () => {},
        removeEventListener: () => {},
      }) as unknown as MediaQueryList,
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("手機的底部分頁列（README §8，UI-8）", () => {
  it("767px 以下：總覽、媒體庫、新增、佇列、更多；側邊導覽不渲染（axe 無違規）", async () => {
    narrow(true);
    mockApi(defaultRoutes());
    signIn();
    const { container } = renderRoute("/library");
    const bar = await screen.findByRole("navigation", { name: "分頁列" });
    expect(screen.queryByRole("navigation", { name: "主要導覽" })).toBeNull();
    expect(
      within(bar)
        .getAllByRole("listitem")
        .map((li) => li.textContent),
    ).toEqual(["總覽", "媒體庫", "新增", "佇列", "更多"]);
    expect(within(bar).getByRole("link", { name: "媒體庫" }).getAttribute("aria-current")).toBe(
      "page",
    );
    await expectNoA11yViolations(container);
  });

  it("「更多」列出其餘頁面、偏好與登出；點頁面後關閉", async () => {
    narrow(true);
    mockApi(defaultRoutes());
    signIn();
    const { router } = renderRoute("/library");
    const user = userEvent.setup();
    const bar = await screen.findByRole("navigation", { name: "分頁列" });
    await user.click(within(bar).getByRole("button", { name: "更多" }));
    const dialog = await screen.findByRole("dialog", { name: "更多" });
    expect(within(dialog).queryByRole("link", { name: "媒體庫" })).toBeNull();
    expect(within(dialog).getByRole("button", { name: "登出" })).toBeTruthy();
    await expectNoA11yViolations(dialog);
    await user.click(within(dialog).getByRole("link", { name: "成本" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/costs"));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "更多" })).toBeNull());
  });

  it("寬螢幕只有側邊導覽", async () => {
    narrow(false);
    mockApi(defaultRoutes());
    signIn();
    renderRoute("/library");
    expect(await screen.findByRole("navigation", { name: "主要導覽" })).toBeTruthy();
    expect(screen.queryByRole("navigation", { name: "分頁列" })).toBeNull();
  });
});
