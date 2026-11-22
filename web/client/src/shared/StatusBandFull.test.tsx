import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import { StatusBand, tickInterval, type StatusBandStage } from "@shared";

import { expectNoA11yViolations } from "../test/axe";

const stages: StatusBandStage[] = [
  { key: "transcript", status: "done", label: "字幕：完成" },
  {
    key: "correct",
    status: "running",
    label: "校正：處理中",
    segments: [
      { from: 0, to: 300, status: "done" },
      { from: 300, to: 400, status: "running" },
    ],
  },
  { key: "translate", status: "skipped", label: "翻譯：已略過" },
];

const fmt = (s: number) => `${Math.floor(s / 60)}:${String(Math.floor(s % 60)).padStart(2, "0")}`;

function Controlled({ onSeek }: { onSeek?: (s: number) => void }) {
  const [pos, setPos] = useState(60);
  return (
    <StatusBand
      variant="full"
      stages={stages}
      label="時間帶"
      durationS={900}
      position={pos}
      formatTime={fmt}
      valueText={(t) => `播放位置 ${t}`}
      onSeek={(s) => {
        setPos(s);
        onSeek?.(s);
      }}
    />
  );
}

describe("StatusBand variant=full", () => {
  it("是 role=slider，aria-valuetext 是時間碼；沒有 axe 問題", async () => {
    const { container } = render(<Controlled />);
    const slider = screen.getByRole("slider", { name: "時間帶" });
    expect(slider.getAttribute("aria-valuemax")).toBe("900");
    expect(slider.getAttribute("aria-valuenow")).toBe("60");
    expect(slider.getAttribute("aria-valuetext")).toBe("播放位置 1:00");
    await expectNoA11yViolations(container);
  });

  it("方向鍵移動 5 秒、PageUp 30 秒、Home／End 到頭尾，且不超出範圍", async () => {
    const onSeek = vi.fn();
    render(<Controlled onSeek={onSeek} />);
    const slider = screen.getByRole("slider");
    slider.focus();
    await userEvent.keyboard("{ArrowRight}");
    expect(onSeek).toHaveBeenLastCalledWith(65);
    await userEvent.keyboard("{ArrowLeft}{ArrowLeft}");
    expect(onSeek).toHaveBeenLastCalledWith(55);
    await userEvent.keyboard("{PageUp}");
    expect(onSeek).toHaveBeenLastCalledWith(85);
    await userEvent.keyboard("{End}");
    expect(onSeek).toHaveBeenLastCalledWith(900);
    await userEvent.keyboard("{ArrowRight}");
    expect(onSeek).toHaveBeenLastCalledWith(900);
    await userEvent.keyboard("{Home}");
    expect(onSeek).toHaveBeenLastCalledWith(0);
    expect(slider.getAttribute("aria-valuetext")).toBe("播放位置 0:00");
  });

  it("點擊依位置換算成時間；hover 顯示時間碼與說明", () => {
    const onSeek = vi.fn();
    render(
      <StatusBand
        variant="full"
        stages={stages}
        label="x"
        durationS={1000}
        formatTime={fmt}
        describeTime={(s) => (s >= 500 ? "後半段的字幕" : "")}
        describeLang="zh-Hans"
        onSeek={onSeek}
      />,
    );
    const slider = screen.getByRole("slider");
    slider.getBoundingClientRect = () =>
      ({ left: 100, width: 200, top: 0, height: 100, right: 300, bottom: 100 }) as DOMRect;
    fireEvent.click(slider, { clientX: 150 });
    expect(onSeek).toHaveBeenCalledWith(250);
    fireEvent.pointerMove(slider, { clientX: 250 });
    const text = screen.getByText("後半段的字幕");
    expect(text.getAttribute("lang")).toBe("zh-Hans");
    expect(screen.getByText("12:30")).toBeTruthy();
    fireEvent.pointerLeave(slider);
    expect(screen.queryByText("後半段的字幕")).toBeNull();
  });

  it("標籤逐列顯示文字（不只靠顏色）；略過的步驟不畫；處理範圍畫在底軌", () => {
    const { container } = render(<Controlled />);
    const labels = [...container.querySelectorAll(".sband__label")].map((e) => e.textContent);
    expect(labels).toEqual(["字幕：完成", "校正：處理中"]);
    expect(container.querySelectorAll(".sband__range--running")).toHaveLength(1);
    expect(container.querySelector(".sband__playhead")).toBeTruthy();
  });

  it("沒有長度時停用 slider", async () => {
    const onSeek = vi.fn();
    render(<StatusBand variant="full" stages={stages} label="x" durationS={null} onSeek={onSeek} />);
    const slider = screen.getByRole("slider");
    expect(slider.getAttribute("aria-disabled")).toBe("true");
    slider.focus();
    await userEvent.keyboard("{ArrowRight}");
    expect(onSeek).not.toHaveBeenCalled();
  });

  it("刻度間距讓整條大約 4～10 個刻度", () => {
    expect(tickInterval(60)).toBe(10);
    expect(tickInterval(933)).toBe(120);
    expect(tickInterval(7200)).toBe(900);
  });
});
