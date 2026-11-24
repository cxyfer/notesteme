import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { expectNoA11yViolations } from "../test/axe";

import { StatusBand, type StatusBandStage } from "@shared";

const stages: StatusBandStage[] = [
  { key: "transcript", status: "done", label: "字幕：完成" },
  { key: "correct", status: "failed", label: "校正：失敗" },
  { key: "translate", status: "skipped", label: "翻譯：已略過" },
];

describe("StatusBand", () => {
  it("是 role=img，文字摘要放在 aria-label", () => {
    render(<StatusBand stages={stages} label="字幕完成，校正失敗" />);
    expect(screen.getByRole("img", { name: "字幕完成，校正失敗" })).toBeTruthy();
  });

  it("略過的步驟不畫帶子；每條帶子的狀態用不同的 class（紋理）", () => {
    const { container } = render(<StatusBand stages={stages} label="x" />);
    const bands = [...container.querySelectorAll<HTMLElement>(".sband__band")];
    expect(bands.map((b) => b.dataset.stage)).toEqual(["transcript", "correct"]);
    expect(bands[1]!.title).toBe("校正：失敗");
    expect(bands[1]!.querySelector(".sband__seg--failed")).toBeTruthy();
  });

  it("segments 依影音長度換算位置，超出範圍的部分裁掉", () => {
    const { container } = render(
      <StatusBand
        label="x"
        durationS={100}
        stages={[
          {
            key: "correct",
            status: "running",
            label: "校正 1/2",
            segments: [
              { from: 0, to: 50, status: "done" },
              { from: 50, to: 150, status: "running" },
            ],
          },
        ]}
      />,
    );
    const segs = [...container.querySelectorAll<HTMLElement>(".sband__seg")];
    expect(segs.map((s) => [s.dataset.status, s.style.left, s.style.width])).toEqual([
      ["done", "0%", "50%"],
      ["running", "50%", "50%"],
    ]);
  });

  it("沒有長度時忽略 segments，整條用步驟的狀態", () => {
    const { container } = render(
      <StatusBand
        label="x"
        durationS={null}
        stages={[
          {
            key: "correct",
            status: "running",
            label: "校正",
            segments: [{ from: 0, to: 5, status: "done" }],
          },
        ]}
      />,
    );
    const segs = [...container.querySelectorAll<HTMLElement>(".sband__seg")];
    expect(segs.map((s) => [s.dataset.status, s.style.width])).toEqual([["running", "100%"]]);
  });

  describe("精簡版的 onSeek（pages.md §1、§3）", () => {
    function rect(el: Element, left: number, width: number) {
      el.getBoundingClientRect = () =>
        ({
          left,
          width,
          top: 0,
          height: 30,
          right: left + width,
          bottom: 30,
          x: left,
          y: 0,
        }) as DOMRect;
    }

    it("role=img 不變、不變成互動元素；另有可聚焦的按鈕，點擊位置的比例乘上長度（axe 無違規）", async () => {
      const onSeek = vi.fn();
      const { container } = render(
        <StatusBand
          stages={stages}
          label="字幕完成，校正失敗"
          durationS={200}
          onSeek={onSeek}
          seekLabel="打開「影片」並定位到點擊的時間"
          className="row-band"
        />,
      );
      const img = screen.getByRole("img", { name: "字幕完成，校正失敗" });
      expect(img.tabIndex).toBe(-1);
      expect(img.onclick).toBeNull();
      // 版面用的 className 留在最外層
      expect(container.firstElementChild!.classList.contains("row-band")).toBe(true);
      const button = screen.getByRole("button", { name: "打開「影片」並定位到點擊的時間" });
      // 按鈕蓋住了每條帶子的 title，滑過時顯示整條的摘要
      expect(button.title).toBe("字幕完成，校正失敗");
      rect(button, 100, 400);
      fireEvent.click(button, { clientX: 200, detail: 1 });
      expect(onSeek).toHaveBeenLastCalledWith(50);
      fireEvent.click(button, { clientX: 900, detail: 1 });
      expect(onSeek).toHaveBeenLastCalledWith(200);
      // 鍵盤按下（detail 0）沒有位置：從頭打開
      fireEvent.click(button, { clientX: 0, detail: 0 });
      expect(onSeek).toHaveBeenLastCalledWith(0);
      await expectNoA11yViolations(container);
    });

    it("沒有長度或沒有 seekLabel 時不提供點擊，結構和原本相同", () => {
      const { container, rerender } = render(
        <StatusBand
          stages={stages}
          label="x"
          durationS={null}
          onSeek={() => undefined}
          seekLabel="y"
        />,
      );
      expect(screen.queryByRole("button")).toBeNull();
      expect(container.firstElementChild!.getAttribute("role")).toBe("img");
      rerender(<StatusBand stages={stages} label="x" durationS={100} onSeek={() => undefined} />);
      expect(screen.queryByRole("button")).toBeNull();
    });
  });
});
