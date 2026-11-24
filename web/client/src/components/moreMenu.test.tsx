import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { expectNoA11yViolations } from "../test/axe";
import { MoreMenu } from "./index";

describe("MoreMenu", () => {
  it("展開後列出項目；選了項目就收起並把焦點還給「更多」；Esc 也會收起", async () => {
    const onDelete = vi.fn();
    const { container } = render(
      <MoreMenu items={[{ label: "刪除…", danger: true, onSelect: onDelete }]} />,
    );
    const more = screen.getByRole("button", { name: "更多" });
    expect(more.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByRole("button", { name: "刪除…" })).toBeNull();

    await userEvent.click(more);
    expect(more.getAttribute("aria-expanded")).toBe("true");
    await expectNoA11yViolations(container);
    await userEvent.click(screen.getByRole("button", { name: "刪除…" }));
    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("button", { name: "刪除…" })).toBeNull();
    expect(document.activeElement).toBe(more);

    await userEvent.click(more);
    await userEvent.keyboard("{Escape}");
    expect(more.getAttribute("aria-expanded")).toBe("false");
    expect(document.activeElement).toBe(more);
  });

  it("點選單外面就收起", async () => {
    render(
      <>
        <MoreMenu items={[{ label: "刪除…", onSelect: () => {} }]} />
        <p>外面</p>
      </>,
    );
    await userEvent.click(screen.getByRole("button", { name: "更多" }));
    await userEvent.click(screen.getByText("外面"));
    expect(screen.queryByRole("button", { name: "刪除…" })).toBeNull();
  });
});
