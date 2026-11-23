// 筆記模板列表（docs/ui/pages.md §10；NOTE-R11）：內建在前，每個只列最新版本；新增或複製模板。
// 這個頁面是 lazy route。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { describeError } from "../../api/describeError";
import { isApiError } from "../../api/errors";
import { fieldErrorsOf } from "../../api/fieldErrors";
import { queryKeys } from "../../api/queryKeys";
import {
  Button,
  Dialog,
  EmptyState,
  ErrorCallout,
  SelectField,
  Spinner,
  TextField,
  useToast,
} from "../../components";
import { formatDateTime } from "../../i18n";
import { usePageTitle } from "../../shell/usePageTitle";

export const TEMPLATE_ID = /^[a-z0-9-]{1,64}$/;

export function useNoteTemplates() {
  return useQuery({
    queryKey: queryKeys.noteTemplates,
    queryFn: ({ signal }) => api.get("/api/note-templates", { signal }),
  });
}

export function NoteTemplatesPage() {
  const { t } = useTranslation();
  usePageTitle(t("pages.noteTemplates.title"));
  const templates = useNoteTemplates();
  const [creating, setCreating] = useState<string | null>(null);
  const list = templates.data?.templates ?? [];
  return (
    <>
      <div className="page__head">
        <h1 className="page__title">{t("pages.noteTemplates.title")}</h1>
        <Button onClick={() => setCreating(list[0]?.id ?? "video-notes")}>
          {t("noteTemplates.new")}
        </Button>
      </div>
      <p className="secondary page__lead">{t("noteTemplates.lead")}</p>
      {templates.isPending && <Spinner label={t("common.loading")} showLabel />}
      {templates.error != null && (
        <ErrorCallout
          error={templates.error}
          actions={[{ label: t("common.retry"), onClick: () => void templates.refetch() }]}
        />
      )}
      {templates.data && list.length === 0 && <EmptyState message={t("noteTemplates.empty")} />}
      {list.length > 0 && (
        <ul className="tune-list">
          {list.map((tpl) => (
            <li className="tune-row" key={tpl.id}>
              <div className="tune-row__main">
                <Link to="/note-templates/$id" params={{ id: tpl.id }} className="tune-row__title">
                  {tpl.name || tpl.id}
                </Link>
                <code className="secondary">{tpl.id}</code>
                {tpl.builtin && <span className="tag">{t("noteTemplates.builtinTag")}</span>}
                <span className="secondary num">
                  {t("noteTemplates.version", { version: tpl.version })}
                </span>
              </div>
              <p className="tune-row__meta secondary">
                {t("noteTemplates.checksCount", { count: (tpl.checks ?? []).length })}
                {t("common.listSeparator")}
                {t("noteTemplates.createdAt", { at: formatDateTime(tpl.created_at) })}
              </p>
              <div className="tune-row__actions">
                <Button variant="text" onClick={() => setCreating(tpl.id)}>
                  {t("noteTemplates.copy")}
                </Button>
              </div>
            </li>
          ))}
        </ul>
      )}
      {creating !== null && (
        <NewTemplateDialog
          from={creating}
          options={list.map((x) => ({ value: x.id, label: x.name || x.id }))}
          onClose={() => setCreating(null)}
        />
      )}
    </>
  );
}

export function NewTemplateDialog({
  from,
  options,
  onClose,
}: {
  from: string;
  options: { value: string; label: string }[];
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const [source, setSource] = useState(from);
  const [id, setId] = useState(`my-${from}`.slice(0, 64));
  const [name, setName] = useState("");
  const create = useMutation({
    mutationFn: () =>
      api.post("/api/note-templates", {
        body: { id, from: source, ...(name.trim() ? { name: name.trim() } : {}) },
      }),
    onSuccess: async (tpl) => {
      await qc.invalidateQueries({ queryKey: queryKeys.noteTemplates });
      toast.show({ title: t("noteTemplates.created", { id: tpl.id }) });
      onClose();
      await navigate({ to: "/note-templates/$id", params: { id: tpl.id } });
    },
  });
  const fe = fieldErrorsOf(create.error);
  const idError =
    (!TEMPLATE_ID.test(id) ? t("noteTemplates.idRule") : undefined) ??
    fe.id ??
    (isApiError(create.error) && create.error.code === "template_exists"
      ? describeError(t, create.error)
      : undefined);
  const opts = options.some((o) => o.value === source)
    ? options
    : [...options, { value: source, label: source }];
  function submit(e: FormEvent) {
    e.preventDefault();
    if (!TEMPLATE_ID.test(id)) return;
    create.mutate();
  }
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("noteTemplates.newTitle")}
      description={t("noteTemplates.newDescription")}
      footer={
        <Button
          variant="primary"
          type="submit"
          form="new-template"
          disabled={create.isPending || !TEMPLATE_ID.test(id)}
        >
          {t("noteTemplates.create")}
        </Button>
      }
    >
      <form id="new-template" className="form-stack" onSubmit={submit} noValidate>
        <SelectField
          label={t("noteTemplates.from")}
          value={source}
          options={opts}
          onChange={(e) => setSource(e.target.value)}
        />
        <TextField
          label={t("noteTemplates.id")}
          value={id}
          maxLength={64}
          autoComplete="off"
          onChange={(e) => setId(e.target.value)}
          error={idError}
        />
        <TextField
          label={t("noteTemplates.name")}
          value={name}
          onChange={(e) => setName(e.target.value)}
          error={fe.name}
        />
        {create.error != null && !idError && Object.keys(fe).length === 0 && (
          <ErrorCallout error={create.error} />
        )}
      </form>
    </Dialog>
  );
}
