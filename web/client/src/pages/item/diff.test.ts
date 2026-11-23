import { describe, expect, it } from "vitest";

import { alignCues, cueAt, joinText, parseCueTime, toCues, type Cue } from "./cues";
import { diffChars, hasChanges } from "./diff";

describe("diffChars", () => {
  it("逐字標出刪除與新增", () => {
    expect(diffChars("比如说daily stand-up", "比如說 daily stand-up")).toEqual([
      { type: "eq", text: "比如" },
      { type: "del", text: "说" },
      { type: "ins", text: "說 " },
      { type: "eq", text: "daily stand-up" },
    ]);
  });

  it("相同時只有 eq；空字串沒有片段", () => {
    expect(diffChars("abc", "abc")).toEqual([{ type: "eq", text: "abc" }]);
    expect(diffChars("", "")).toEqual([]);
    expect(hasChanges(diffChars("abc", "abc"))).toBe(false);
  });

  it("不切開 surrogate pair（emoji、擴充區漢字）", () => {
    const parts = diffChars("好😀", "好😃");
    expect(parts).toEqual([
      { type: "eq", text: "好" },
      { type: "del", text: "😀" },
      { type: "ins", text: "😃" },
    ]);
  });

  it("把兩邊的文字完整保留（eq+del 是原文，eq+ins 是新文）", () => {
    const before = "原本的目的是提升团队的信息同步";
    const after = "原本的目的是提升團隊的資訊同步";
    const parts = diffChars(before, after);
    expect(
      parts
        .filter((p) => p.type !== "ins")
        .map((p) => p.text)
        .join(""),
    ).toBe(before);
    expect(
      parts
        .filter((p) => p.type !== "del")
        .map((p) => p.text)
        .join(""),
    ).toBe(after);
  });
});

describe("cues", () => {
  it("parseCueTime 接受 HH:MM:SS.mmm、MM:SS 與秒數", () => {
    expect(parseCueTime("00:01:05.500")).toBe(65.5);
    expect(parseCueTime("01:05")).toBe(65);
    expect(parseCueTime("12.5")).toBe(12.5);
    expect(parseCueTime("1::2")).toBeNaN();
  });

  it("toCues 丟掉時間不正確的句子", () => {
    expect(
      toCues([
        { index: 1, start: "00:00:01.000", end: "00:00:02.000", text: "a" },
        { index: 2, start: "x", end: "00:00:03.000", text: "b" },
      ]).map((c) => c.index),
    ).toEqual([1]);
  });

  it("cueAt 用二分搜尋找目前的句子", () => {
    const cs = toCues([
      { index: 1, start: "00:00:01", end: "00:00:02", text: "a" },
      { index: 2, start: "00:00:05", end: "00:00:06", text: "b" },
    ]);
    expect(cueAt(cs, 0)).toBe(-1);
    expect(cueAt(cs, 1)).toBe(0);
    expect(cueAt(cs, 4)).toBe(0);
    expect(cueAt(cs, 9)).toBe(1);
  });

  const c = (index: number, start: number, end: number, text = String(index)): Cue => ({
    index,
    start,
    end,
    text,
  });

  it("alignCues 依時間重疊分組：合併與拆句都對得上", () => {
    const before = [c(1, 0, 2), c(2, 2, 4), c(3, 4, 6), c(4, 10, 12)];
    const after = [c(1, 0, 4, "1+2"), c(2, 4, 5), c(3, 5, 6), c(4, 20, 22)];
    const groups = alignCues(before, after);
    expect(groups.map((g) => [g.a.map((x) => x.index), g.b.map((x) => x.index)])).toEqual([
      [[1, 2], [1]],
      [[3], [2, 3]],
      [[4], []],
      [[], [4]],
    ]);
  });

  it("alignCues：相鄰句子只重疊幾毫秒時不串成一組", () => {
    const before = [c(1, 0, 2.005), c(2, 2, 4.003), c(3, 4, 6)];
    const after = [c(1, 0, 2.004), c(2, 2.001, 4), c(3, 3.998, 6)];
    expect(alignCues(before, after).map((g) => [g.a.length, g.b.length])).toEqual([
      [1, 1],
      [1, 1],
      [1, 1],
    ]);
  });

  it("joinText：中日韓直接相接，其他語言用空白", () => {
    expect(joinText([c(1, 0, 1, "你好"), c(2, 1, 2, "世界")], "zh-Hans")).toBe("你好世界");
    expect(joinText([c(1, 0, 1, "hello"), c(2, 1, 2, "world")], "en")).toBe("hello world");
  });
});
