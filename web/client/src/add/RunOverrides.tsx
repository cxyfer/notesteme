// 「新增」抽屜的「調整這次的設定」（pages.md §2、ING-R7、ING-07）：預設收合，只影響這一次新增的影音，
// 不修改處理方案。內容送到 POST /api/items 與 POST /api/estimate 的 overrides（單次執行層，PRF-R2）。
//
//   - 目標語言：language.targets（list 整個取代）。留空就沿用。
//   - 字幕來源：language.fallback 的順序與要不要用（平台字幕之後才輪到它們，caption_priority 不在這裡調整）。
//     起始的順序是「處理方案這一層 → 全域設定（GET /api/settings/language，已合併內建預設）」；
//     讀不到全域設定、處理方案也沒有寫時不提供調整：list 是整個取代，用猜的起始值會把 owner 移除的來源加回來。
//   - 步驟：校正、翻譯、關鍵畫面、筆記。只能把會做的步驟關掉（stages.<步驟>.executor: off）；
//     處理方案沒有開啟的步驟不能在這裡打開（打開需要 provider 與 model），顯示為停用。
//     步驟是否開啟依「處理方案這一層 → 全域設定（GET /api/settings/stages）」判斷，頻道覆寫要到 metadata 之後才知道。
import { useQuery } from "@tanstack/react-query";
import { useId } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../api/client";
import { queryKeys } from "../api/queryKeys";
import { Button } from "../components";
import { sourceLabel } from "../pages/item/sourceLabel";

/** 可以在這裡關掉的步驟（pages.md §2 的線框） */
export const RUN_STAGES = ["correct", "translate", "frames", "note"] as const;
type RunStage = (typeof RUN_STAGES)[number];

export interface SourceChoice {
  id: string;
  on: boolean;
}

export interface RunOverridesValue {
  /** 目標語言的原始輸入（逗號或空白分隔）；空字串＝沿用 */
  targets: string;
  /** 字幕來源的順序與開關；undefined＝沒有動過（沿用） */
  sources?: SourceChoice[];
  /** 這次不做的步驟 */
  skip: RunStage[];
}

export const EMPTY_RUN_OVERRIDES: RunOverridesValue = { targets: "", skip: [] };

type Obj = Record<string, unknown>;
const isObj = (v: unknown): v is Obj => v !== null && typeof v === "object" && !Array.isArray(v);
const strList = (v: unknown): string[] | undefined =>
  Array.isArray(v) && v.every((x) => typeof x === "string") ? (v as string[]) : undefined;
const get = (o: unknown, ...path: string[]): unknown =>
  path.reduce<unknown>((cur, k) => (isObj(cur) ? cur[k] : undefined), o);

export function parseTargets(text: string): string[] {
  return [...new Set(text.split(/[,，、\s]+/).filter(Boolean))];
}

/** 沒有調整任何東西時回傳 undefined（請求裡就不帶 overrides） */
export function buildOverrides(
  v: RunOverridesValue,
  baseSources: readonly string[] | undefined,
): Obj | undefined {
  const out: Obj = {};
  const language: Obj = {};
  const targets = parseTargets(v.targets);
  if (targets.length > 0) language.targets = targets;
  if (v.sources) {
    const on = v.sources.filter((s) => s.on).map((s) => s.id);
    const same =
      baseSources && on.length === baseSources.length && on.every((id, i) => id === baseSources[i]);
    if (!same) language.fallback = on;
  }
  if (Object.keys(language).length > 0) out.language = language;
  if (v.skip.length > 0)
    out.stages = Object.fromEntries(v.skip.map((s) => [s, { executor: "off" }]));
  return Object.keys(out).length > 0 ? out : undefined;
}

function useGlobalSection(section: "language" | "stages", enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.settings(section),
    queryFn: ({ signal }) =>
      api.get<"/api/settings/{section}">(apiPath("/api/settings/{section}", { section }), {
        signal,
      }) as Promise<Obj>,
    enabled,
    retry: false,
    staleTime: 60_000,
  });
}

/** 處理方案這一層 → 全域設定；都沒有時 undefined（不知道） */
export function useRunDefaults(profileConfig: Obj | undefined, enabled: boolean) {
  const language = useGlobalSection("language", enabled);
  const stages = useGlobalSection("stages", enabled);
  const sources =
    strList(get(profileConfig, "language", "fallback")) ?? strList(get(language.data, "fallback"));
  const targets =
    strList(get(profileConfig, "language", "targets")) ?? strList(get(language.data, "targets"));
  const executor = (s: RunStage): string | undefined => {
    const v = get(profileConfig, "stages", s, "executor") ?? get(stages.data, s, "executor");
    return typeof v === "string" ? v : undefined;
  };
  return {
    sources,
    targets,
    executor,
    loading: language.isPending && enabled,
    stagesKnown: stages.isSuccess,
  };
}

export function RunOverrides({
  open,
  onToggle,
  value,
  onChange,
  defaults,
  errors,
}: {
  open: boolean;
  onToggle: (open: boolean) => void;
  value: RunOverridesValue;
  onChange: (v: RunOverridesValue) => void;
  defaults: ReturnType<typeof useRunDefaults>;
  /** 422 的逐欄位錯誤（path → 訊息），路徑可能帶 overrides. 前綴 */
  errors: Record<string, string>;
}) {
  const { t } = useTranslation();
  const id = useId();
  const adjusted = buildOverrides(value, defaults.sources) !== undefined;
  const errorAt = (prefix: string) =>
    Object.entries(errors).find(([p]) => p.replace(/^overrides\./, "").startsWith(prefix))?.[1];
  const sources: SourceChoice[] =
    value.sources ?? (defaults.sources ?? []).map((s) => ({ id: s, on: true }));

  function move(i: number, by: -1 | 1) {
    const next = [...sources];
    const [x] = next.splice(i, 1);
    next.splice(i + by, 0, x!);
    onChange({ ...value, sources: next });
  }

  const targetsError = errorAt("language.targets");
  const sourcesError = errorAt("language.fallback");
  const stagesError = errorAt("stages");

  return (
    <section className="add__run" aria-labelledby={`${id}-head`}>
      <h3 id={`${id}-head`} className="add__run-head">
        <Button
          variant="text"
          aria-expanded={open}
          aria-controls={`${id}-body`}
          onClick={() => onToggle(!open)}
        >
          {adjusted ? t("add.run.toggleAdjusted") : t("add.run.toggle")}
        </Button>
      </h3>
      <div id={`${id}-body`} className="add__run-body" hidden={!open}>
        <p className="field__hint">{t("add.run.hint")}</p>

        <div className="field">
          <label className="field__label" htmlFor={`${id}-targets`}>
            {t("add.run.targets")}
          </label>
          <input
            id={`${id}-targets`}
            className="field__input"
            autoComplete="off"
            spellCheck={false}
            value={value.targets}
            placeholder={defaults.targets?.join(", ")}
            aria-invalid={targetsError ? true : undefined}
            aria-describedby={`${id}-targets-hint`}
            onChange={(e) => onChange({ ...value, targets: e.target.value })}
          />
          <span id={`${id}-targets-hint`} className="field__hint">
            {defaults.targets
              ? t("add.run.targetsHint", { current: defaults.targets.join(", ") })
              : t("add.run.targetsHintUnknown")}
          </span>
          {targetsError && <span className="field__error">{targetsError}</span>}
        </div>

        <fieldset className="add__run-sources">
          <legend>{t("add.run.sources")}</legend>
          {defaults.loading ? null : defaults.sources === undefined ? (
            <p className="field__hint">{t("add.run.sourcesUnknown")}</p>
          ) : (
            <>
              <p className="field__hint">{t("add.run.sourcesHint")}</p>
              {sources.length === 0 ? (
                <p className="secondary">{t("add.run.sourcesNone")}</p>
              ) : (
                <ol className="add__run-source-list">
                  {sources.map((s, i) => {
                    const name = sourceLabel(t, s.id);
                    return (
                      <li key={s.id}>
                        <label className="check">
                          <input
                            type="checkbox"
                            checked={s.on}
                            onChange={() =>
                              onChange({
                                ...value,
                                sources: sources.map((x) =>
                                  x.id === s.id ? { ...x, on: !x.on } : x,
                                ),
                              })
                            }
                          />
                          <span>{name}</span>
                        </label>
                        <Button
                          variant="text"
                          disabled={i === 0}
                          aria-label={t("add.run.moveUp", { name })}
                          onClick={() => move(i, -1)}
                        >
                          {t("add.run.up")}
                        </Button>
                        <Button
                          variant="text"
                          disabled={i === sources.length - 1}
                          aria-label={t("add.run.moveDown", { name })}
                          onClick={() => move(i, 1)}
                        >
                          {t("add.run.down")}
                        </Button>
                      </li>
                    );
                  })}
                </ol>
              )}
            </>
          )}
          {sourcesError && <p className="field__error">{sourcesError}</p>}
        </fieldset>

        <fieldset className="add__run-stages">
          <legend>{t("add.run.stages")}</legend>
          <p className="field__hint">
            {defaults.stagesKnown ? t("add.run.stagesHint") : t("add.run.stagesHintUnknown")}
          </p>
          {RUN_STAGES.map((s) => {
            const off = defaults.executor(s) === "off";
            return (
              <label className="check" key={s}>
                <input
                  type="checkbox"
                  checked={!off && !value.skip.includes(s)}
                  disabled={off}
                  aria-describedby={off ? `${id}-off-${s}` : undefined}
                  onChange={(e) =>
                    onChange({
                      ...value,
                      skip: e.target.checked
                        ? value.skip.filter((x) => x !== s)
                        : [...value.skip, s],
                    })
                  }
                />
                <span>{t(`stage.${s}`)}</span>
                {off && (
                  <span id={`${id}-off-${s}`} className="secondary">
                    {t("add.run.stageOff")}
                  </span>
                )}
              </label>
            );
          })}
          {stagesError && <p className="field__error">{stagesError}</p>}
        </fieldset>
      </div>
    </section>
  );
}
