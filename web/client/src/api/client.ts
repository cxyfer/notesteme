// API client：所有對後端的呼叫都經過這裡（慣例見 web/client/README.md「API 呼叫」）。
//
//   - 同源，自動帶 cookie（credentials: "same-origin"）。
//   - 非 GET／HEAD 的請求加上 X-CSRF-Token（從 CSRF cookie 讀取，AUTH-R5 的 double-submit）。
//   - Accept-Language 跟隨 UI 語言：後端的錯誤訊息依它選語言（NFR-I18N-2）。
//   - 非 2xx 一律丟出 ApiError（帶 code 與 message）；401 unauthorized 另外通知 onUnauthorized（導到登入頁）。
//
// 型別來自 src/api/schema.d.ts（openapi-typescript 由 internal/api/openapi.json 產生，不要手改）。
import { CSRF_HEADER, csrfToken } from "../auth/session";
import { currentLanguage } from "../i18n";
import { ApiError, CLIENT_CODES, errorFromResponse } from "./errors";
import type { paths } from "./schema";

type Method = "get" | "put" | "post" | "delete" | "patch";

/** paths[P][M] 的 200／201 JSON 回應型別；沒有本體（204）時是 undefined。 */
export type ResponseOf<P extends keyof paths, M extends Method> = paths[P][M] extends {
  responses: infer R;
}
  ? R extends { 200: { content: { "application/json": infer B } } }
    ? B
    : R extends { 201: { content: { "application/json": infer B } } }
      ? B
      : undefined
  : never;

/** paths[P][M] 的 JSON 請求本體型別。 */
export type BodyOf<P extends keyof paths, M extends Method> = paths[P][M] extends {
  requestBody?: { content: { "application/json": infer B } };
}
  ? B
  : never;

type Unauthorized = (err: ApiError) => void;
let onUnauthorized: Unauthorized | undefined;

/** 由 router 註冊：收到 401 unauthorized 時導到登入頁。 */
export function setUnauthorizedHandler(fn: Unauthorized | undefined) {
  onUnauthorized = fn;
}

export interface RequestOptions {
  body?: unknown;
  /** 陣列會展開成重複的參數（例如 status=a&status=b） */
  query?: Record<string, string | number | boolean | readonly string[] | undefined>;
  signal?: AbortSignal;
  headers?: Record<string, string>;
}

/** 把 `/api/items/{id}` 的路徑參數代入（逐段 encodeURIComponent）。 */
export function apiPath(template: string, params: Record<string, string | number> = {}): string {
  return template.replace(/\{(\w+)\}/g, (_, k: string) => {
    const v = params[k];
    if (v === undefined) throw new Error(`apiPath: 缺少參數 ${k}`);
    return encodeURIComponent(String(v));
  });
}

export async function request<T>(
  method: string,
  path: string,
  opts: RequestOptions = {},
): Promise<T> {
  const headers: Record<string, string> = {
    Accept: "application/json",
    "Accept-Language": currentLanguage(),
    ...opts.headers,
  };
  const m = method.toUpperCase();
  if (m !== "GET" && m !== "HEAD") {
    const token = csrfToken();
    if (token) headers[CSRF_HEADER] = token;
  }
  let body: BodyInit | undefined;
  if (opts.body instanceof FormData) {
    // multipart：Content-Type（含 boundary）交給瀏覽器產生
    body = opts.body;
  } else if (opts.body !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(opts.body);
  }
  let url = path;
  if (opts.query) {
    const q = new URLSearchParams();
    for (const [k, v] of Object.entries(opts.query)) {
      if (v === undefined) continue;
      if (Array.isArray(v)) for (const x of v as readonly string[]) q.append(k, x);
      else q.set(k, String(v));
    }
    const s = q.toString();
    if (s) url += `?${s}`;
  }

  let res: Response;
  try {
    res = await fetch(url, {
      method: m,
      headers,
      body,
      credentials: "same-origin",
      signal: opts.signal,
    });
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") throw e;
    throw new ApiError(0, CLIENT_CODES.network, e instanceof Error ? e.message : String(e));
  }
  if (!res.ok) {
    const err = await errorFromResponse(res);
    if (res.status === 401 && err.code === "unauthorized") onUnauthorized?.(err);
    throw err;
  }
  if (res.status === 204 || res.headers.get("Content-Length") === "0") return undefined as T;
  const text = await res.text();
  if (text === "") return undefined as T;
  try {
    return JSON.parse(text) as T;
  } catch {
    throw new ApiError(res.status, CLIENT_CODES.badResponse, res.statusText);
  }
}

/** 有型別的捷徑：api.get("/api/queue")、api.post("/api/auth/login", { body })。 */
export const api = {
  get<P extends keyof paths>(path: P | string, opts?: RequestOptions) {
    return request<ResponseOf<P, "get">>("GET", path, opts);
  },
  post<P extends keyof paths>(
    path: P | string,
    opts?: RequestOptions & { body?: BodyOf<P, "post"> | FormData },
  ) {
    return request<ResponseOf<P, "post">>("POST", path, opts);
  },
  put<P extends keyof paths>(
    path: P | string,
    opts?: RequestOptions & { body?: BodyOf<P, "put"> },
  ) {
    return request<ResponseOf<P, "put">>("PUT", path, opts);
  },
  delete<P extends keyof paths>(path: P | string, opts?: RequestOptions) {
    return request<ResponseOf<P, "delete">>("DELETE", path, opts);
  },
  patch<P extends keyof paths>(
    path: P | string,
    opts?: RequestOptions & { body?: BodyOf<P, "patch"> },
  ) {
    return request<ResponseOf<P, "patch">>("PATCH", path, opts);
  },
};
