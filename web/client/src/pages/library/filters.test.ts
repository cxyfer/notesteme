import { describe, expect, it } from "vitest";

import {
  hasFilters,
  isValidDate,
  normalizeStatus,
  toApiQuery,
  toggleStatus,
  validateLibrarySearch,
} from "./filters";

describe("validateLibrarySearch", () => {
  it("只保留認得而且合法的參數", () => {
    expect(
      validateLibrarySearch({
        q: "  postgres  ",
        platform: "youtube",
        error_class: "nope",
        timing: "interpolated",
        extra: "x",
        dur_min: "5",
        dur_max: "-1",
        published_from: "2026-02-30",
        published_to: "2026-11-01",
      }),
    ).toEqual({
      q: "postgres",
      platform: "youtube",
      timing: "interpolated",
      dur_min: 5,
      published_to: "2026-11-01",
    });
  });

  it("TanStack 把數字、boolean 解析成非字串時轉回字串", () => {
    expect(validateLibrarySearch({ q: 123, channel: 42, lang: true, dur_min: 7 })).toEqual({
      q: "123",
      channel: "42",
      lang: "true",
      dur_min: 7,
    });
  });

  it("標題搜尋截到 200 字元；空白視為沒有條件", () => {
    expect(validateLibrarySearch({ q: "a".repeat(300) }).q).toHaveLength(200);
    expect(validateLibrarySearch({ q: "   " })).toEqual({});
  });
});

describe("normalizeStatus", () => {
  it("丟掉不認得的步驟或狀態、含逗號或多個冒號的值；去重複並排序", () => {
    expect(
      normalizeStatus([
        "normalize:done",
        "metadata:failed",
        "metadata:done",
        "metadata:done",
        "x:done",
        "metadata:nope",
        "metadata:done,normalize:done",
        "a:b:c",
      ]),
    ).toEqual(["metadata:done", "metadata:failed", "normalize:done"]);
    expect(normalizeStatus("correct:running")).toEqual(["correct:running"]);
    expect(normalizeStatus([])).toBeUndefined();
  });

  it("toggleStatus 加入與移除", () => {
    expect(toggleStatus(undefined, "correct:failed")).toEqual(["correct:failed"]);
    expect(toggleStatus(["correct:failed"], "correct:failed")).toBeUndefined();
  });
});

describe("toApiQuery", () => {
  it("日期換成當地時區當天的起訖、分鐘換成秒", () => {
    const q = toApiQuery({
      published_from: "2026-11-01",
      published_to: "2026-11-02",
      dur_min: 5,
      dur_max: 60,
      status: ["metadata:done"],
    });
    expect(q.published_from).toBe(new Date(2026, 10, 1).toISOString());
    expect(q.published_to).toBe(new Date(2026, 10, 2, 23, 59, 59, 999).toISOString());
    expect(q.duration_min).toBe("300");
    expect(q.duration_max).toBe("3600");
    expect(q.status).toEqual(["metadata:done"]);
  });
});

describe("其他", () => {
  it("isValidDate 拒絕不存在的日期", () => {
    expect(isValidDate("2026-02-28")).toBe(true);
    expect(isValidDate("2026-02-29")).toBe(false);
    expect(isValidDate("2026-1-1")).toBe(false);
  });

  it("hasFilters", () => {
    expect(hasFilters({})).toBe(false);
    expect(hasFilters({ platform: "local" })).toBe(true);
  });
});
