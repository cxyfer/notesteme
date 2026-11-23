// UI-5 的測試資料：搜尋、術語審核、彙整、系統告警與備份。
import type { components } from "../api/schema";

type S = components["schemas"];

export function searchResult(n: number, over: Partial<S["SearchResult"]> = {}): S["SearchResult"] {
  return {
    item: { id: `item-${n}`, ref: `yt:item${n}`, title: `影片 ${n}` },
    lang: "en",
    variant: "corrected",
    cue: 2,
    start: "00:00:04.000",
    end: "00:00:08.000",
    text: "we keep an ordered log of events",
    highlights: [{ start: 11, end: 22 }],
    before: "first we write",
    after: "and replay it",
    ...over,
  };
}
