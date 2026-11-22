// 語言（pages.md §12、SRC-R1～R3、TRN）：預設目標語言、原文字幕的語言優先順序、介面語言。
// GET 回傳的是合併了內建預設的生效值；PUT 整段取代，所以送出完整的區段（不送空字串或 null）。
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { Button, ErrorCallout, TextField, useToast } from "../../components";
import { currentLanguage, LANGUAGE_NAMES, LANGUAGES, setLanguage, type Language } from "../../i18n";
import { Loading, Panel, SaveError, useSettingsSection } from "./shared";

interface Rule {
  lang: string;
  kind: "manual" | "auto";
}

interface LanguageForm {
  targets: string;
  caption_priority: Rule[];
  allow_platform_translated: boolean;
  fallback: string;
  variant_strategy: "convert" | "localize";
}

const KINDS = ["manual", "auto"] as const;
const STRATEGIES = ["convert", "localize"] as const;

function splitList(s: string): string[] {
  return s
    .split(/[,，、\s]+/)
    .map((x) => x.trim())
    .filter(Boolean);
}

function toForm(v: Record<string, unknown>): LanguageForm {
  const list = (x: unknown) =>
    Array.isArray(x) ? x.filter((e): e is string => typeof e === "string") : [];
  const rules = Array.isArray(v.caption_priority)
    ? (v.caption_priority as unknown[]).flatMap((r): Rule[] => {
        const o = r as { lang?: unknown; kind?: unknown };
        return typeof o.lang === "string" && (o.kind === "manual" || o.kind === "auto")
          ? [{ lang: o.lang, kind: o.kind } satisfies Rule]
          : [];
      })
    : [];
  return {
    targets: list(v.targets).join(", "),
    caption_priority: rules,
    allow_platform_translated: v.allow_platform_translated === true,
    fallback: list(v.fallback).join(", "),
    variant_strategy: v.variant_strategy === "localize" ? "localize" : "convert",
  };
}

export function LanguageSection() {
  const { t } = useTranslation();
  const { query } = useSettingsSection("language");
  return (
    <>
      {query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <ErrorCallout
          error={query.error}
          actions={[{ label: t("common.retry"), onClick: () => void query.refetch() }]}
        />
      ) : (
        <LanguageEditor initial={toForm(query.data)} />
      )}
      <InterfaceLanguage />
    </>
  );
}

function LanguageEditor({ initial }: { initial: LanguageForm }) {
  const { t } = useTranslation();
  const toast = useToast();
  const { save } = useSettingsSection("language");
  const [form, setForm] = useState(initial);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();

  const set = <K extends keyof LanguageForm>(k: K, v: LanguageForm[K]) =>
    setForm((f) => ({ ...f, [k]: v }));
  const setRule = (i: number, r: Partial<Rule>) =>
    set(
      "caption_priority",
      form.caption_priority.map((x, j) => (j === i ? { ...x, ...r } : x)),
    );
  const move = (i: number, d: -1 | 1) => {
    const xs = [...form.caption_priority];
    const [r] = xs.splice(i, 1);
    xs.splice(i + d, 0, r!);
    set("caption_priority", xs);
  };

  const setTargets = (v: string) => set("targets", v);
  const setFallback = (v: string) => set("fallback", v);
  const removeRule = (i: number) =>
    set(
      "caption_priority",
      form.caption_priority.filter((_, j) => j !== i),
    );
  const addRule = () =>
    set("caption_priority", [...form.caption_priority, { lang: "", kind: "manual" }]);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setPending(true);
    setError(undefined);
    try {
      const out = await save({
        targets: splitList(form.targets),
        caption_priority: form.caption_priority
          .map((r) => ({ lang: r.lang.trim(), kind: r.kind }))
          .filter((r) => r.lang !== ""),
        allow_platform_translated: form.allow_platform_translated,
        fallback: splitList(form.fallback),
        variant_strategy: form.variant_strategy,
      });
      setForm(toForm(out));
      toast.show({ title: t("settings.common.saved") });
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }

  return (
    <form className="settings-form" onSubmit={onSubmit} noValidate>
      <Panel title={t("settings.language.targetsTitle")}>
        <TextField
          label={t("settings.language.targets")}
          hint={t("settings.language.targetsHint")}
          value={form.targets}
          onChange={(e) => setTargets(e.target.value)}
        />
        <label className="settings-field">
          <span className="field__label">{t("settings.language.variant")}</span>
          <select
            value={form.variant_strategy}
            onChange={(e) => set("variant_strategy", e.target.value as "convert" | "localize")}
          >
            {STRATEGIES.map((s) => (
              <option key={s} value={s}>
                {t(`settings.language.variantOption.${s}`)}
              </option>
            ))}
          </select>
        </label>
      </Panel>

      <Panel
        title={t("settings.language.priorityTitle")}
        description={t("settings.language.priorityHint")}
      >
        <ol className="settings-rules">
          {form.caption_priority.map((r, i) => (
            <li key={i} className="settings-rule">
              <TextField
                label={t("settings.language.ruleLang", { n: i + 1 })}
                value={r.lang}
                onChange={(e) => setRule(i, { lang: e.target.value })}
              />
              <label className="settings-field">
                <span className="field__label">
                  {t("settings.language.ruleKind", { n: i + 1 })}
                </span>
                <select
                  value={r.kind}
                  onChange={(e) => setRule(i, { kind: e.target.value as Rule["kind"] })}
                >
                  {KINDS.map((k) => (
                    <option key={k} value={k}>
                      {t(`settings.language.kind.${k}`)}
                    </option>
                  ))}
                </select>
              </label>
              <div className="settings-actions">
                <Button
                  variant="text"
                  disabled={i === 0}
                  aria-label={t("settings.language.moveUp", { n: i + 1 })}
                  onClick={() => move(i, -1)}
                >
                  {t("settings.common.up")}
                </Button>
                <Button
                  variant="text"
                  disabled={i === form.caption_priority.length - 1}
                  aria-label={t("settings.language.moveDown", { n: i + 1 })}
                  onClick={() => move(i, 1)}
                >
                  {t("settings.common.down")}
                </Button>
                <Button
                  variant="text"
                  aria-label={t("settings.language.removeRule", { n: i + 1 })}
                  onClick={() => removeRule(i)}
                >
                  {t("settings.common.remove")}
                </Button>
              </div>
            </li>
          ))}
        </ol>
        <Button onClick={addRule}>{t("settings.language.addRule")}</Button>
        <label className="settings-check">
          <input
            type="checkbox"
            checked={form.allow_platform_translated}
            onChange={(e) => set("allow_platform_translated", e.target.checked)}
          />
          {t("settings.language.allowTranslated")}
        </label>
        <TextField
          label={t("settings.language.fallback")}
          hint={t("settings.language.fallbackHint")}
          value={form.fallback}
          onChange={(e) => setFallback(e.target.value)}
        />
      </Panel>

      {error !== undefined && <SaveError error={error} />}
      <div className="settings-actions">
        <Button type="submit" variant="primary" disabled={pending}>
          {t("settings.common.save")}
        </Button>
      </div>
    </form>
  );
}

/** 介面語言：存在瀏覽器（localStorage），和導覽底部的快速切換是同一個值。 */
function InterfaceLanguage() {
  const { t, i18n } = useTranslation();
  void i18n.language;
  return (
    <Panel title={t("settings.language.uiTitle")} description={t("settings.language.uiHint")}>
      <label className="settings-field">
        <span className="field__label">{t("settings.language.uiLabel")}</span>
        <select
          value={currentLanguage()}
          onChange={(e) => void setLanguage(e.target.value as Language)}
        >
          {LANGUAGES.map((l) => (
            <option key={l} value={l} lang={l}>
              {LANGUAGE_NAMES[l]}
            </option>
          ))}
        </select>
      </label>
    </Panel>
  );
}
