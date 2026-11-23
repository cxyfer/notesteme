// 預算（pages.md §12、COST-R4、R16）：月預算、單部上限、提醒門檻、預算時區。
// PUT 取代整個區段：只送這四個欄位（GET 另外回傳的 month、spent_usd… 送回去會 422「不認識的欄位」）；
// 「不限」送 null。
import { Link } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { Button, ErrorCallout, TextField, useToast } from "../../components";
import { formatUsdCents } from "../costs/money";
import { Loading, Panel, SaveError, useSettingsSection } from "./shared";

interface Budget {
  monthly_usd?: number | null;
  per_item_usd?: number | null;
  warn_at_percent?: number;
  timezone?: string;
  month?: string;
  spent_usd?: number;
  reserved_usd?: number;
  remaining_usd?: number | null;
}

const MAX_USD = 100000;

function usdText(v: number | null | undefined) {
  return v == null ? "" : String(v);
}

/** 空白＝不限（null）；不是 0～100000 的數字時回傳 NaN */
function usdValue(s: string): number | null {
  if (s.trim() === "") return null;
  const n = Number(s);
  return Number.isFinite(n) && n >= 0 && n <= MAX_USD ? n : NaN;
}

export function BudgetSection() {
  const { t } = useTranslation();
  const { query } = useSettingsSection("budget");
  if (query.isPending) return <Loading />;
  if (query.isError)
    return (
      <ErrorCallout
        error={query.error}
        actions={[{ label: t("common.retry"), onClick: () => void query.refetch() }]}
      />
    );
  return <BudgetForm key={JSON.stringify(query.data)} data={query.data as Budget} />;
}

function BudgetForm({ data }: { data: Budget }) {
  const { t } = useTranslation();
  const toast = useToast();
  const { save } = useSettingsSection("budget");
  const [monthly, setMonthly] = useState(usdText(data.monthly_usd));
  const [perItem, setPerItem] = useState(usdText(data.per_item_usd));
  const [warn, setWarn] = useState(String(data.warn_at_percent ?? 80));
  const [tz, setTz] = useState(data.timezone ?? "UTC");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const m = usdValue(monthly);
  const p = usdValue(perItem);
  const w = Number(warn);
  const warnBad = !Number.isInteger(w) || w < 1 || w > 100;
  const bad = Number.isNaN(m) || Number.isNaN(p) || warnBad || tz.trim() === "";

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (bad || pending) return;
    setPending(true);
    setError(undefined);
    try {
      await save({ monthly_usd: m, per_item_usd: p, warn_at_percent: w, timezone: tz.trim() });
      toast.show({ title: t("common.saved") });
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }

  return (
    <>
      {data.month && (
        <Panel title={t("settings.budget.thisMonth", { month: data.month })}>
          <p className="settings-panel__description">
            {t("settings.budget.spent", {
              spent: formatUsdCents(data.spent_usd ?? 0),
              reserved: formatUsdCents(data.reserved_usd ?? 0),
            })}{" "}
            {data.remaining_usd != null &&
              t("settings.budget.remaining", {
                remaining: formatUsdCents(data.remaining_usd),
              })}{" "}
            <Link to="/costs">{t("settings.budget.openCosts")}</Link>
          </p>
        </Panel>
      )}
      <Panel title={t("settings.budget.title")} description={t("settings.budget.hint")}>
        <form className="settings-form settings-form--narrow" onSubmit={submit} noValidate>
          <TextField
            label={t("settings.budget.monthly")}
            type="number"
            min={0}
            max={MAX_USD}
            value={monthly}
            onChange={(e) => setMonthly(e.target.value)}
            hint={t("settings.budget.blankUnlimited")}
            error={Number.isNaN(m) ? t("settings.budget.usdRule") : undefined}
          />
          <TextField
            label={t("settings.budget.perItem")}
            type="number"
            min={0}
            max={MAX_USD}
            value={perItem}
            onChange={(e) => setPerItem(e.target.value)}
            hint={t("settings.budget.blankUnlimited")}
            error={Number.isNaN(p) ? t("settings.budget.usdRule") : undefined}
          />
          <TextField
            label={t("settings.budget.warnAt")}
            type="number"
            min={1}
            max={100}
            value={warn}
            onChange={(e) => setWarn(e.target.value)}
            hint={t("settings.budget.warnHint")}
            error={warnBad ? t("settings.budget.warnRule") : undefined}
          />
          <TextField
            label={t("settings.budget.timezone")}
            value={tz}
            autoComplete="off"
            onChange={(e) => setTz(e.target.value)}
            hint={t("settings.budget.timezoneHint")}
          />
          {error !== undefined && <SaveError error={error} />}
          <div className="settings-actions">
            <Button variant="primary" type="submit" disabled={bad || pending}>
              {pending ? t("common.saving") : t("settings.common.save")}
            </Button>
          </div>
        </form>
      </Panel>
    </>
  );
}
