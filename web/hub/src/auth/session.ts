// Hub 的 session 與 CSRF（HACC-R8）。session cookie 是 HttpOnly；CSRF cookie __Host-notesteme_hub_csrf 不是，
// 前端讀它放進 X-CSRF-Token。Hub 有 GET /api/v1/me，所以「是否登入」以它為準，cookie 只是送出請求時要用。
export const CSRF_COOKIE = "__Host-notesteme_hub_csrf";
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

/**
 * 登入後要回到的站內路徑：只接受站內的絕對路徑，避免 open redirect。
 * Hub 的 OAuth callback 一律轉到 /（HACC A11），所以 redirect 存在 sessionStorage，登入回來後由首頁取用。
 */
export function safeRedirect(target: unknown, fallback = "/"): string {
  if (typeof target !== "string" || target === "") return fallback;
  if (!target.startsWith("/") || target.startsWith("//") || target.startsWith("/\\"))
    return fallback;
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f\\]/.test(target)) return fallback;
  if (target === "/login" || target.startsWith("/login?") || target.startsWith("/auth/"))
    return fallback;
  return target;
}

const RETURN_KEY = "notesteme-hub.returnTo";

export function rememberReturnTo(path: string) {
  try {
    sessionStorage.setItem(RETURN_KEY, safeRedirect(path));
  } catch {
    // 隱私模式：登入後回到首頁
  }
}

/** 取出並清除登入前的路徑；沒有時回 undefined。 */
export function takeReturnTo(): string | undefined {
  try {
    const v = sessionStorage.getItem(RETURN_KEY);
    sessionStorage.removeItem(RETURN_KEY);
    return v ? safeRedirect(v) : undefined;
  } catch {
    return undefined;
  }
}
