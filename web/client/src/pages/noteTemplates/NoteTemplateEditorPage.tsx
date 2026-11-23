// 筆記模板編輯（docs/ui/pages.md §10；NOTE-R7、R11、R12）：三欄。
//   左：模板內文、名稱、「閱讀」按鈕、自動檢查（PATCH 建立新版本；內建模板唯讀，只能複製）
//   中：筆記 prompt（模板與 prompt 沒有綁定的 API，只連到 Prompt 庫）
//   右：預覽（選一部有筆記的影音；只能預覽已儲存的版本 <id>@<版本>，不呼叫 LLM）
// 這個頁面是 lazy route。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Link } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { isApiError } from "../../api/errors";
import { fieldErrorsOf } from "../../api/fieldErrors";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import {
  Button,
  EmptyState,
  ErrorCallout,
  ItemPicker,
  Spinner,
  TextField,
  useToast,
} from "../../components";
import { NoteMarkdown } from "../../notes/NoteMarkdown";
import { usePageTitle } from "../../shell/usePageTitle";
import { NoteChecks } from "../../notes/NoteChecks";
import { NewTemplateDialog, useNoteTemplates } from "./NoteTemplatesPage";

type Template = components["schemas"]["NoteTemplateBody"];

/** NOTE-R7 的五項自動檢查 */
export const NOTE_CHECKS = [
  "frontmatter",
  "heading_timestamps",
  "images_exist",
  "image_names",
  "self_check",
] as const;
/** NOTE-R11：模板可以用的變數（與 internal/notes 一致；後端改了要同步） */
const TEMPLATE_VARS = [
  ".Frontmatter",
  ".Title",
  ".ReadButton",
  ".InfoTable",
  ".Sections",
  ".SelfCheck",
  ".Lang",
  ".Item.URL",
  ".Item.Channel",
  ".Item.Published",
  ".Item.Duration",
] as const;
const NOTE_PROMPTS = ["note-default", "note-map-default"] as const;

const route = getRouteApi("/app/note-templates/$id");

export function NoteTemplateEditorPage() {
  const { t } = useTranslation();
  const { id } = route.useParams();
  const templates = useNoteTemplates();
  const list = templates.data?.templates ?? [];
  const tpl = list.find((x) => x.id === id);
  usePageTitle(tpl ? tpl.name || tpl.id : t("pages.noteTemplates.title"));
  return (
    <>
      <nav className="crumbs" aria-label={t("noteTemplates.breadcrumb")}>
        <Link to="/note-templates">{t("pages.noteTemplates.title")}</Link>
      </nav>
      {!tpl ? (
        <>
          <h1 className="page__title">{t("pages.noteTemplates.title")}</h1>
          {templates.isPending && <Spinner label={t("common.loading")} showLabel />}
          {templates.error != null && (
            <ErrorCallout
              error={templates.error}
              actions={[{ label: t("common.retry"), onClick: () => void templates.refetch() }]}
            />
          )}
          {templates.data && <EmptyState message={t("noteTemplates.notFound")} />}
        </>
      ) : (
        <Editor
          key={`${tpl.id}@${tpl.version}`}
          tpl={tpl}
          options={list.map((x) => ({ value: x.id, label: x.name || x.id }))}
        />
      )}
    </>
  );
}

function Editor({ tpl, options }: { tpl: Template; options: { value: string; label: string }[] }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState(tpl.name);
  const [body, setBody] = useState(tpl.body);
  const [readButton, setReadButton] = useState(tpl.read_button);
  const [checks, setChecks] = useState<string[]>(tpl.checks ?? []);
  const [copying, setCopying] = useState(false);
  const readOnly = tpl.builtin;

  const savedChecks = tpl.checks ?? [];
  const checksChanged =
    checks.length !== savedChecks.length || checks.some((c) => !savedChecks.includes(c));
  const dirty =
    name !== tpl.name || body !== tpl.body || readButton !== tpl.read_button || checksChanged;

  const save = useMutation({
    mutationFn: () => {
      const patch: components["schemas"]["PatchNoteTemplateInputBody"] = {};
      if (name !== tpl.name) patch.name = name;
      if (body !== tpl.body) patch.body = body;
      if (readButton !== tpl.read_button) patch.read_button = readButton;
      // 全部取消勾選時明確送 []（不執行任何檢查），不是省略
      if (checksChanged) patch.checks = NOTE_CHECKS.filter((c) => checks.includes(c));
      return api.patch<"/api/note-templates/{id}">(
        apiPath("/api/note-templates/{id}", { id: tpl.id }),
        { body: patch },
      );
    },
    onSuccess: async (v) => {
      await qc.invalidateQueries({ queryKey: queryKeys.noteTemplates });
      toast.show({ title: t("noteTemplates.savedAs", { version: v.version }) });
    },
  });
  const fe = fieldErrorsOf(save.error);
  function submit(e: FormEvent) {
    e.preventDefault();
    if (dirty && !readOnly) save.mutate();
  }

  return (
    <>
      <div className="page__head">
        <h1 className="page__title">{tpl.name || tpl.id}</h1>
        <span className="secondary num">
          {t("noteTemplates.version", { version: tpl.version })}
        </span>
        {readOnly && <span className="tag">{t("noteTemplates.builtinTag")}</span>}
        <div className="page__actions">
          <Button onClick={() => setCopying(true)}>{t("noteTemplates.copy")}</Button>
        </div>
      </div>
      {readOnly && <p className="secondary">{t("noteTemplates.builtinReadOnly")}</p>}
      <div className="note-editor">
        <form className="note-editor__column" onSubmit={submit} noValidate>
          <h2>{t("noteTemplates.templateTitle")}</h2>
          <TextField
            label={t("noteTemplates.name")}
            value={name}
            readOnly={readOnly}
            onChange={(e) => setName(e.target.value)}
            error={fe.name}
          />
          <div className="field">
            <label className="field__label" htmlFor="note-template-body">
              {t("noteTemplates.body")}
            </label>
            <textarea
              id="note-template-body"
              className="field__input code-textarea"
              value={body}
              readOnly={readOnly}
              spellCheck={false}
              onChange={(e) => setBody(e.target.value)}
              aria-invalid={fe.body ? true : undefined}
              aria-describedby={fe.body ? "note-template-body-error" : undefined}
            />
            {fe.body && (
              <span id="note-template-body-error" className="field__error">
                {fe.body}
              </span>
            )}
          </div>
          <details>
            <summary>{t("noteTemplates.vars")}</summary>
            <ul className="var-list">
              {TEMPLATE_VARS.map((v) => (
                <li key={v}>
                  <code>{`{{${v}}}`}</code>
                </li>
              ))}
            </ul>
          </details>
          <TextField
            label={t("noteTemplates.readButton")}
            value={readButton}
            readOnly={readOnly}
            onChange={(e) => setReadButton(e.target.value)}
            hint={t("noteTemplates.readButtonHint")}
            error={fe.read_button}
          />
          <fieldset className="plain-fieldset" disabled={readOnly}>
            <legend>{t("noteTemplates.checks")}</legend>
            {NOTE_CHECKS.map((c) => (
              <label className="check-row" key={c}>
                <input
                  type="checkbox"
                  checked={checks.includes(c)}
                  onChange={(e) =>
                    setChecks((prev) =>
                      e.target.checked ? [...prev, c] : prev.filter((x) => x !== c),
                    )
                  }
                />
                {t(`noteTemplates.check.${c}`)}
              </label>
            ))}
            <p className="secondary">{t("noteTemplates.checksHint")}</p>
          </fieldset>
          {save.error != null && Object.keys(fe).length === 0 && (
            <ErrorCallout error={save.error} />
          )}
          {!readOnly && (
            <div className="page__actions page__actions--start">
              <Button variant="primary" type="submit" disabled={!dirty || save.isPending}>
                {save.isPending ? t("common.saving") : t("noteTemplates.saveVersion")}
              </Button>
              {dirty && (
                <Button
                  variant="text"
                  onClick={() => {
                    setName(tpl.name);
                    setBody(tpl.body);
                    setReadButton(tpl.read_button);
                    setChecks(tpl.checks ?? []);
                  }}
                >
                  {t("common.discard")}
                </Button>
              )}
            </div>
          )}
          <p className="secondary">{t("noteTemplates.layoutNote")}</p>
        </form>

        <section className="note-editor__column" aria-labelledby="note-prompt-title">
          <h2 id="note-prompt-title">{t("noteTemplates.promptTitle")}</h2>
          <p className="secondary">{t("noteTemplates.promptNote")}</p>
          <ul className="var-list">
            {NOTE_PROMPTS.map((p) => (
              <li key={p}>
                <Link to="/prompts/$id" params={{ id: p }}>
                  {p}
                </Link>
              </li>
            ))}
          </ul>
        </section>

        <PreviewColumn tpl={tpl} dirty={dirty} />
      </div>
      {copying && (
        <NewTemplateDialog from={tpl.id} options={options} onClose={() => setCopying(false)} />
      )}
    </>
  );
}

function PreviewColumn({ tpl, dirty }: { tpl: Template; dirty: boolean }) {
  const { t } = useTranslation();
  const [item, setItem] = useState("");
  const ref = `${tpl.id}@${tpl.version}`;
  // 只預覽已儲存的版本；選了影音才送出，不重試（409 no_note 是正常結果）
  const preview = useQuery({
    queryKey: [...queryKeys.noteTemplates, "preview", ref, item],
    queryFn: ({ signal }) =>
      api.post<"/api/note-templates/{id}/preview">(
        apiPath("/api/note-templates/{id}/preview", { id: ref }),
        { body: { item }, signal },
      ),
    enabled: item !== "",
    retry: false,
    staleTime: 60_000,
  });
  const result = preview.data;
  const loading = preview.isFetching;
  const error = preview.error;
  const noNote = isApiError(error) && error.code === "no_note";
  return (
    <section className="note-editor__column" aria-labelledby="note-preview-title">
      <h2 id="note-preview-title">{t("noteTemplates.previewTitle")}</h2>
      <p className="secondary">{t("noteTemplates.previewNote")}</p>
      {dirty && (
        <p className="secondary">{t("noteTemplates.previewSaved", { version: tpl.version })}</p>
      )}
      <ItemPicker value={item} onChange={setItem} />
      {loading && <Spinner label={t("common.loading")} showLabel />}
      {noNote ? (
        <p className="secondary">{t("noteTemplates.noNote")}</p>
      ) : (
        error != null && <ErrorCallout error={error} live={false} />
      )}
      {result && !loading && (
        <>
          <NoteChecks checks={result.checks ?? []} />
          <NoteMarkdown markdown={result.markdown} label={t("noteTemplates.previewResult")} />
        </>
      )}
    </section>
  );
}
