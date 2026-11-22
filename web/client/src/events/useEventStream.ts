// React 端的 SSE：AppShell 呼叫一次 useEventStream()；頁面用 useServerEvent() 訂閱個別事件
// （例如 UI-1 的「有 N 個新項目」只計數、不直接讓列表失效，docs/ui/README.md §3.4）。
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";

import { invalidationTable, reconnectInvalidations } from "./invalidation";
import { connectEventStream, type StreamOptions } from "./stream";
import type { EventType, ServerEvent } from "./types";

type Listener = (ev: ServerEvent) => void;
const listeners = new Set<Listener>();
const reconnectListeners = new Set<() => void>();

/** 把事件送給所有 useServerEvent 的訂閱者（測試也用它模擬事件）。 */
export function dispatchServerEvent(ev: ServerEvent) {
  for (const l of listeners) l(ev);
}

/** 通知 useStreamReconnect 的訂閱者「重新連上了」（測試也用它模擬）。 */
export function dispatchStreamReconnect() {
  for (const l of reconnectListeners) l();
}

export function useEventStream(
  opts: Partial<StreamOptions> & { onUnauthorized?: () => void } = {},
) {
  const qc = useQueryClient();
  const optsRef = useRef(opts);
  useEffect(() => {
    optsRef.current = opts;
  });
  useEffect(() => {
    let opened = false;
    return connectEventStream({
      ...optsRef.current,
      onOpen: () => {
        for (const key of reconnectInvalidations) void qc.invalidateQueries({ queryKey: key });
        // 第一次連上不算：之前沒有漏掉事件
        if (opened) dispatchStreamReconnect();
        opened = true;
      },
      onEvent: (ev) => {
        for (const key of invalidationTable[ev.type](ev))
          void qc.invalidateQueries({ queryKey: key });
        dispatchServerEvent(ev);
      },
      onUnauthorized: () => optsRef.current.onUnauthorized?.(),
    });
  }, [qc]);
}

/** 訂閱伺服器事件；types 省略時收全部。 */
export function useServerEvent(handler: Listener, types?: readonly EventType[]) {
  const ref = useRef(handler);
  useEffect(() => {
    ref.current = handler;
  });
  const key = types?.join(",");
  useEffect(() => {
    const filter = key ? new Set(key.split(",")) : undefined;
    const l: Listener = (ev) => {
      if (!filter || filter.has(ev.type)) ref.current(ev);
    };
    listeners.add(l);
    return () => {
      listeners.delete(l);
    };
  }, [key]);
}

/** 斷線後重新連上時呼叫（第一次連上不呼叫）：斷線期間的事件收不到，頁面可以提示重新載入。 */
export function useStreamReconnect(handler: () => void) {
  const ref = useRef(handler);
  useEffect(() => {
    ref.current = handler;
  });
  useEffect(() => {
    const l = () => ref.current();
    reconnectListeners.add(l);
    return () => {
      reconnectListeners.delete(l);
    };
  }, []);
}
