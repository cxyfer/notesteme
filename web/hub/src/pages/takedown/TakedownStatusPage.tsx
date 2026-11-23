// 下架申請的狀態（GET /api/v1/takedowns/:id，不需要登入，只有 {id, state}）。
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import { Button, ErrorCallout, Spinner, TextField } from "../../components";
import { usePageTitle } from "../../shell/usePageTitle";

export function TakedownStatusPage() {
  const { t } = useTranslation();
  usePageTitle(t("takedownStatus.title"));
  const { id } = useParams({ from: "/takedowns/$id" });
  const navigate = useNavigate();
  const [draft, setDraft] = useState(id);
  const q = useQuery({
    queryKey: queryKeys.takedown(id),
    queryFn: ({ signal }) =>
      api.get<"/api/v1/takedowns/{id}">(apiPath("/api/v1/takedowns/{id}", { id }), { signal }),
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const v = draft.trim();
    if (v) void navigate({ to: "/takedowns/$id", params: { id: v } });
  };
  return (
    <section aria-labelledby="tds-title" className="narrow">
      <h1 id="tds-title" className="page__title">
        {t("takedownStatus.title")}
      </h1>
      <form className="stack-inline" onSubmit={submit}>
        <TextField
          label={t("takedownStatus.id")}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          maxLength={64}
          className="mono"
        />
        <Button type="submit">{t("takedownStatus.check")}</Button>
      </form>
      {q.isPending ? (
        <Spinner showLabel />
      ) : q.isError ? (
        <ErrorCallout error={q.error} />
      ) : (
        <p role="status" className="takedown-state">
          {t(`takedownState.${q.data.state}`, { defaultValue: q.data.state })}
        </p>
      )}
    </section>
  );
}
