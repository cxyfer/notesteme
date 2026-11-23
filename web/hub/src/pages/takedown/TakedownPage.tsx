// 下架申請表單（HACC-R15，不需要登入）：版本 ID、理由、說明（20～5,000 字）、聯絡 email、善意聲明。
// 成功後顯示申請編號（沒有 email 通知，申請人要記下編號回來查，HACC A5）。
import { useMutation } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { fieldErrorsOf } from "../../api/fieldErrors";
import { Button, CopyButton, ErrorCallout, SelectField, TextField } from "../../components";
import { usePageTitle } from "../../shell/usePageTitle";

export const TAKEDOWN_REASONS = ["copyright", "privacy", "other"] as const;

export function TakedownPage() {
  const { t } = useTranslation();
  usePageTitle(t("takedown.title"));
  const search = useSearch({ from: "/takedown" });
  const [versionId, setVersionId] = useState(search.version ?? "");
  const [reason, setReason] = useState<string>("copyright");
  const [statement, setStatement] = useState("");
  const [email, setEmail] = useState("");
  const [goodFaith, setGoodFaith] = useState(false);
  const [lookup, setLookup] = useState("");
  const navigate = useNavigate();
  const m = useMutation({
    mutationFn: () =>
      api.post("/api/v1/takedowns", {
        body: {
          version_id: versionId.trim(),
          reason,
          statement,
          contact_email: email.trim(),
          good_faith: goodFaith,
        },
      }),
  });
  const errs = fieldErrorsOf(m.error);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    m.mutate();
  };

  if (m.isSuccess)
    return (
      <section aria-labelledby="takedown-title" className="narrow">
        <h1 id="takedown-title" className="page__title">
          {t("takedown.sent")}
        </h1>
        <p role="status">{t("takedown.sentBody", { id: m.data.id })}</p>
        <p className="mono break">{m.data.id}</p>
        <CopyButton text={m.data.id} label={t("takedown.copyId")} />
        <p>
          <Link to="/takedowns/$id" params={{ id: m.data.id }}>
            {t("takedown.checkStatus")}
          </Link>
        </p>
      </section>
    );

  const stmtLen = [...statement.trim()].length;
  return (
    <section aria-labelledby="takedown-title" className="narrow">
      <h1 id="takedown-title" className="page__title">
        {t("takedown.title")}
      </h1>
      <p className="secondary">{t("takedown.intro")}</p>
      <form className="stack" onSubmit={submit} noValidate>
        <TextField
          label={t("takedown.version")}
          hint={t("takedown.versionHint")}
          value={versionId}
          onChange={(e) => setVersionId(e.target.value)}
          error={errs.version_id}
          required
          maxLength={64}
          className="mono"
        />
        <SelectField
          label={t("takedown.reason")}
          value={reason}
          options={TAKEDOWN_REASONS.map((r) => ({ value: r, label: t(`takedownReason.${r}`) }))}
          onChange={(e) => setReason(e.target.value)}
          error={errs.reason}
        />
        <div className="field">
          <label className="field__label" htmlFor="takedown-statement">
            {t("takedown.statement")}
          </label>
          <textarea
            id="takedown-statement"
            className="field__input"
            rows={8}
            maxLength={5000}
            value={statement}
            required
            aria-invalid={errs.statement ? true : undefined}
            aria-describedby="takedown-statement-hint"
            onChange={(e) => setStatement(e.target.value)}
          />
          <span id="takedown-statement-hint" className="field__hint">
            {t("takedown.statementHint", { count: stmtLen })}
          </span>
          {errs.statement && <span className="field__error">{errs.statement}</span>}
        </div>
        <TextField
          label={t("takedown.email")}
          hint={t("takedown.emailHint")}
          type="email"
          autoComplete="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          error={errs.contact_email}
          required
          maxLength={254}
        />
        <label className="check">
          <input
            type="checkbox"
            checked={goodFaith}
            onChange={(e) => setGoodFaith(e.target.checked)}
            aria-invalid={errs.good_faith ? true : undefined}
          />
          {t("takedown.goodFaith")}
        </label>
        {errs.good_faith && <span className="field__error">{errs.good_faith}</span>}
        {m.isError && Object.keys(errs).length === 0 && <ErrorCallout error={m.error} />}
        <div>
          <Button type="submit" variant="primary" disabled={m.isPending || !goodFaith}>
            {t("takedown.submit")}
          </Button>
        </div>
      </form>
      <form
        className="stack-inline lookup"
        aria-label={t("takedownStatus.title")}
        onSubmit={(e) => {
          e.preventDefault();
          const v = lookup.trim();
          if (v) void navigate({ to: "/takedowns/$id", params: { id: v } });
        }}
      >
        <TextField
          label={t("takedown.lookup")}
          value={lookup}
          onChange={(e) => setLookup(e.target.value)}
          maxLength={64}
          className="mono"
        />
        <Button type="submit">{t("takedownStatus.check")}</Button>
      </form>
    </section>
  );
}
