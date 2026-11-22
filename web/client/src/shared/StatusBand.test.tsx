import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

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
});
