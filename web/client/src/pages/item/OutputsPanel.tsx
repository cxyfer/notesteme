// 輸出與下載（OUT-R1、R6、R11、R13～R15）：
//   - 下載：GET /api/items/{id}/subtitles?format=… 即時渲染（同源、帶 cookie 的一般連結）。
//   - ASS 可以選擇樣式（OUT-R6，`style=` 是樣式名稱）；樣式清單讀不到時退回預設樣式的連結，不擋下載。
//   - 輸出目標寫過的檔案（sidecar、folder）與寫入時的雜湊。
//   - 匯入修正過的字幕（SRT、VTT、ASS）成新的 edited 版本；422 時指出第幾句或檔案第幾行。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useId, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { describeError } from "../../api/describeError";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import { Button, ErrorCallout, SelectField, Spinner, useToast } from "../../components";
import { formatBytes, formatDateTime } from "../../i18n";
import type { ApiOutput } from "./data";

export const DOWNLOAD_FORMATS = ["srt", "vtt", "ass", "bilingual-srt", "txt"] as const;

/** 匯入的檔案大小上限（字幕是純文字；避免把大檔讀進記憶體） */
const MAX_IMPORT_BYTES = 10 * 1024 * 1024;

/** 內建的預設 ASS 樣式（後端省略 style 時用它，OUT-R6） */
export const DEFAULT_ASS_STYLE = "default";

export function downloadUrl(itemId: string, format: string, style?: string) {
  const base = `${apiPath("/api/items/{id}/subtitles", { id: itemId })}?format=${encodeURIComponent(format)}`;
  return style ? `${base}&style=${encodeURIComponent(style)}` : base;
}

export function OutputsPanel({
  itemId,
  outputs,
  isPending,
  error,
  onRetry,
  hasTranslation,
}: {
  itemId: string;
  outputs: readonly ApiOutput[] | undefined;
  isPending: boolean;
  error: unknown;
  onRetry: () => void;
  hasTranslation: boolean;
}) {
  const { t } = useTranslation();
  const formats = DOWNLOAD_FORMATS.filter((f) => f !== "bilingual-srt" || hasTranslation);
  // 樣式清單和設定頁共用快取；讀不到（權限、未啟用、錯誤）時不重試，只提供預設樣式的連結。
  const styles = useQuery({
    queryKey: queryKeys.assStyles.list(),
    queryFn: ({ signal }) => api.get("/api/ass-styles", { signal }),
    retry: false,
  });
  const styleNames = (styles.data?.styles ?? []).map((s) => s.name);
  const [style, setStyle] = useState(DEFAULT_ASS_STYLE);
  // 選過的樣式被刪掉時回到預設，不送出會 422 的名稱
  const chosen = styleNames.includes(style) ? style : DEFAULT_ASS_STYLE;
  return (
    <div className="outputs">
      <section aria-labelledby="dl-title">
        <h2 id="dl-title" className="section__title">
          {t("item.outputs.download")}
        </h2>
        <ul className="outputs__downloads">
          {formats.map((f) =>
            f === "ass" && styleNames.length > 0 ? (
              <li key={f} className="outputs__ass">
                <a
                  className="button button--outline"
                  href={downloadUrl(itemId, f, chosen)}
                  download
                >
                  {t("item.outputs.assStyled", { style: chosen })}
                </a>
                <SelectField
                  className="outputs__style"
                  label={t("item.outputs.assStyle")}
                  value={chosen}
                  onChange={(e) => setStyle(e.target.value)}
                  options={styleNames.map((n) => ({ value: n, label: n }))}
                />
              </li>
            ) : (
              <li key={f}>
                <a className="button button--outline" href={downloadUrl(itemId, f)} download>
                  {t(`item.outputs.formats.${f}`)}
                </a>
              </li>
            ),
          )}
        </ul>
      </section>

      <section aria-labelledby="out-title">
        <h2 id="out-title" className="section__title">
          {t("item.outputs.written")}
        </h2>
        {isPending ? (
          <Spinner label={t("item.outputs.loading")} showLabel />
        ) : error && !outputs ? (
          <ErrorCallout error={error} actions={[{ label: t("common.retry"), onClick: onRetry }]} />
        ) : !outputs?.length ? (
          <p className="secondary">{t("item.outputs.none")}</p>
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th scope="col">{t("item.outputs.target")}</th>
                <th scope="col">{t("item.outputs.format")}</th>
                <th scope="col">{t("item.outputs.lang")}</th>
                <th scope="col">{t("item.outputs.path")}</th>
                <th scope="col">{t("item.outputs.size")}</th>
                <th scope="col">{t("item.outputs.writtenAt")}</th>
              </tr>
            </thead>
            <tbody>
              {outputs.map((o) => (
                <tr key={`${o.target}:${o.path}`}>
                  <td>{t(`item.outputs.targets.${o.target}`, { defaultValue: o.target })}</td>
                  <td>{o.format}</td>
                  <td>{o.lang}</td>
                  <td>
                    <code className="path">{o.path}</code>
                  </td>
                  <td className="num">{formatBytes(o.size)}</td>
                  <td className="num">{formatDateTime(o.written_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <ImportForm itemId={itemId} />
    </div>
  );
}

function ImportForm({ itemId }: { itemId: string }) {
  const { t } = useTranslation();
  const id = useId();
  const qc = useQueryClient();
  const toast = useToast();
  const fileRef = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | undefined>();
  const [lang, setLang] = useState("");
  const [localError, setLocalError] = useState<string | undefined>();
  const imp = useMutation({
    mutationFn: async (f: File) =>
      api.post<"/api/items/{id}/subtitles/import">(
        apiPath("/api/items/{id}/subtitles/import", { id: itemId }),
        {
          body: {
            content: await f.text(),
            name: f.name,
            ...(lang.trim() ? { lang: lang.trim() } : {}),
          },
        },
      ),
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: queryKeys.items.cuesOf(itemId) });
      void qc.invalidateQueries({ queryKey: queryKeys.items.artifacts(itemId) });
      void qc.invalidateQueries({ queryKey: queryKeys.items.detail(itemId) });
      toast.show({
        title: r?.stale_translation
          ? t("item.import.doneStale", { count: r.cues })
          : t("item.import.done", { count: r?.cues ?? 0 }),
      });
      setFile(undefined);
      if (fileRef.current) fileRef.current.value = "";
    },
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setLocalError(undefined);
    if (!file) {
      setLocalError(t("item.import.pickFirst"));
      return;
    }
    if (file.size > MAX_IMPORT_BYTES) {
      setLocalError(t("item.import.tooLarge"));
      return;
    }
    imp.mutate(file);
  }

  const err = imp.error;
  const where =
    isApiError(err) && err.status === 422
      ? err.body.line
        ? t("item.import.atCue", { line: err.body.line })
        : err.body.file_line
          ? t("item.import.atLine", { line: err.body.file_line })
          : ""
      : "";

  return (
    <section aria-labelledby="imp-title">
      <h2 id="imp-title" className="section__title">
        {t("item.import.title")}
      </h2>
      <p className="secondary">{t("item.import.description")}</p>
      <form className="form form--inline" onSubmit={submit} noValidate>
        <div className="field">
          <label className="field__label" htmlFor={`${id}-file`}>
            {t("item.import.file")}
          </label>
          <input
            ref={fileRef}
            id={`${id}-file`}
            type="file"
            accept=".srt,.vtt,.ass,.ssa"
            aria-invalid={localError ? true : undefined}
            onChange={(e) => setFile(e.target.files?.[0])}
          />
        </div>
        <div className="field">
          <label className="field__label" htmlFor={`${id}-lang`}>
            {t("item.import.lang")}
          </label>
          <input
            id={`${id}-lang`}
            className="field__input"
            value={lang}
            onChange={(e) => setLang(e.target.value)}
            placeholder={t("item.import.langPlaceholder")}
            autoComplete="off"
          />
        </div>
        <Button type="submit" disabled={imp.isPending}>
          {imp.isPending ? t("item.import.submitting") : t("item.import.submit")}
        </Button>
      </form>
      {localError && (
        <p className="field__error" role="alert">
          {localError}
        </p>
      )}
      {imp.isError && (
        <ErrorCallout
          error={err}
          message={
            where ? t("item.import.errorAt", { where, message: describeError(t, err) }) : undefined
          }
        />
      )}
    </section>
  );
}
