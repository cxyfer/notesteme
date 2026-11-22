// 媒體庫的篩選列：標題搜尋、平台、錯誤類別、「更多篩選」（頻道、語言、時間來源、發布日、長度、各步驟的狀態），
// 以及已套用條件的清單（可以個別移除或全部清除）。所有條件都在 URL（filters.ts）。
import { forwardRef, useEffect, useId, useState, type Ref } from "react";
import { useTranslation } from "react-i18next";

import { Button } from "../../components";
import {
  ERROR_CLASSES,
  hasFilters,
  PLATFORMS,
  STAGE_STATUSES,
  STAGES,
  TIMINGS,
  toggleStatus,
  type LibrarySearch,
} from "./filters";

export type SearchPatch = Partial<Record<keyof LibrarySearch, unknown>>;

export interface FilterBarProps {
  search: LibrarySearch;
  /** 合併後重新驗證；replace=true 時不新增瀏覽紀錄（打字中的搜尋） */
  onChange: (patch: SearchPatch, opts?: { replace?: boolean }) => void;
  onClear: () => void;
}

const DEBOUNCE_MS = 300;

/** 打字時延遲送出；外部的值改變（例如「清除全部」）時跟著更新。 */
function useDebounced(
  external: string | undefined,
  commit: (v: string | undefined) => void,
): [string, (v: string) => void, () => void] {
  const [value, setValue] = useState(external ?? "");
  const [seen, setSeen] = useState(external);
  if (seen !== external) {
    setSeen(external);
    if ((external ?? "") !== value.trim()) setValue(external ?? "");
  }
  const trimmed = value.trim();
  useEffect(() => {
    if (trimmed === (external ?? "")) return;
    const id = setTimeout(() => commit(trimmed || undefined), DEBOUNCE_MS);
    return () => clearTimeout(id);
  }, [trimmed, external, commit]);
  const flush = () => commit(trimmed || undefined);
  return [value, setValue, flush];
}

function TextFilter({
  label,
  value: external,
  onCommit,
  type = "text",
  hint,
  inputMode,
}: {
  label: string;
  value: string | undefined;
  onCommit: (v: string | undefined) => void;
  type?: "text" | "number";
  hint?: string;
  inputMode?: "numeric";
}) {
  const id = useId();
  const [value, setValue, flush] = useDebounced(external, onCommit);
  return (
    <div className="field filter__field">
      <label className="field__label" htmlFor={id}>
        {label}
      </label>
      <input
        id={id}
        className="field__input"
        type={type}
        min={type === "number" ? 0 : undefined}
        inputMode={inputMode}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") flush();
        }}
        aria-describedby={hint ? `${id}-hint` : undefined}
      />
      {hint && (
        <span id={`${id}-hint`} className="field__hint">
          {hint}
        </span>
      )}
    </div>
  );
}

function SelectFilter<T extends string>({
  label,
  value,
  options,
  optionLabel,
  onChange,
}: {
  label: string;
  value: T | undefined;
  options: readonly T[];
  optionLabel: (v: T) => string;
  onChange: (v: T | undefined) => void;
}) {
  const { t } = useTranslation();
  const id = useId();
  return (
    <div className="field filter__field">
      <label className="field__label" htmlFor={id}>
        {label}
      </label>
      <select
        id={id}
        className="field__input"
        value={value ?? ""}
        onChange={(e) => onChange((e.target.value || undefined) as T | undefined)}
      >
        <option value="">{t("library.filters.any")}</option>
        {options.map((o) => (
          <option key={o} value={o}>
            {optionLabel(o)}
          </option>
        ))}
      </select>
    </div>
  );
}

export const SearchBox = forwardRef(function SearchBox(
  { value: external, onCommit }: { value: string | undefined; onCommit: (v?: string) => void },
  ref: Ref<HTMLInputElement>,
) {
  const { t } = useTranslation();
  const id = useId();
  const [value, setValue] = useDebounced(external, onCommit);
  return (
    <div className="field filter__search">
      <label className="field__label" htmlFor={id}>
        {t("library.filters.search")}
      </label>
      <input
        ref={ref}
        id={id}
        className="field__input"
        type="search"
        value={value}
        maxLength={200}
        aria-describedby={`${id}-hint`}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Escape" && value) {
            // Esc 清除搜尋（不讓外層把它當成其他動作）
            e.preventDefault();
            e.stopPropagation();
            setValue("");
            onCommit(undefined);
          }
        }}
      />
      <span id={`${id}-hint`} className="field__hint">
        {t("library.filters.searchHint")}
      </span>
    </div>
  );
});

/** 已套用的條件：每個一顆可以移除的 chip */
function useAppliedChips(search: LibrarySearch, onChange: FilterBarProps["onChange"]) {
  const { t } = useTranslation();
  const chips: { key: string; label: string; remove: () => void }[] = [];
  const add = (key: string, label: string, patch: SearchPatch) =>
    chips.push({ key, label, remove: () => onChange(patch) });
  if (search.q) add("q", t("library.chips.q", { value: search.q }), { q: undefined });
  if (search.platform)
    add("platform", t("library.chips.platform", { value: t(`platform.${search.platform}`) }), {
      platform: undefined,
    });
  if (search.channel)
    add("channel", t("library.chips.channel", { value: search.channel }), { channel: undefined });
  for (const s of search.status ?? []) {
    const [stage, status] = s.split(":");
    add(
      `status-${s}`,
      t("library.chips.status", {
        stage: t(`stage.${stage}`),
        status: t(`stageStatus.${status}`),
      }),
      { status: toggleStatus(search.status, s) },
    );
  }
  if (search.error_class)
    add(
      "error_class",
      t("library.chips.errorClass", { value: t(`errorClass.${search.error_class}`) }),
      { error_class: undefined },
    );
  if (search.lang)
    add("lang", t("library.chips.lang", { value: search.lang }), { lang: undefined });
  if (search.timing)
    add("timing", t("library.chips.timing", { value: t(`timing.${search.timing}`) }), {
      timing: undefined,
    });
  if (search.published_from)
    add("published_from", t("library.chips.publishedFrom", { value: search.published_from }), {
      published_from: undefined,
    });
  if (search.published_to)
    add("published_to", t("library.chips.publishedTo", { value: search.published_to }), {
      published_to: undefined,
    });
  if (search.dur_min !== undefined)
    add("dur_min", t("library.chips.durMin", { count: search.dur_min }), { dur_min: undefined });
  if (search.dur_max !== undefined)
    add("dur_max", t("library.chips.durMax", { count: search.dur_max }), { dur_max: undefined });
  return chips;
}

export function FilterBar({ search, onChange, onClear }: FilterBarProps) {
  const { t } = useTranslation();
  const [more, setMore] = useState(false);
  const panelId = useId();
  const chips = useAppliedChips(search, onChange);
  const moreCount =
    [
      search.channel,
      search.lang,
      search.timing,
      search.published_from,
      search.published_to,
      search.dur_min,
      search.dur_max,
    ].filter((v) => v !== undefined).length + (search.status?.length ?? 0);

  return (
    <div className="filters">
      <div className="filters__row">
        <SelectFilter
          label={t("library.filters.platform")}
          value={search.platform}
          options={PLATFORMS}
          optionLabel={(v) => t(`platform.${v}`)}
          onChange={(v) => onChange({ platform: v })}
        />
        <SelectFilter
          label={t("library.filters.errorClass")}
          value={search.error_class}
          options={ERROR_CLASSES}
          optionLabel={(v) => t(`errorClass.${v}`)}
          onChange={(v) => onChange({ error_class: v })}
        />
        <Button
          className="filters__more"
          aria-expanded={more}
          aria-controls={panelId}
          onClick={() => setMore((m) => !m)}
        >
          {moreCount > 0
            ? t("library.filters.moreCount", { count: moreCount })
            : t("library.filters.more")}
        </Button>
      </div>

      <div id={panelId} className="filters__panel" hidden={!more}>
        <div className="filters__grid">
          <TextFilter
            label={t("library.filters.channel")}
            hint={t("library.filters.channelHint")}
            value={search.channel}
            onCommit={(v) => onChange({ channel: v })}
          />
          <TextFilter
            label={t("library.filters.lang")}
            hint={t("library.filters.langHint")}
            value={search.lang}
            onCommit={(v) => onChange({ lang: v })}
          />
          <SelectFilter
            label={t("library.filters.timing")}
            value={search.timing}
            options={TIMINGS}
            optionLabel={(v) => t(`timing.${v}`)}
            onChange={(v) => onChange({ timing: v })}
          />
          <DateFilter
            label={t("library.filters.publishedFrom")}
            value={search.published_from}
            onChange={(v) => onChange({ published_from: v })}
          />
          <DateFilter
            label={t("library.filters.publishedTo")}
            value={search.published_to}
            onChange={(v) => onChange({ published_to: v })}
          />
          <TextFilter
            type="number"
            inputMode="numeric"
            label={t("library.filters.durMin")}
            value={search.dur_min?.toString()}
            onCommit={(v) => onChange({ dur_min: v })}
          />
          <TextFilter
            type="number"
            inputMode="numeric"
            label={t("library.filters.durMax")}
            value={search.dur_max?.toString()}
            onCommit={(v) => onChange({ dur_max: v })}
          />
        </div>
        <fieldset className="filters__stages">
          <legend>{t("library.filters.stageStatus")}</legend>
          <p className="field__hint">{t("library.filters.stageStatusHint")}</p>
          {STAGES.map((stage) => (
            <fieldset className="filters__stage" key={stage}>
              <legend>{t(`stage.${stage}`)}</legend>
              {STAGE_STATUSES.map((status) => {
                const v = `${stage}:${status}`;
                return (
                  <label className="check" key={status}>
                    <input
                      type="checkbox"
                      checked={search.status?.includes(v) ?? false}
                      onChange={() => onChange({ status: toggleStatus(search.status, v) })}
                    />
                    <span>{t(`stageStatus.${status}`)}</span>
                  </label>
                );
              })}
            </fieldset>
          ))}
        </fieldset>
      </div>

      {hasFilters(search) && (
        <div className="filters__applied">
          <h2 className="visually-hidden">{t("library.filters.applied")}</h2>
          <ul className="chips">
            {chips.map((c) => (
              <li className="chip" key={c.key}>
                <span>{c.label}</span>
                <button
                  type="button"
                  className="chip__remove"
                  aria-label={t("library.chips.remove", { label: c.label })}
                  onClick={c.remove}
                >
                  <svg viewBox="0 0 16 16" aria-hidden="true" focusable="false">
                    <path
                      d="m4.5 4.5 7 7m0-7-7 7"
                      stroke="currentColor"
                      strokeWidth="1.6"
                      strokeLinecap="round"
                    />
                  </svg>
                </button>
              </li>
            ))}
          </ul>
          <Button variant="text" onClick={onClear}>
            {t("library.filters.clearAll")}
          </Button>
        </div>
      )}
    </div>
  );
}

function DateFilter({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string | undefined;
  onChange: (v: string | undefined) => void;
}) {
  const id = useId();
  return (
    <div className="field filter__field">
      <label className="field__label" htmlFor={id}>
        {label}
      </label>
      <input
        id={id}
        className="field__input"
        type="date"
        value={value ?? ""}
        onChange={(e) => onChange(e.target.value || undefined)}
      />
    </div>
  );
}
