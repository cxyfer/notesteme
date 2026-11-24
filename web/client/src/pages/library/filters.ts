// 媒體庫的篩選條件 ↔ URL search params（LIB-R1、R2；docs/ui/README.md §3.3）。
//
// URL 參數與 GET /api/items 的名稱對應，除了：
//   - published_from／published_to 存日期（YYYY-MM-DD，使用者的時區），送 API 時才換成 RFC 3339 的當天起訖。
//   - dur_min／dur_max 存「分鐘」（UI 的單位），送 API 時換成 duration_min／duration_max 的秒。
// 不合法的值一律丟掉（不報錯）：網址是使用者可以手改、也可能是舊書籤。

/** 平台（internal/source 的 Platform*） */
export const PLATFORMS = ["youtube", "bilibili", "podcast", "local"] as const;
export type Platform = (typeof PLATFORMS)[number];

/** pipeline 的步驟，依執行順序（internal/pipeline/stages.go） */
export const STAGES = [
  "metadata",
  "transcript",
  "normalize",
  "summary",
  "segment",
  "correct",
  "translate",
  "sponsorblock",
  "chapters",
  "frames",
  "note",
  "publish",
] as const;
export type Stage = (typeof STAGES)[number];

/** 步驟狀態（internal/store/models.go 的 Status*） */
export const STAGE_STATUSES = [
  "pending",
  "running",
  "waiting",
  "awaiting_agent",
  "blocked",
  "done",
  "skipped",
  "failed",
] as const;
export type StageStatus = (typeof STAGE_STATUSES)[number];

/** 錯誤類別（internal/errclass） */
export const ERROR_CLASSES = [
  "retryable",
  "wait",
  "needs_action",
  "budget_blocked",
  "permanent",
] as const;
export type ErrorClass = (typeof ERROR_CLASSES)[number];

/** cue 的時間來源（GET /api/items 的 timing） */
export const TIMINGS = ["source", "merged", "silence", "interpolated", "model"] as const;
export type Timing = (typeof TIMINGS)[number];

export interface LibrarySearch {
  q?: string;
  platform?: Platform;
  channel?: string;
  /** `<stage>:<status>`；同一個步驟的多個值是 OR，不同步驟之間是 AND（與 API 相同） */
  status?: string[];
  error_class?: ErrorClass;
  lang?: string;
  /** YYYY-MM-DD */
  published_from?: string;
  /** YYYY-MM-DD */
  published_to?: string;
  /** 分鐘 */
  dur_min?: number;
  /** 分鐘 */
  dur_max?: number;
  timing?: Timing;
  /**
   * 停住的原因（GET /api/items 的 blocked，單一值）。等你決定是 awaiting_user（DIG-R6、R10 的彙整連結
   * /library?blocked=awaiting_user）；其他原因（budget_blocked、needs_action…）只驗格式（UI-8）。
   */
  blocked?: string;
  /** 已讀狀態（LIB-R19，UI-7） */
  read_state?: ReadState;
}

export const READ_STATES = ["unread", "read"] as const;
export type ReadState = (typeof READ_STATES)[number];

export const FILTER_KEYS = [
  "q",
  "platform",
  "channel",
  "status",
  "error_class",
  "lang",
  "published_from",
  "published_to",
  "dur_min",
  "dur_max",
  "timing",
  "blocked",
  "read_state",
] as const satisfies readonly (keyof LibrarySearch)[];

/** API 的上限：標題搜尋最多 200 字元 */
const MAX_Q = 200;
const MAX_TEXT = 200;

// TanStack Router 預設用 JSON 解析 search，`q=123` 會變成數字、`true` 變成 boolean：一律先轉回字串。
function str(v: unknown): string | undefined {
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  return undefined;
}

function oneOf<T extends string>(list: readonly T[], v: unknown): T | undefined {
  const s = str(v);
  return s !== undefined && (list as readonly string[]).includes(s) ? (s as T) : undefined;
}

function text(v: unknown, max = MAX_TEXT): string | undefined {
  const s = str(v)?.trim();
  return s ? s.slice(0, max) : undefined;
}

const DATE_RE = /^(\d{4})-(\d{2})-(\d{2})$/;

/** YYYY-MM-DD 而且是存在的日期 */
export function isValidDate(s: string): boolean {
  const m = DATE_RE.exec(s);
  if (!m) return false;
  const [y, mo, d] = [Number(m[1]), Number(m[2]), Number(m[3])];
  const dt = new Date(y, mo - 1, d);
  return dt.getFullYear() === y && dt.getMonth() === mo - 1 && dt.getDate() === d;
}

function date(v: unknown): string | undefined {
  const s = str(v)?.trim();
  return s && isValidDate(s) ? s : undefined;
}

/** 等你決定（DIG-R6） */
export const AWAITING_USER = "awaiting_user";

/** 停住的原因：與 API 相同的格式 ^[a-z_]{1,64}$ */
function blockReason(v: unknown): string | undefined {
  const s = str(v);
  return s && /^[a-z_]{1,64}$/.test(s) ? s : undefined;
}

function minutes(v: unknown): number | undefined {
  const s = str(v)?.trim();
  if (!s || !/^\d{1,5}$/.test(s)) return undefined;
  return Number(s);
}

/** `<stage>:<status>`，兩邊都要是已知的值；去重複並依步驟、狀態的順序排列（同一組條件只有一種網址） */
export function normalizeStatus(v: unknown): string[] | undefined {
  const raw = Array.isArray(v) ? v : v === undefined ? [] : [v];
  const set = new Set<string>();
  for (const x of raw) {
    const s = str(x);
    if (!s) continue;
    const [stage, status, ...rest] = s.split(":");
    if (rest.length) continue;
    if (!oneOf(STAGES, stage) || !oneOf(STAGE_STATUSES, status)) continue;
    set.add(`${stage}:${status}`);
  }
  if (!set.size) return undefined;
  const rank = (s: string) => {
    const [stage, status] = s.split(":") as [Stage, StageStatus];
    return STAGES.indexOf(stage) * 100 + STAGE_STATUSES.indexOf(status);
  };
  return [...set].sort((a, b) => rank(a) - rank(b));
}

/** route 的 validateSearch：只保留認得而且合法的參數，其他丟掉。 */
export function validateLibrarySearch(raw: Record<string, unknown>): LibrarySearch {
  const out: LibrarySearch = {
    q: text(raw.q, MAX_Q),
    platform: oneOf(PLATFORMS, raw.platform),
    channel: text(raw.channel),
    status: normalizeStatus(raw.status),
    error_class: oneOf(ERROR_CLASSES, raw.error_class),
    lang: text(raw.lang, 35),
    published_from: date(raw.published_from),
    published_to: date(raw.published_to),
    dur_min: minutes(raw.dur_min),
    dur_max: minutes(raw.dur_max),
    timing: oneOf(TIMINGS, raw.timing),
    blocked: blockReason(raw.blocked),
    read_state: oneOf(READ_STATES, raw.read_state),
  };
  // 拿掉 undefined，讓網址只帶有值的參數
  for (const k of FILTER_KEYS) if (out[k] === undefined) delete out[k];
  return out;
}

/** 有沒有任何篩選條件（決定空狀態的文案：「還沒有任何影音」或「沒有符合條件的影音」） */
export function hasFilters(s: LibrarySearch): boolean {
  return FILTER_KEYS.some((k) => s[k] !== undefined);
}

/** 當地時區那一天的開始或結束，RFC 3339（UTC）。 */
function dayBound(d: string, end: boolean): string {
  const [y, m, day] = d.split("-").map(Number) as [number, number, number];
  const dt = end ? new Date(y, m - 1, day, 23, 59, 59, 999) : new Date(y, m - 1, day);
  return dt.toISOString();
}

export type ItemsQuery = Record<string, string | readonly string[] | undefined>;

/** LibrarySearch → GET /api/items 的查詢參數（不含 cursor、limit）。 */
export function toApiQuery(s: LibrarySearch): ItemsQuery {
  return {
    q: s.q,
    platform: s.platform,
    channel: s.channel,
    status: s.status,
    blocked: s.blocked,
    error_class: s.error_class,
    lang: s.lang,
    published_from: s.published_from ? dayBound(s.published_from, false) : undefined,
    published_to: s.published_to ? dayBound(s.published_to, true) : undefined,
    duration_min: s.dur_min !== undefined ? String(s.dur_min * 60) : undefined,
    // 上限（含）就是那一分鐘整：「10 分鐘以內」是 600 秒以下
    duration_max: s.dur_max !== undefined ? String(s.dur_max * 60) : undefined,
    timing: s.timing,
    read_state: s.read_state,
  };
}

/** 切換一個 `<stage>:<status>` 條件 */
export function toggleStatus(current: string[] | undefined, value: string): string[] | undefined {
  const set = new Set(current ?? []);
  if (set.has(value)) set.delete(value);
  else set.add(value);
  return normalizeStatus([...set]);
}
