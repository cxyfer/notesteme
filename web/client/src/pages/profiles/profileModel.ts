// 處理方案的表單模型（PRF-R1、internal/pipeline/schema.go）。
// config 是「存下來的這一層」：沒寫的欄位沿用全域設定。表單直接編輯 config 的副本（未知欄位原樣保留），
// 儲存時用 mergePatch(原本, 草稿) 算出 RFC 7396 merge patch（F4）：刪掉的鍵送 null，物件遞迴，陣列與純量整個取代。
import type { components } from "../../api/schema";

export type Profile = components["schemas"]["Profile"];
export type Config = Record<string, unknown>;

/**
 * 編輯器顯示的步驟（pages.md §8 的線框：分段、校正、翻譯、關鍵畫面、筆記），依執行順序；
 * 都在 schema 的 stageOrder 中（internal/pipeline/schema.go）。sponsorblock、chapters、summary
 * 不在 §8 的線框中，表單不顯示，但原樣保留（儲存只送改過的鍵）。
 */
export const LLM_STAGES = ["segment", "correct", "translate", "frames", "note"] as const;
export type LlmStage = (typeof LLM_STAGES)[number];

/**
 * StageExecutors（internal/pipeline/stages.go）：translate 另外可以用 opencc；frames 可以用 rules（只依規則選圖），
 * 不列 agent（schema 回 422「選圖還不支援交給 Agent。」，FRM F6）。batch 是獨立欄位（BATCH-R1），不是 executor。
 */
export function stageExecutors(stage: LlmStage): string[] {
  switch (stage) {
    case "translate":
      return ["api", "agent", "off", "opencc"];
    case "frames":
      return ["rules", "api", "off"];
    default:
      return ["api", "agent", "off"];
  }
}

/** 寫出來的步驟沒填 executor 時，schema 以步驟的預設檢查（defaultStageConfig：frames 是 off，其他是 api）。 */
const DEFAULT_EXECUTOR: Record<LlmStage, string> = {
  segment: "api",
  correct: "api",
  translate: "api",
  frames: "off",
  note: "api",
};

/** 內建的 provider（D10）；設定檔 llm.providers 登記的沒有列表 API，可以直接輸入。 */
export const KNOWN_PROVIDERS = ["anthropic", "openai"] as const;

/**
 * 步驟的 provider 建議清單：筆記另外有 gemini（GEM-R1：engine: gemini 需要 type 為 gemini 的 provider；
 * gemini 用在其他步驟時執行期失敗，所以只在筆記列出）。
 */
export function knownProviders(stage: LlmStage): readonly string[] {
  return stage === "note" ? [...KNOWN_PROVIDERS, "gemini"] : KNOWN_PROVIDERS;
}

/** stages.note.engine（GEM-R1） */
export const NOTE_ENGINES = ["pipeline", "gemini"] as const;

export const SPLIT_TIMING = ["forbid", "when_needed", "free"] as const;
export const ON_MANUAL = ["skip", "run"] as const;
export const VARIANT_STRATEGY = ["convert", "localize"] as const;
export const CAPTION_KINDS = ["manual", "auto"] as const;
/** output.Formats */
export const OUTPUT_FORMATS = ["srt", "vtt", "txt", "cues-json", "ass", "bilingual-srt"] as const;
/** 輸出目標（OUT-R*、VAULT-R1）：vault 只能選用設定檔定義的 id */
export const OUTPUT_TARGETS = ["sidecar", "folder", "vault"] as const;
/** outputs[].on_user_change（OUT-R12、VAULT-R6）：三種目標都有；沒寫時是 save_as_new */
export const ON_USER_CHANGE = ["save_as_new", "skip"] as const;

/** 各輸出目標可以有的欄位（internal/pipeline/schema.go 的 outputFields、vaultOutputFields）；換目標時丟掉其他的 */
const TARGET_FIELDS: Record<string, readonly string[]> = {
  sidecar: ["target", "formats", "lang", "style", "on_user_change"],
  folder: ["target", "formats", "lang", "style", "path", "on_user_change"],
  vault: ["target", "vault", "formats", "lang", "folder", "on_user_change", "hooks"],
};

/**
 * 把一個輸出改成另一個目標：只留下新目標認得的欄位（否則伺服器回 422「不認識的欄位。」）。
 * 改成 vault 時預設選第一個 vault（沒有時不填，由伺服器回「必填」）；vault 的 formats 可以是空陣列。
 */
export function retarget(
  o: Record<string, unknown>,
  target: string,
  vaults: readonly string[] = [],
): Record<string, unknown> {
  const keep = TARGET_FIELDS[target] ?? TARGET_FIELDS.sidecar!;
  const n: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(o)) if (keep.includes(k)) n[k] = v;
  n.target = target;
  if (target === "vault" && n.vault === undefined && vaults[0]) n.vault = vaults[0];
  return n;
}

export function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

export function clone<T>(v: T): T {
  return structuredClone(v);
}

function deepEqual(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

/**
 * 從 before 變成 after 的 RFC 7396 merge patch；沒有差異時回傳 undefined。
 * 兩者都不含 null（存好的各層不含 null，F4）。
 */
export function mergePatch(before: Config, after: Config): Config | undefined {
  const out: Config = {};
  for (const k of Object.keys(before)) {
    if (!(k in after)) out[k] = null;
  }
  for (const [k, v] of Object.entries(after)) {
    const prev = before[k];
    if (isObject(v) && isObject(prev)) {
      const sub = mergePatch(prev, v);
      if (sub) out[k] = sub;
    } else if (!deepEqual(prev, v)) {
      out[k] = v;
    }
  }
  return Object.keys(out).length ? out : undefined;
}

/** 讀取點號路徑的值（stages.correct.provider）。 */
export function getPath(doc: Config, path: string): unknown {
  let cur: unknown = doc;
  for (const k of path.split(".")) {
    if (!isObject(cur)) return undefined;
    cur = cur[k];
  }
  return cur;
}

/**
 * 回傳設好 path 的新文件；value 是 undefined 時刪掉該鍵，並把因此變空的物件一起刪掉
 * （空的 stages.correct 會讓 schema 把它當成「寫出來的步驟」檢查完整性，D8）。
 */
export function setPath(doc: Config, path: string, value: unknown): Config {
  const keys = path.split(".");
  const next = clone(doc);
  const parents: Record<string, unknown>[] = [next];
  let cur: Record<string, unknown> = next;
  for (const k of keys.slice(0, -1)) {
    if (!isObject(cur[k])) cur[k] = {};
    cur = cur[k] as Record<string, unknown>;
    parents.push(cur);
  }
  const last = keys[keys.length - 1]!;
  if (value === undefined) delete cur[last];
  else cur[last] = value;
  // 由內往外刪掉空物件
  for (let i = parents.length - 1; i > 0; i--) {
    if (Object.keys(parents[i]!).length === 0) delete parents[i - 1]![keys[i - 1]!];
    else break;
  }
  return next;
}

/** POST 完整文件時不能有 null（F4）。 */
export function stripNulls(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(stripNulls);
  if (isObject(v)) {
    const out: Record<string, unknown> = {};
    for (const [k, x] of Object.entries(v)) if (x !== null) out[k] = stripNulls(x);
    return out;
  }
  return v;
}

/** 步驟生效的 executor：寫出來的步驟沒填 executor 時是步驟的預設（frames 是 off，其他是 api）；整個步驟沒寫時沿用全域（undefined）。 */
export function stageExecutor(doc: Config, stage: LlmStage): string | undefined {
  const s = getPath(doc, `stages.${stage}`);
  if (!isObject(s)) return undefined;
  return typeof s.executor === "string" ? s.executor : DEFAULT_EXECUTOR[stage];
}

/** prompt 引用 <id>、<id>@default、<id>@N（D11）→ id 與版本（undefined＝跟隨預設）。 */
export function parsePromptRef(ref: string): { id: string; version?: number } {
  const [id, ver] = ref.split("@") as [string, string | undefined];
  if (!ver || ver === "default") return { id };
  const n = Number(ver);
  return Number.isInteger(n) && n >= 1 ? { id, version: n } : { id };
}

/** 名稱規則（CreateProfileInputBody）。 */
export function validProfileName(name: string) {
  return /^[a-z0-9][a-z0-9_-]{0,63}$/.test(name);
}

/** 字串陣列 ↔ 以逗號或空白分隔的文字（目標語言、輸出語言）。 */
export function splitList(s: string): string[] {
  return s
    .split(/[\s,，、]+/)
    .map((x) => x.trim())
    .filter(Boolean);
}

/**
 * prompt id → 引用它的處理方案名稱（依字母排序）。只看處理方案目前的版本與 stages.*.prompt；
 * 全域設定、頻道與影音層的引用看不到（刪除時後端的 409 used_by 會列出全部）。
 */
export function promptUsers(profiles: readonly Profile[]): Map<string, string[]> {
  const out = new Map<string, string[]>();
  for (const p of profiles) {
    const stages = isObject(p.config.stages) ? p.config.stages : {};
    const ids = new Set<string>();
    for (const s of Object.values(stages)) {
      if (isObject(s) && typeof s.prompt === "string") ids.add(parsePromptRef(s.prompt).id);
    }
    for (const id of ids) out.set(id, [...(out.get(id) ?? []), p.name]);
  }
  for (const names of out.values()) names.sort();
  return out;
}
