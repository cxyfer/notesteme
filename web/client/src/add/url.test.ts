import { describe, expect, it } from "vitest";

import { isEditableTarget } from "../shell/keyboard";
import { isHttpUrl, parseParts } from "./url";

describe("isHttpUrl", () => {
  it("只接受單行的 http(s) 網址", () => {
    expect(isHttpUrl("https://www.youtube.com/watch?v=abc")).toBe(true);
    expect(isHttpUrl("  http://example.com/feed.xml  ")).toBe(true);
    expect(isHttpUrl("ftp://example.com")).toBe(false);
    expect(isHttpUrl("javascript:alert(1)")).toBe(false);
    expect(isHttpUrl("https://a.com\nhttps://b.com")).toBe(false);
    expect(isHttpUrl("hello world")).toBe(false);
    expect(isHttpUrl("")).toBe(false);
  });
});

describe("parseParts", () => {
  it("逗號、空白、範圍；去重並排序", () => {
    expect(parseParts("3, 1，5-7 1")).toEqual([1, 3, 5, 6, 7]);
  });
  it("格式不對、0、倒序或太大的範圍回傳 undefined", () => {
    expect(parseParts("")).toBeUndefined();
    expect(parseParts("a")).toBeUndefined();
    expect(parseParts("0")).toBeUndefined();
    expect(parseParts("5-3")).toBeUndefined();
    expect(parseParts("1-5000")).toBeUndefined();
  });
});

describe("isEditableTarget", () => {
  it("文字輸入框、textarea、contenteditable 算；勾選框與按鈕不算", () => {
    const el = (html: string) => {
      const d = document.createElement("div");
      d.innerHTML = html;
      return d.firstElementChild!;
    };
    expect(isEditableTarget(el("<input type=text>"))).toBe(true);
    expect(isEditableTarget(el("<textarea></textarea>"))).toBe(true);
    expect(isEditableTarget(el("<input type=checkbox>"))).toBe(false);
    expect(isEditableTarget(el("<button></button>"))).toBe(false);
    const ce = el("<div></div>") as HTMLElement;
    Object.defineProperty(ce, "isContentEditable", { value: true });
    expect(isEditableTarget(ce)).toBe(true);
    expect(isEditableTarget(null)).toBe(false);
  });
});
