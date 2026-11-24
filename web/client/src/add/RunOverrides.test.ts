import { describe, expect, it } from "vitest";

import { buildOverrides, EMPTY_RUN_OVERRIDES, parseTargets } from "./RunOverrides";

describe("buildOverrides", () => {
  it("沒有調整時不帶 overrides", () => {
    expect(buildOverrides(EMPTY_RUN_OVERRIDES, ["supadata", "groq"])).toBeUndefined();
  });

  it("字幕來源改回原本的順序、全部勾選時不送（list 是整個取代）", () => {
    const sources = [
      { id: "supadata", on: true },
      { id: "groq", on: true },
    ];
    expect(
      buildOverrides({ ...EMPTY_RUN_OVERRIDES, sources }, ["supadata", "groq"]),
    ).toBeUndefined();
    expect(
      buildOverrides(
        { ...EMPTY_RUN_OVERRIDES, sources: [sources[0]!, { id: "groq", on: false }] },
        ["supadata", "groq"],
      ),
    ).toEqual({ language: { fallback: ["supadata"] } });
    // 全部取消：只用平台字幕
    expect(
      buildOverrides(
        { ...EMPTY_RUN_OVERRIDES, sources: sources.map((x) => ({ ...x, on: false })) },
        ["supadata", "groq"],
      ),
    ).toEqual({ language: { fallback: [] } });
  });

  it("目標語言可以用逗號、頓號或空白分隔，去重", () => {
    expect(parseTargets(" ja，en  ja、ko ")).toEqual(["ja", "en", "ko"]);
    expect(buildOverrides({ ...EMPTY_RUN_OVERRIDES, targets: "ja" }, undefined)).toEqual({
      language: { targets: ["ja"] },
    });
  });

  it("不做的步驟送 executor: off", () => {
    expect(buildOverrides({ ...EMPTY_RUN_OVERRIDES, skip: ["frames", "note"] }, undefined)).toEqual(
      {
        stages: { frames: { executor: "off" }, note: { executor: "off" } },
      },
    );
  });
});
