// 測試用的輕量 API 替身：依「METHOD /path/{param}」比對 fetch，不連網路。
//   const api = mockApi({ "GET /api/items": () => ({ items: [], total: 0 }) });
//   api.calls("GET", "/api/items") → 每次呼叫的 URL（含 query）
// handler 回傳 Response 就原樣使用，回傳其他值就當 200 JSON；沒有對應的路由回 404 not_found。
import { vi } from "vitest";

export interface MockRequest {
  method: string;
  url: URL;
  params: Record<string, string>;
  body: unknown;
  init: RequestInit;
}

export type MockHandler = (req: MockRequest) => unknown;

export function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

export function apiError(status: number, code: string, message = code, extra = {}): Response {
  return json(status, { code, message, ...extra });
}

interface Route {
  method: string;
  re: RegExp;
  names: string[];
  handler: MockHandler;
}

export function mockApi(routes: Record<string, MockHandler>) {
  const table: Route[] = Object.entries(routes).map(([key, handler]) => {
    const [method, path] = key.split(" ") as [string, string];
    const names: string[] = [];
    const re = new RegExp(
      "^" +
        path.replace(/\{(\w+)\}/g, (_, n: string) => {
          names.push(n);
          return "([^/]+)";
        }) +
        "$",
    );
    return { method, re, names, handler };
  });
  const log: { method: string; url: URL; init: RequestInit }[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = new URL(String(input), "http://localhost");
    const method = (init.method ?? "GET").toUpperCase();
    log.push({ method, url, init });
    for (const r of table) {
      if (r.method !== method) continue;
      const m = r.re.exec(url.pathname);
      if (!m) continue;
      const params = Object.fromEntries(r.names.map((n, i) => [n, decodeURIComponent(m[i + 1]!)]));
      let body: unknown = init.body;
      if (typeof body === "string") body = JSON.parse(body);
      const out = await r.handler({ method, url, params, body, init });
      return out instanceof Response ? out : json(200, out ?? {});
    }
    return apiError(404, "not_found", `沒有替身：${method} ${url.pathname}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return {
    fetchMock,
    /** 某個路由（pathname 完全相同，或含 {param} 的樣式）的呼叫紀錄 */
    calls(method: string, path: string) {
      const re = new RegExp("^" + path.replace(/\{\w+\}/g, "[^/]+") + "$");
      return log.filter((c) => c.method === method && re.test(c.url.pathname));
    },
  };
}
