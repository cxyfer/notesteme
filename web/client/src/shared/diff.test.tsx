import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { diffChars, DiffText, hasChanges } from "@shared";

import { expectNoA11yViolations } from "../test/axe";

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
    expect(hasChanges(diffChars("abc", "abd"))).toBe(true);
  });

  it("不切開 surrogate pair", () => {
    expect(diffChars("a😀b", "a😁b")).toEqual([
      { type: "eq", text: "a" },
      { type: "del", text: "😀" },
      { type: "ins", text: "😁" },
      { type: "eq", text: "b" },
    ]);
  });
});

describe("DiffText", () => {
  it("刪除與新增用 del／ins 並附螢幕閱讀器標籤，內容是純文字", async () => {
    const { container } = render(
      <p>
        <DiffText
          parts={diffChars("<b>说</b>", "<b>說</b>")}
          labels={{ del: "刪除：", ins: "新增：" }}
          lang="zh-Hant"
        />
      </p>,
    );
    expect(container.querySelector("del")?.textContent).toBe("刪除：说");
    expect(container.querySelector("ins")?.textContent).toBe("新增：說");
    expect(container.querySelector("b")).toBeNull();
    await expectNoA11yViolations(container);
  });
});
