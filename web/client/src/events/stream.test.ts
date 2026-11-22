import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { queryKeys } from "../api/queryKeys";
import { invalidationTable } from "./invalidation";
import { backoffDelay, connectEventStream } from "./stream";
import { EVENT_TYPES, type ServerEvent } from "./types";

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  listeners = new Map<string, Set<(e: MessageEvent) => void>>();
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  constructor(public url: string) {
    FakeEventSource.instances.push(this);
  }
  addEventListener(t: string, fn: (e: MessageEvent) => void) {
    if (!this.listeners.has(t)) this.listeners.set(t, new Set());
    this.listeners.get(t)!.add(fn);
  }
  removeEventListener(t: string, fn: (e: MessageEvent) => void) {
    this.listeners.get(t)?.delete(fn);
  }
  close() {
    this.closed = true;
  }
  emit(type: string, data: unknown) {
    for (const fn of this.listeners.get(type) ?? [])
      fn(new MessageEvent(type, { data: JSON.stringify(data) }));
  }
}

function fakeDoc() {
  let handler: (() => void) | undefined;
  return {
    hidden: false,
    addEventListener: (_: string, fn: () => void) => (handler = fn),
    removeEventListener: () => (handler = undefined),
    fire() {
      handler?.();
    },
  };
}

const latest = () => FakeEventSource.instances.at(-1)!;

beforeEach(() => {
  vi.useFakeTimers();
  FakeEventSource.instances = [];
});
afterEach(() => vi.useRealTimers());

function connect(extra: Partial<Parameters<typeof connectEventStream>[0]> = {}) {
  const onEvent = vi.fn();
  const onOpen = vi.fn();
  const onUnauthorized = vi.fn();
  const doc = fakeDoc();
  const stop = connectEventStream({
    onEvent,
    onOpen,
    onUnauthorized,
    EventSourceImpl: FakeEventSource as unknown as typeof EventSource,
    probe: async () => 200,
    random: () => 0.5,
    doc: doc as unknown as Document,
    ...extra,
  });
  return { onEvent, onOpen, onUnauthorized, doc, stop };
}

describe("connectEventStream", () => {
  it("訂閱每個具名事件型別，解析 data；不認得的事件略過", () => {
    const { onEvent } = connect();
    const es = latest();
    expect([...es.listeners.keys()].sort()).toEqual([...EVENT_TYPES].sort());
    es.emit("stage.status", {
      type: "stage.status",
      item: "yt:a",
      item_id: "1",
      stage: "fetch",
      status: "done",
    });
    es.emit("stage.status", "not json{");
    expect(onEvent).toHaveBeenCalledOnce();
    expect(onEvent.mock.calls[0]![0]).toMatchObject({
      type: "stage.status",
      item_id: "1",
      status: "done",
    });
  });

  it("斷線後以指數退避重連，連上時通知 onOpen 並重設退避", async () => {
    const { onOpen } = connect({ baseDelay: 1000, maxDelay: 4000 });
    latest().onopen!();
    expect(onOpen).toHaveBeenCalledTimes(1);
    const delays: number[] = [];
    for (let i = 0; i < 4; i++) {
      const before = FakeEventSource.instances.length;
      latest().onerror!();
      expect(latest().closed).toBe(true);
      let waited = 0;
      while (FakeEventSource.instances.length === before) {
        await vi.advanceTimersByTimeAsync(250);
        waited += 250;
      }
      delays.push(waited);
    }
    expect(delays).toEqual([1000, 2000, 4000, 4000]);
    latest().onopen!();
    expect(onOpen).toHaveBeenCalledTimes(2);
    const before = FakeEventSource.instances.length;
    latest().onerror!();
    await vi.advanceTimersByTimeAsync(1000);
    expect(FakeEventSource.instances.length).toBe(before + 1);
  });

  it("重連前探測到 401 就停止並通知 onUnauthorized", async () => {
    const { onUnauthorized } = connect({ probe: async () => 401 });
    latest().onerror!();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(onUnauthorized).toHaveBeenCalledOnce();
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it("頁面隱藏超過寬限期就關閉，回到前景時重新連上", async () => {
    const { doc } = connect({ hiddenGrace: 5000 });
    doc.hidden = true;
    doc.fire();
    await vi.advanceTimersByTimeAsync(5000);
    expect(latest().closed).toBe(true);
    doc.hidden = false;
    doc.fire();
    expect(FakeEventSource.instances).toHaveLength(2);
    expect(latest().closed).toBe(false);
  });

  it("停止後不再重連", async () => {
    const { stop } = connect();
    stop();
    expect(latest().closed).toBe(true);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(FakeEventSource.instances).toHaveLength(1);
  });
});

describe("backoffDelay", () => {
  it("有上限與 ±20% jitter", () => {
    expect(backoffDelay(0, 1000, 30_000, () => 0.5)).toBe(1000);
    expect(backoffDelay(10, 1000, 30_000, () => 0.5)).toBe(30_000);
    expect(backoffDelay(0, 1000, 30_000, () => 0)).toBe(800);
    expect(backoffDelay(0, 1000, 30_000, () => 1)).toBe(1200);
  });
});

describe("invalidationTable", () => {
  const ev = (type: ServerEvent["type"]): ServerEvent => ({ type, item: "yt:a", item_id: "1" });

  it("每個事件型別都有對應", () => {
    for (const t of EVENT_TYPES) expect(invalidationTable[t]).toBeTypeOf("function");
  });

  it("stage.status 讓詳情、列表與佇列失效；item.created 不讓列表失效（只顯示「有 N 個新項目」）", async () => {
    const qc = new QueryClient();
    const keys = [queryKeys.items.detail("1"), queryKeys.items.list({ q: "" }), queryKeys.queue];
    for (const k of keys) qc.setQueryData(k, 1);
    for (const k of invalidationTable["stage.status"](ev("stage.status")))
      await qc.invalidateQueries({ queryKey: k });
    expect(keys.map((k) => qc.getQueryState(k)?.isInvalidated)).toEqual([true, true, true]);

    const qc2 = new QueryClient();
    for (const k of keys) qc2.setQueryData(k, 1);
    for (const k of invalidationTable["item.created"](ev("item.created")))
      await qc2.invalidateQueries({ queryKey: k });
    expect(qc2.getQueryState(queryKeys.items.list({ q: "" }))?.isInvalidated).toBe(false);
  });
});
