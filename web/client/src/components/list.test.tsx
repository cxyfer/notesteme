import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { StatusBand } from "@shared";

import { expectNoA11yViolations } from "../test/axe";
import {
  allowedImageUrl,
  EmptyState,
  ErrorCallout,
  LoadMore,
  Thumbnail,
  thumbnailUrl,
  VirtualList,
} from "./index";

describe("Thumbnail", () => {
  it("YouTube 由影片 ID 推得縮圖；ID 格式不對時用占位", async () => {
    expect(thumbnailUrl("youtube", "dQw4w9WgXcQ")).toBe(
      "https://i.ytimg.com/vi/dQw4w9WgXcQ/mqdefault.jpg",
    );
    expect(thumbnailUrl("youtube", "../../x")).toBeUndefined();
    expect(thumbnailUrl("bilibili", "BV1xx411c7mD")).toBeUndefined();
    const { container } = render(
      <Thumbnail platform="bilibili" externalId="BV1" placeholder="Bilibili" />,
    );
    expect(container.querySelector(".thumb--placeholder")?.textContent).toBe("Bilibili");
    await expectNoA11yViolations(container);
  });

  it("只接受 CSP 允許的 https 網域；載入失敗時換成占位", () => {
    expect(allowedImageUrl("https://i0.hdslb.com/bfs/a.jpg")).toBe(
      "https://i0.hdslb.com/bfs/a.jpg",
    );
    expect(allowedImageUrl("http://i.ytimg.com/a.jpg")).toBeUndefined();
    expect(allowedImageUrl("https://evil.example/a.jpg")).toBeUndefined();
    expect(allowedImageUrl("https://ytimg.com.evil.example/a.jpg")).toBeUndefined();
    const { container } = render(
      <Thumbnail platform="youtube" externalId="dQw4w9WgXcQ" placeholder="YouTube" />,
    );
    const img = container.querySelector("img")!;
    expect(img.getAttribute("alt")).toBe("");
    fireEvent.error(img);
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector(".thumb--placeholder")).toBeTruthy();
  });
});

describe("EmptyState 與 LoadMore", () => {
  it("空狀態附上動作按鈕", async () => {
    const onClick = vi.fn();
    const { container } = render(
      <EmptyState message="沒有符合條件的影音。" action={{ label: "清除篩選條件", onClick }} />,
    );
    await userEvent.click(screen.getByRole("button", { name: "清除篩選條件" }));
    expect(onClick).toHaveBeenCalled();
    await expectNoA11yViolations(container);
  });

  it("分頁載入器：數量、載入更多、錯誤時重試", async () => {
    const onLoadMore = vi.fn();
    const { container, rerender } = render(
      <LoadMore loaded={50} total={120} hasMore loading={false} onLoadMore={onLoadMore} />,
    );
    expect(screen.getByText("已載入 50／共 120 部")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "載入更多" }));
    expect(onLoadMore).toHaveBeenCalledTimes(1);
    await expectNoA11yViolations(container);
    rerender(
      <LoadMore
        loaded={50}
        total={120}
        hasMore
        loading={false}
        error={new Error("x")}
        onLoadMore={onLoadMore}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: "重試" }));
    expect(onLoadMore).toHaveBeenCalledTimes(2);
  });

  it("ErrorCallout 的 live=false 不是 role=alert", () => {
    render(<ErrorCallout error={new Error("x")} message="m" live={false} />);
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByText("m")).toBeTruthy();
  });
});

describe("VirtualList", () => {
  it("10,000 筆只渲染可視範圍加 overscan；aria-setsize 是總數", async () => {
    const items = Array.from({ length: 10_000 }, (_, i) => `第 ${i + 1} 列`);
    const { container } = render(
      <VirtualList
        label="列表"
        items={items}
        total={20_000}
        getKey={(s) => s}
        estimateSize={50}
        overscan={5}
        renderItem={(s, { focusProps }) => (
          <a href="#x" {...focusProps}>
            {s}
          </a>
        )}
      />,
    );
    const rows = screen.getAllByRole("listitem");
    // 800px ÷ 50px = 16 列，加上 overscan 5
    expect(rows.length).toBeGreaterThanOrEqual(16);
    expect(rows.length).toBeLessThanOrEqual(16 + 5 + 1);
    expect(rows[0]!.getAttribute("aria-setsize")).toBe("20000");
    expect(rows[3]!.getAttribute("aria-posinset")).toBe("4");
    expect(container.querySelector(".vlist__inner")!.getAttribute("style")).toContain(
      "height: 500000px",
    );
    await expectNoA11yViolations(container);
  });

  it("可以 Tab 進列表的列一定在渲染範圍內（active 的列被捲走時改用第一個渲染出來的列）", () => {
    const items = Array.from({ length: 1000 }, (_, i) => `第 ${i + 1} 列`);
    const { container } = render(
      <VirtualList
        label="列表"
        items={items}
        getKey={(s) => s}
        estimateSize={50}
        renderItem={(s, { focusProps }) => (
          <a href="#x" {...focusProps}>
            {s}
          </a>
        )}
      />,
    );
    const scroller = container.querySelector<HTMLElement>(".vlist")!;
    scroller.scrollTop = 20_000;
    fireEvent.scroll(scroller);
    const tabbable = [...container.querySelectorAll<HTMLElement>("[data-row-focus]")].filter(
      (a) => a.tabIndex === 0,
    );
    expect(tabbable).toHaveLength(1);
    expect(tabbable[0]!.textContent).not.toBe("第 1 列");
  });

  it("接近最後一列時呼叫 onEndReached", () => {
    const onEnd = vi.fn();
    render(
      <VirtualList
        label="列表"
        items={["a", "b", "c"]}
        getKey={(s) => s}
        onEndReached={onEnd}
        renderItem={(s) => <span>{s}</span>}
      />,
    );
    expect(onEnd).toHaveBeenCalled();
  });
});

describe("StatusBand 在 Client 的 token 下", () => {
  it("沒有 axe 問題", async () => {
    const { container } = render(
      <StatusBand
        label="字幕：完成，校正：失敗"
        stages={[
          { key: "transcript", status: "done", label: "字幕：完成" },
          { key: "correct", status: "failed", label: "校正：失敗" },
        ]}
      />,
    );
    await expectNoA11yViolations(container);
  });
});
