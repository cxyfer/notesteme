import { screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { createQueryClient } from "../api/queryClient";
import { expectNoA11yViolations } from "../test/axe";
import { item } from "../test/fixtures";
import { mockApi } from "../test/mockApi";
import { ItemPicker, SelectField } from "./index";

describe("SelectField", () => {
  it("標籤、說明與錯誤以 aria 連到 select（axe 無違規）", async () => {
    const { container } = render(
      <SelectField
        label="執行方式"
        hint="沒填時沿用全域設定"
        error="必填。"
        options={[
          { value: "", label: "沿用全域設定" },
          { value: "api", label: "API 處理" },
        ]}
        defaultValue="api"
      />,
    );
    const select = screen.getByLabelText("執行方式");
    expect(select.getAttribute("aria-invalid")).toBe("true");
    expect(select.getAttribute("aria-describedby")?.split(" ")).toHaveLength(2);
    await expectNoA11yViolations(container);
  });
});

describe("ItemPicker", () => {
  it("列出最近的影音；目前值不在結果中時仍保留選項（axe 無違規）", async () => {
    const api = mockApi({
      "GET /api/items": () => ({ items: [item(1, { id: "item-1", title: "第一部" })], total: 1 }),
    });
    const onChange = vi.fn();
    const { container } = render(
      <QueryClientProvider client={createQueryClient()}>
        <ItemPicker value="item-9" onChange={onChange} />
      </QueryClientProvider>,
    );
    expect(await screen.findByRole("option", { name: "第一部" })).toBeTruthy();
    expect(screen.getByRole("option", { name: "item-9" })).toBeTruthy();
    await userEvent.selectOptions(screen.getByLabelText("影音"), "item-1");
    expect(onChange).toHaveBeenCalledWith("item-1");
    expect(api.calls("GET", "/api/items")[0]!.url.searchParams.get("limit")).toBe("50");
    await expectNoA11yViolations(container);
  });
});
