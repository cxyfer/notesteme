// 字幕樣式編輯頁（docs/ui/pages.md §9）：原文行／譯文行／版面三個分頁、即時預覽、儲存（PATCH 只送改過的欄位）。
// 內建樣式唯讀，只能「複製成自訂樣式」。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi } from "@tanstack/react-router";
import { useId, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { fieldErrorsOf } from "../../api/fieldErrors";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import { Button, ErrorCallout, SelectField, Spinner, TextField, useToast } from "../../components";
import { usePageTitle } from "../../shell/usePageTitle";
import { NotFoundContent } from "../NotFoundPage";
import { CopyStyleDialog } from "./StyleDialogs";
import {
  ALIGNMENTS,
  BORDER_STYLES,
  compactPatch,
  FONT_SUGGESTIONS,
  LANG_OPTIONS,
  ORDER_OPTIONS,
  SHOW_OPTIONS,
  TRANSLATION_AT_OPTIONS,
  validatePatch,
  type Layout,
  type Line,
  type LineKey,
  type Style,
  type StylePatch,
} from "./styleModel";
import { StylePreview } from "./StylePreview";

const route = getRouteApi("/app/styles/$id");

// 版面欄位的 API 路徑（422 的 errors[].path）
const LAYOUT_PATH = {
  show: "layout.show",
  order: "layout.order",
  translation_at: "layout.translation_at",
  alignment: "layout.alignment",
  margin_v: "layout.margin_v",
  border_style: "layout.border_style",
} as const;
const TABS = ["primary", "translation", "layout"] as const;
type Tab = (typeof TABS)[number];

export function StyleEditorPage() {
  const { t } = useTranslation();
  const { id } = route.useParams();
  usePageTitle(`${id} · ${t("pages.styles.title")}`);
  const qc = useQueryClient();
  const toast = useToast();
  // 目標語言與原文語言：沒有指定字型的行依它們帶入預設字型（OUT-R7），預覽也用目標語言
  const [lang, setLang] = useState("");
  const [sourceLang, setSourceLang] = useState("");
  const style = useQuery({
    queryKey: queryKeys.assStyles.detail(id, lang, sourceLang),
    queryFn: ({ signal }) =>
      api.get<"/api/ass-styles/{id}">(apiPath("/api/ass-styles/{id}", { id }), {
        query: { lang: lang || undefined, source_lang: sourceLang || undefined },
        signal,
      }) as Promise<Style>,
    placeholderData: (prev) => prev,
  });
  const [patch, setPatch] = useState<StylePatch>({});
  const [tab, setTab] = useState<Tab>("primary");
  const [copying, setCopying] = useState(false);
  const save = useMutation({
    mutationFn: (body: StylePatch) =>
      api.patch<"/api/ass-styles/{id}">(apiPath("/api/ass-styles/{id}", { id }), { body }),
    onSuccess: async () => {
      setPatch({});
      await qc.invalidateQueries({ queryKey: queryKeys.assStyles.all });
      toast.show({ title: t("common.saved") });
    },
  });

  const heading = <h1 className="page__title">{id}</h1>;
  if (style.isPending) {
    return (
      <>
        {heading}
        <Spinner label={t("common.loading")} />
      </>
    );
  }
  if (style.error && !style.data) {
    if (isApiError(style.error) && style.error.status === 404) return <NotFoundContent />;
    return (
      <>
        {heading}
        <ErrorCallout
          error={style.error}
          actions={[{ label: t("common.retry"), onClick: () => void style.refetch() }]}
        />
      </>
    );
  }
  const st = style.data!;
  const readOnly = st.builtin;
  const compact = compactPatch(patch);
  const localErrors = validatePatch(patch);
  const serverErrors = fieldErrorsOf(save.error);
  const errorFor = (path: string) =>
    localErrors[path]
      ? t(`styles.fieldError.${localErrors[path]}`)
      : (serverErrors[path] ?? undefined);
  const hasLocalErrors = Object.keys(localErrors).length > 0;

  function setLine<K extends keyof Line>(key: LineKey, field: K, value: Line[K] | undefined) {
    setPatch((p) => {
      const line = { ...(p[key] ?? {}) } as Record<string, unknown>;
      if (value === undefined || value === st[key][field]) delete line[field];
      else line[field] = value;
      return { ...p, [key]: line };
    });
  }
  function setLayout<K extends keyof Layout>(field: K, value: Layout[K]) {
    setPatch((p) => {
      const layout = { ...(p.layout ?? {}) } as Record<string, unknown>;
      if (value === st.layout[field]) delete layout[field];
      else layout[field] = value;
      return { ...p, layout };
    });
  }

  return (
    <>
      <div className="page__head">
        {heading}
        <span className="tag">{readOnly ? t("styles.builtinTag") : t("styles.customTag")}</span>
        <div className="page__actions">
          {readOnly ? (
            <Button variant="primary" onClick={() => setCopying(true)}>
              {t("styles.copyToCustom")}
            </Button>
          ) : (
            <>
              <Button onClick={() => setCopying(true)}>{t("styles.duplicate")}</Button>
              <Button
                variant="text"
                disabled={!compact || save.isPending}
                onClick={() => {
                  setPatch({});
                  save.reset();
                }}
              >
                {t("common.discard")}
              </Button>
              <Button
                variant="primary"
                disabled={!compact || hasLocalErrors || save.isPending}
                onClick={() => compact && save.mutate(compact)}
              >
                {save.isPending ? t("common.saving") : t("common.saveChanges")}
              </Button>
            </>
          )}
        </div>
      </div>
      {readOnly && <p className="secondary page__lead">{t("styles.builtinReadOnly")}</p>}
      {save.error && Object.keys(serverErrors).length === 0 && <ErrorCallout error={save.error} />}
      <div className="style-editor">
        <StylePreview style={id} patch={compact} lang={lang} disabled={hasLocalErrors} />
        <div className="style-editor__form">
          <div className="style-editor__langs">
            <SelectField
              label={t("styles.targetLang")}
              value={lang}
              onChange={(e) => setLang(e.target.value)}
              options={LANG_OPTIONS.map((l) => ({
                value: l,
                label: t(`styles.lang.${l || "none"}`),
              }))}
              hint={t("styles.targetLangHint")}
            />
            <SelectField
              label={t("styles.sourceLang")}
              value={sourceLang}
              onChange={(e) => setSourceLang(e.target.value)}
              options={LANG_OPTIONS.map((l) => ({
                value: l,
                label: t(`styles.lang.${l || "none"}`),
              }))}
            />
          </div>
          <Tabs tab={tab} onChange={setTab} />
          <div
            className="style-editor__panel"
            id={`style-panel-${tab}`}
            role="tabpanel"
            aria-labelledby={`style-tab-${tab}`}
          >
            <fieldset className="plain-fieldset" disabled={readOnly}>
              <legend className="visually-hidden">{t(`styles.tab.${tab}`)}</legend>
              {tab === "layout" ? (
                <LayoutFields
                  layout={{ ...st.layout, ...patch.layout }}
                  onChange={setLayout}
                  errorFor={errorFor}
                />
              ) : (
                <LineFields
                  key={tab}
                  lineKey={tab}
                  line={{ ...st[tab], ...patch[tab] }}
                  onChange={(f, v) => setLine(tab, f, v)}
                  errorFor={errorFor}
                />
              )}
            </fieldset>
          </div>
        </div>
      </div>
      {copying && <CopyStyleDialog from={id} open onOpenChange={(o) => !o && setCopying(false)} />}
    </>
  );
}

function Tabs({ tab, onChange }: { tab: Tab; onChange: (t: Tab) => void }) {
  const { t } = useTranslation();
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  function onKey(e: KeyboardEvent, i: number) {
    let next = -1;
    if (e.key === "ArrowRight") next = (i + 1) % TABS.length;
    else if (e.key === "ArrowLeft") next = (i - 1 + TABS.length) % TABS.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = TABS.length - 1;
    if (next < 0) return;
    e.preventDefault();
    onChange(TABS[next]!);
    refs.current[next]?.focus();
  }
  return (
    <div className="tabs" role="tablist" aria-label={t("styles.tabs")}>
      {TABS.map((k, i) => (
        <button
          key={k}
          ref={(el) => {
            refs.current[i] = el;
          }}
          type="button"
          role="tab"
          id={`style-tab-${k}`}
          className="tabs__tab"
          aria-selected={tab === k}
          aria-controls={`style-panel-${k}`}
          tabIndex={tab === k ? 0 : -1}
          onClick={() => onChange(k)}
          onKeyDown={(e) => onKey(e, i)}
        >
          {t(`styles.tab.${k}`)}
        </button>
      ))}
    </div>
  );
}

function numberOrUndefined(s: string): number | undefined {
  if (s.trim() === "") return undefined;
  const n = Number(s);
  return Number.isNaN(n) ? Number.NaN : n;
}

function LineFields({
  lineKey,
  line,
  onChange,
  errorFor,
}: {
  lineKey: LineKey;
  line: Line;
  onChange: <K extends keyof Line>(field: K, value: Line[K] | undefined) => void;
  errorFor: (path: string) => string | undefined;
}) {
  const { t } = useTranslation();
  const listId = useId();
  const path = {
    font: `${lineKey}.font`,
    size: `${lineKey}.size`,
    color: `${lineKey}.color`,
    outline_color: `${lineKey}.outline_color`,
    outline: `${lineKey}.outline`,
    back_color: `${lineKey}.back_color`,
    shadow: `${lineKey}.shadow`,
  };
  return (
    <div className="form-grid">
      <TextField
        label={t("styles.field.font")}
        value={line.font}
        list={listId}
        onChange={(e) => onChange("font", e.target.value)}
        hint={t("styles.field.fontHint")}
        error={errorFor(path.font)}
      />
      <datalist id={listId}>
        {FONT_SUGGESTIONS.map((f) => (
          <option key={f} value={f} />
        ))}
      </datalist>
      <TextField
        label={t("styles.field.size")}
        type="number"
        min={1}
        max={400}
        value={Number.isNaN(line.size) ? "" : line.size}
        onChange={(e) => onChange("size", numberOrUndefined(e.target.value) ?? Number.NaN)}
        error={errorFor(path.size)}
      />
      <ColorField
        label={t("styles.field.color")}
        value={line.color}
        onChange={(v) => onChange("color", v)}
        error={errorFor(path.color)}
      />
      <ColorField
        label={t("styles.field.outlineColor")}
        value={line.outline_color}
        onChange={(v) => onChange("outline_color", v)}
        error={errorFor(path.outline_color)}
      />
      <TextField
        label={t("styles.field.outline")}
        type="number"
        min={0}
        max={50}
        step={0.5}
        value={Number.isNaN(line.outline) ? "" : line.outline}
        onChange={(e) => onChange("outline", numberOrUndefined(e.target.value) ?? Number.NaN)}
        error={errorFor(path.outline)}
      />
      <ColorField
        label={t("styles.field.backColor")}
        value={line.back_color}
        onChange={(v) => onChange("back_color", v)}
        error={errorFor(path.back_color)}
      />
      <TextField
        label={t("styles.field.shadow")}
        type="number"
        min={0}
        max={50}
        step={0.5}
        value={Number.isNaN(line.shadow) ? "" : line.shadow}
        onChange={(e) => onChange("shadow", numberOrUndefined(e.target.value) ?? Number.NaN)}
        error={errorFor(path.shadow)}
      />
      <label className="check">
        <input
          type="checkbox"
          checked={line.bold}
          onChange={(e) => onChange("bold", e.target.checked)}
        />
        {t("styles.field.bold")}
      </label>
    </div>
  );
}

/** 顏色：#RRGGBB 或 #RRGGBBAA。<input type="color"> 會丟掉不透明度，所以用文字欄位加色塊。 */
function ColorField({
  label,
  value,
  onChange,
  error,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  error?: string;
}) {
  const { t } = useTranslation();
  return (
    <div className="color-field">
      <TextField
        label={label}
        value={value}
        onChange={(e) => onChange(e.target.value.trim())}
        hint={t("styles.field.colorHint")}
        error={error}
        spellCheck={false}
        autoComplete="off"
      />
      <span
        className="color-field__swatch"
        aria-hidden="true"
        style={{ background: /^#[0-9A-Fa-f]{6,8}$/.test(value) ? value : "transparent" }}
      />
    </div>
  );
}

function LayoutFields({
  layout,
  onChange,
  errorFor,
}: {
  layout: Layout;
  onChange: <K extends keyof Layout>(field: K, value: Layout[K]) => void;
  errorFor: (path: string) => string | undefined;
}) {
  const { t } = useTranslation();
  return (
    <div className="form-grid">
      <SelectField
        label={t("styles.field.show")}
        value={layout.show}
        onChange={(e) => onChange("show", e.target.value)}
        options={SHOW_OPTIONS.map((v) => ({ value: v, label: t(`styles.show.${v}`) }))}
        error={errorFor(LAYOUT_PATH.show)}
      />
      <SelectField
        label={t("styles.field.order")}
        value={layout.order}
        onChange={(e) => onChange("order", e.target.value)}
        options={ORDER_OPTIONS.map((v) => ({ value: v, label: t(`styles.order.${v}`) }))}
        disabled={layout.show !== "both"}
        error={errorFor(LAYOUT_PATH.order)}
      />
      <SelectField
        label={t("styles.field.translationAt")}
        value={layout.translation_at}
        onChange={(e) => onChange("translation_at", e.target.value)}
        options={TRANSLATION_AT_OPTIONS.map((v) => ({
          value: v,
          label: t(`styles.translationAt.${v}`),
        }))}
        disabled={layout.show !== "both"}
        error={errorFor(LAYOUT_PATH.translation_at)}
      />
      <SelectField
        label={t("styles.field.alignment")}
        value={String(layout.alignment)}
        onChange={(e) => onChange("alignment", Number(e.target.value))}
        options={ALIGNMENTS.map((v) => ({ value: String(v), label: t(`styles.alignment.${v}`) }))}
        error={errorFor(LAYOUT_PATH.alignment)}
      />
      <TextField
        label={t("styles.field.marginV")}
        type="number"
        min={0}
        max={1080}
        value={Number.isNaN(layout.margin_v) ? "" : layout.margin_v}
        onChange={(e) => onChange("margin_v", numberOrUndefined(e.target.value) ?? Number.NaN)}
        error={errorFor(LAYOUT_PATH.margin_v)}
      />
      <SelectField
        label={t("styles.field.borderStyle")}
        value={String(layout.border_style)}
        onChange={(e) => onChange("border_style", Number(e.target.value))}
        options={BORDER_STYLES.map((v) => ({
          value: String(v),
          label: t(`styles.borderStyle.${v}`),
        }))}
        error={errorFor(LAYOUT_PATH.border_style)}
      />
    </div>
  );
}
