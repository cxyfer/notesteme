// session 與 CSRF（docs/specs/AUTH-access.md、internal/api/api.go 的 login）。
//
// 後端沒有「目前 session」的路由：CSRF cookie（非 HttpOnly，與 session 同時設定、同時清除）
// 只當作「可能已登入」的提示。沒有它就直接導到登入頁；有它就照常渲染，任何 401 再導到登入頁。
export const CSRF_COOKIE = "notesteme_csrf";
export const CSRF_HEADER = "X-CSRF-Token";

export function readCookie(name: string, cookie: string = document.cookie): string | undefined {
  for (const part of cookie.split(";")) {
    const [k, ...v] = part.trim().split("=");
    if (k === name) return decodeURIComponent(v.join("="));
  }
  return undefined;
}

export function csrfToken(): string | undefined {
  return readCookie(CSRF_COOKIE) || undefined;
}

export function hasSessionHint(): boolean {
  return csrfToken() !== undefined;
}

/**
 * 登入後要導回的路徑：只接受站內的絕對路徑，避免 open redirect。
 * `//evil.example`、`/\evil.example`、含控制字元或 scheme 的值一律改成預設值。
 */
export function safeRedirect(target: unknown, fallback = "/library"): string {
  if (typeof target !== "string" || target === "") return fallback;
  if (!target.startsWith("/") || target.startsWith("//") || target.startsWith("/\\"))
    return fallback;
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f\\]/.test(target)) return fallback;
  if (target === "/login" || target.startsWith("/login?") || target.startsWith("/login/"))
    return fallback;
  return target;
}
