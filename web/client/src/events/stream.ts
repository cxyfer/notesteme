// SSE 連線管理（不依賴 React，方便測試）。
//
//   - 後端送具名事件，要對每個型別 addEventListener（onmessage 收不到）。
//   - 原生 EventSource 會無限重試且看不到狀態碼：出錯時自己 close，用指數退避重連（上限 maxDelay）；
//     重連前先探測一次是不是 401（登入失效就停止並通知 onUnauthorized）。
//   - 頁面隱藏超過 hiddenGrace 就關閉連線；回到前景時重新連上（並觸發 onOpen，讓快取失效）。
import { EVENT_TYPES, parseEvent, type ServerEvent } from "./types";

export interface StreamOptions {
  url?: string;
  onEvent: (ev: ServerEvent) => void;
  /** 每次（重新）連上時呼叫 */
  onOpen?: () => void;
  onUnauthorized?: () => void;
  baseDelay?: number;
  maxDelay?: number;
  hiddenGrace?: number;
  EventSourceImpl?: typeof EventSource;
  probe?: (url: string) => Promise<number>;
  random?: () => number;
  doc?: Pick<Document, "hidden" | "addEventListener" | "removeEventListener">;
}

/** 第 attempt 次（從 0 起算）重連的等待時間：指數成長、上限 maxDelay、±20% jitter。 */
export function backoffDelay(
  attempt: number,
  base: number,
  max: number,
  random: () => number = Math.random,
) {
  const raw = Math.min(max, base * 2 ** attempt);
  return Math.round(raw * (0.8 + 0.4 * random()));
}

/** 探測 SSE 端點的狀態碼：只讀標頭，立刻中止。 */
async function defaultProbe(url: string): Promise<number> {
  const ctrl = new AbortController();
  try {
    const res = await fetch(url, { credentials: "same-origin", signal: ctrl.signal });
    return res.status;
  } catch {
    return 0;
  } finally {
    ctrl.abort();
  }
}

export function connectEventStream(opts: StreamOptions): () => void {
  const url = opts.url ?? "/api/events";
  const base = opts.baseDelay ?? 1000;
  const max = opts.maxDelay ?? 30_000;
  const grace = opts.hiddenGrace ?? 60_000;
  const ES = opts.EventSourceImpl ?? EventSource;
  const probe = opts.probe ?? defaultProbe;
  const doc = opts.doc ?? document;

  let es: EventSource | undefined;
  let attempt = 0;
  let retryTimer: ReturnType<typeof setTimeout> | undefined;
  let hideTimer: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;

  const handle = (e: MessageEvent<string>) => {
    const ev = parseEvent(e.data);
    if (ev) opts.onEvent(ev);
  };

  function open() {
    if (stopped || es) return;
    es = new ES(url, { withCredentials: true });
    for (const t of EVENT_TYPES) es.addEventListener(t, handle as EventListener);
    es.onopen = () => {
      attempt = 0;
      opts.onOpen?.();
    };
    es.onerror = () => {
      close();
      scheduleRetry();
    };
  }

  function close() {
    if (!es) return;
    for (const t of EVENT_TYPES) es.removeEventListener(t, handle as EventListener);
    es.onopen = null;
    es.onerror = null;
    es.close();
    es = undefined;
  }

  function scheduleRetry() {
    if (stopped || retryTimer) return;
    const delay = backoffDelay(attempt++, base, max, opts.random);
    retryTimer = setTimeout(async () => {
      retryTimer = undefined;
      if (stopped || doc.hidden) return;
      const status = await probe(url);
      if (stopped) return;
      if (status === 401) {
        stopped = true;
        opts.onUnauthorized?.();
        return;
      }
      open();
    }, delay);
  }

  function onVisibility() {
    if (doc.hidden) {
      hideTimer ??= setTimeout(() => {
        hideTimer = undefined;
        close();
      }, grace);
    } else {
      if (hideTimer) clearTimeout(hideTimer);
      hideTimer = undefined;
      if (!es && !retryTimer) open();
    }
  }

  doc.addEventListener("visibilitychange", onVisibility);
  open();

  return () => {
    stopped = true;
    if (retryTimer) clearTimeout(retryTimer);
    if (hideTimer) clearTimeout(hideTimer);
    doc.removeEventListener("visibilitychange", onVisibility);
    close();
  };
}
