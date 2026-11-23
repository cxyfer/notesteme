// 試跑與比較版本共用的部分：參數（影音、範圍、provider、model）與試跑結果的顯示。
// 試跑不寫入任何產出（PRM-R6）；結果用「差異」模式（輸入 → 輸出逐字標示）並附上驗證結果。
import { useId } from "react";
import { useTranslation } from "react-i18next";

import { DiffText, diffChars } from "@shared";

import type { components } from "../../api/schema";
import { ItemPicker, TextField } from "../../components";
import { currentLanguage, formatNumber, formatTimecode } from "../../i18n";
import { KNOWN_PROVIDERS } from "../profiles/profileModel";

export type PlaygroundResult = components["schemas"]["PlaygroundBody"];

export interface RunParams {
  item: string;
  from: string;
  to: string;
  provider: string;
  model: string;
}

export const EMPTY_PARAMS: RunParams = { item: "", from: "", to: "", provider: "", model: "" };

/** 缺少的參數（全部有才可以送出） */
export function missingParams(p: RunParams): (keyof RunParams)[] {
  return (Object.keys(p) as (keyof RunParams)[]).filter((k) => !p[k].trim());
}

export function RunParamsFields({
  params,
  onChange,
}: {
  params: RunParams;
  onChange: (p: RunParams) => void;
}) {
  const { t } = useTranslation();
  const listId = useId();
  const set = (k: keyof RunParams) => (v: string) => onChange({ ...params, [k]: v });
  return (
    <div className="form-stack">
      <ItemPicker value={params.item} onChange={set("item")} />
      <div className="form-row">
        <TextField
          label={t("prompts.run.from")}
          value={params.from}
          onChange={(e) => set("from")(e.target.value)}
          placeholder="06:00"
          inputMode="numeric"
        />
        <TextField
          label={t("prompts.run.to")}
          value={params.to}
          onChange={(e) => set("to")(e.target.value)}
          placeholder="08:00"
          inputMode="numeric"
        />
      </div>
      <p className="field__hint">{t("prompts.run.rangeHint")}</p>
      <div className="form-row">
        <TextField
          label={t("prompts.run.provider")}
          value={params.provider}
          list={listId}
          onChange={(e) => set("provider")(e.target.value.trim())}
          autoComplete="off"
        />
        <datalist id={listId}>
          {KNOWN_PROVIDERS.map((p) => (
            <option key={p} value={p} />
          ))}
        </datalist>
        <TextField
          label={t("prompts.run.model")}
          value={params.model}
          onChange={(e) => set("model")(e.target.value.trim())}
          autoComplete="off"
        />
      </div>
    </div>
  );
}

export function estimateText(t: ReturnType<typeof useTranslation>["t"], usd: number | null) {
  return usd === null
    ? t("prompts.run.noEstimate")
    : t("prompts.run.estimate", {
        amount: new Intl.NumberFormat(currentLanguage(), {
          style: "currency",
          currency: "USD",
          maximumFractionDigits: 4,
        }).format(usd),
      });
}

/** 一次試跑的結果：驗證、用量與花費、每一句的差異。 */
export function RunResult({ result, compact }: { result: PlaygroundResult; compact?: boolean }) {
  const { t } = useTranslation();
  const v = result.validation;
  const cues = result.output ?? [];
  return (
    <div className="run-result">
      <p className={v.passed ? "run-result__ok" : "run-result__fail"} role="status">
        {v.passed
          ? t("prompts.run.validationPassed", { count: result.cues })
          : t("prompts.run.validationFailed")}
      </p>
      {!v.passed && (v.errors ?? []).length > 0 && (
        <ul className="run-result__errors">
          {(v.errors ?? []).map((e, i) => (
            <li key={i}>{e}</li>
          ))}
        </ul>
      )}
      <p className="secondary num">
        {t("prompts.run.usage", {
          version: result.prompt.version,
          input: formatNumber(result.usage.input_tokens),
          output: formatNumber(result.usage.output_tokens),
        })}{" "}
        {estimateText(t, result.estimate_usd)}
      </p>
      {!compact && (
        <ol className="run-cues">
          {cues.map((c) => (
            <li key={c.index} className="run-cues__row">
              <span className="run-cues__time num">{formatTimecode(c.start_ms / 1000)}</span>
              <span className="run-cues__text">
                {c.original !== undefined ? (
                  <DiffText
                    parts={diffChars(c.original, c.text)}
                    labels={{ del: t("diff.deleted"), ins: t("diff.inserted") }}
                  />
                ) : (
                  c.text
                )}
              </span>
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}
