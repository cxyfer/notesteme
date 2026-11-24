import { describe, expect, it } from "vitest";

import { item } from "../../test/fixtures";
import { groupCounts } from "./groups";

describe("groupCounts", () => {
  const rows = [
    item(1, { channel_id: "UCa" }),
    item(2, { channel_id: "UCa" }),
    item(3, { channel_id: "UCb" }),
  ];

  it("以每組第一列的 index 記數量；沒有下一頁時都是確定的", () => {
    expect([...groupCounts(rows, "channel", false)]).toEqual([
      [0, { count: 2, complete: true }],
      [2, { count: 1, complete: true }],
    ]);
  });

  it("還有下一頁時，最後一組可能沒數完", () => {
    expect(groupCounts(rows, "channel", true).get(2)).toEqual({ count: 1, complete: false });
    expect(groupCounts(rows, "channel", true).get(0)).toEqual({ count: 2, complete: true });
  });

  it("沒有列時是空的", () => {
    expect(groupCounts([], undefined, false).size).toBe(0);
  });
});
