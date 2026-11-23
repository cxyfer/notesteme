// 複製成自訂樣式、匯入 .ass 樣式（字幕樣式列表與編輯頁共用）。
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { describeError } from "../../api/describeError";
import { isApiError } from "../../api/errors";
import { fieldErrorsOf } from "../../api/fieldErrors";
import { queryKeys } from "../../api/queryKeys";
import { Button, Dialog, ErrorCallout, TextField, useToast } from "../../components";
import { validStyleName } from "./styleModel";

export function CopyStyleDialog({
  from,
  open,
  onOpenChange,
}: {
  from: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const [name, setName] = useState(`${from}-custom`);
  const [touched, setTouched] = useState(false);
  const create = useMutation({
    mutationFn: () => api.post("/api/ass-styles", { body: { from, name } }),
    onSuccess: async (st) => {
      await qc.invalidateQueries({ queryKey: queryKeys.assStyles.all });
      toast.show({ title: t("styles.copied", { name: st.name }) });
      onOpenChange(false);
      await navigate({ to: "/styles/$id", params: { id: st.id } });
    },
  });
  const nameError =
    (touched && !validStyleName(name) ? t("styles.nameRule") : undefined) ??
    fieldErrorsOf(create.error).name ??
    (isApiError(create.error) && create.error.code === "style_exists"
      ? describeError(t, create.error)
      : undefined);
  function submit(e: FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (!validStyleName(name)) return;
    create.mutate();
  }
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t("styles.copyTitle", { name: from })}
      description={t("styles.copyDescription")}
      footer={
        <Button variant="primary" type="submit" form="copy-style" disabled={create.isPending}>
          {t("styles.copy")}
        </Button>
      }
    >
      <form id="copy-style" onSubmit={submit} noValidate>
        <TextField
          label={t("styles.newName")}
          value={name}
          onChange={(e) => setName(e.target.value)}
          onBlur={() => setTouched(true)}
          hint={t("styles.nameRule")}
          error={nameError}
          autoComplete="off"
          required
        />
        {create.error && !nameError && <ErrorCallout error={create.error} />}
      </form>
    </Dialog>
  );
}

export function ImportStyleDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const [file, setFile] = useState<File | null>(null);
  const [name, setName] = useState("");
  const [primary, setPrimary] = useState("");
  const [translation, setTranslation] = useState("");
  const [touched, setTouched] = useState(false);
  const imp = useMutation({
    mutationFn: async () => {
      const content = await file!.text();
      return api.post("/api/ass-styles/import", {
        body: {
          content,
          name,
          primary_style: primary.trim() || undefined,
          translation_style: translation.trim() || undefined,
        },
      });
    },
    onSuccess: async (st) => {
      await qc.invalidateQueries({ queryKey: queryKeys.assStyles.all });
      toast.show({ title: t("styles.imported", { name: st.name }) });
      onOpenChange(false);
      await navigate({ to: "/styles/$id", params: { id: st.id } });
    },
  });
  const fe = fieldErrorsOf(imp.error);
  const nameError =
    (touched && !validStyleName(name) ? t("styles.nameRule") : undefined) ?? fe.name;
  const fileError = (touched && !file ? t("styles.importNeedFile") : undefined) ?? fe.content;
  function submit(e: FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (!file || !validStyleName(name)) return;
    imp.mutate();
  }
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t("styles.importTitle")}
      description={t("styles.importDescription")}
      footer={
        <Button variant="primary" type="submit" form="import-style" disabled={imp.isPending}>
          {t("styles.import")}
        </Button>
      }
    >
      <form id="import-style" className="form-stack" onSubmit={submit} noValidate>
        <TextField
          label={t("styles.importFile")}
          type="file"
          accept=".ass,.ssa"
          onChange={(e) => {
            const f = e.target.files?.[0] ?? null;
            setFile(f);
            if (f && !name) {
              const base = f.name
                .replace(/\.(ass|ssa)$/i, "")
                .toLowerCase()
                .replace(/[^a-z0-9._-]+/g, "-");
              if (validStyleName(base)) setName(base);
            }
          }}
          error={fileError}
        />
        <TextField
          label={t("styles.newName")}
          value={name}
          onChange={(e) => setName(e.target.value)}
          hint={t("styles.nameRule")}
          error={nameError}
          autoComplete="off"
          required
        />
        <TextField
          label={t("styles.importPrimary")}
          value={primary}
          onChange={(e) => setPrimary(e.target.value)}
          hint={t("styles.importPrimaryHint")}
          error={fe.primary_style}
        />
        <TextField
          label={t("styles.importTranslation")}
          value={translation}
          onChange={(e) => setTranslation(e.target.value)}
          hint={t("styles.importTranslationHint")}
          error={fe.translation_style}
        />
        {imp.error && !nameError && !fileError && <ErrorCallout error={imp.error} />}
      </form>
    </Dialog>
  );
}
