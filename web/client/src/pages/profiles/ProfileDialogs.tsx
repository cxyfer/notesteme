// 新增處理方案、複製處理方案（列表與編輯頁共用）：POST /api/profiles。
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
import { stripNulls, validProfileName, type Config } from "./profileModel";

export function NewProfileDialog({
  from,
  config,
  onClose,
}: {
  /** 複製來源的名稱；沒有時是新增空白的處理方案 */
  from?: string;
  config?: Config;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const [name, setName] = useState(from ? `${from}-copy` : "");
  const [touched, setTouched] = useState(false);
  const create = useMutation({
    mutationFn: () =>
      api.post("/api/profiles", {
        body: { name, config: (stripNulls(config ?? {}) as Config) ?? {} },
      }),
    onSuccess: async (p) => {
      await qc.invalidateQueries({ queryKey: queryKeys.profiles });
      toast.show({
        title: from
          ? t("profiles.copied", { name: p.name })
          : t("profiles.created", { name: p.name }),
      });
      onClose();
      await navigate({ to: "/profiles/$id", params: { id: p.name } });
    },
  });
  const nameError =
    (touched && !validProfileName(name) ? t("profiles.nameRule") : undefined) ??
    fieldErrorsOf(create.error).name ??
    (isApiError(create.error) && create.error.code === "profile_exists"
      ? describeError(t, create.error)
      : undefined);
  function submit(e: FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (!validProfileName(name)) return;
    create.mutate();
  }
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={from ? t("profiles.copyTitle", { name: from }) : t("profiles.newTitle")}
      description={from ? t("profiles.copyDescription") : t("profiles.newDescription")}
      footer={
        <Button variant="primary" type="submit" form="new-profile" disabled={create.isPending}>
          {from ? t("profiles.copy") : t("profiles.create")}
        </Button>
      }
    >
      <form id="new-profile" onSubmit={submit} noValidate>
        <TextField
          label={t("profiles.name")}
          value={name}
          onChange={(e) => setName(e.target.value)}
          onBlur={() => setTouched(true)}
          hint={t("profiles.nameRule")}
          error={nameError}
          autoComplete="off"
          required
        />
        {create.error && !nameError && <ErrorCallout error={create.error} />}
      </form>
    </Dialog>
  );
}
