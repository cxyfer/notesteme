// 新增 prompt（POST /api/prompts）：空白新增，或把內建／既有的 prompt「另存為新的 prompt」（PRM-R4）。
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { describeError } from "../../api/describeError";
import { isApiError } from "../../api/errors";
import { fieldErrorsOf } from "../../api/fieldErrors";
import { queryKeys } from "../../api/queryKeys";
import { Button, Dialog, ErrorCallout, SelectField, TextField, useToast } from "../../components";
import { PROMPT_KINDS, validPromptId } from "./promptModel";

export function NewPromptDialog({
  from,
  onClose,
}: {
  /** 另存的來源：id、類型與內文 */
  from?: { id: string; kind: string; body: string };
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const [id, setId] = useState(from ? `my-${from.id}` : "");
  const [kind, setKind] = useState<string>(from?.kind ?? "correct");
  const [touched, setTouched] = useState(false);
  const create = useMutation({
    mutationFn: () =>
      api.post("/api/prompts", {
        // 空白新增時給一段最小的內文，之後在編輯頁修改（內文不能是空的）
        body: {
          id,
          kind,
          body: from?.body ?? t("prompts.newBodyPlaceholder", { example: "{{glossary}}" }),
        },
      }),
    onSuccess: async (p) => {
      await qc.invalidateQueries({ queryKey: queryKeys.prompts.all });
      toast.show({ title: t("prompts.created", { id: p.id }) });
      onClose();
      await navigate({ to: "/prompts/$id", params: { id: p.id } });
    },
  });
  const fe = fieldErrorsOf(create.error);
  const idError =
    (touched && !validPromptId(id) ? t("prompts.idRule") : undefined) ??
    fe.id ??
    (isApiError(create.error) && create.error.code === "prompt_exists"
      ? describeError(t, create.error)
      : undefined);
  const kindOptions: { value: string; label: string }[] = PROMPT_KINDS.map((k) => ({
    value: k,
    label: t(`prompts.kind.${k}`),
  }));
  if (!kindOptions.some((o) => o.value === kind)) kindOptions.push({ value: kind, label: kind });
  function submit(e: FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (!validPromptId(id)) return;
    create.mutate();
  }
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={from ? t("prompts.saveAsNewTitle", { id: from.id }) : t("prompts.newTitle")}
      description={from ? t("prompts.saveAsNewDescription") : t("prompts.newDescription")}
      footer={
        <Button variant="primary" type="submit" form="new-prompt" disabled={create.isPending}>
          {from ? t("prompts.saveAsNew") : t("prompts.create")}
        </Button>
      }
    >
      <form id="new-prompt" className="form-stack" onSubmit={submit} noValidate>
        <TextField
          label={t("prompts.id")}
          value={id}
          onChange={(e) => setId(e.target.value)}
          onBlur={() => setTouched(true)}
          hint={t("prompts.idRule")}
          error={idError}
          autoComplete="off"
          spellCheck={false}
          required
        />
        <SelectField
          label={t("prompts.kindLabel")}
          value={kind}
          onChange={(e) => setKind(e.target.value)}
          options={kindOptions}
          disabled={!!from}
          error={fe.kind}
        />
        {create.error && !idError && !fe.kind && <ErrorCallout error={create.error} />}
      </form>
    </Dialog>
  );
}
