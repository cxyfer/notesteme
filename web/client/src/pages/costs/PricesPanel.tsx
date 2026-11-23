// 計價表（COST-R12、R13）：內建與覆寫的單價、確認日期與是否過期；確認內建計價表、新增或覆寫單價
// （已有單價時先確認「舊價 → 新價」）、移除覆寫（先確認）。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { describeError } from "../../api/describeError";
import { fieldErrorsOf } from "../../api/fieldErrors";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import {
  Button,
  Dialog,
  ErrorCallout,
  SelectField,
  Spinner,
  TextField,
  useToast,
} from "../../components";
import { formatUnitPrice } from "./money";

type Price = components["schemas"]["PriceEntry"];
const UNIT_TYPES = ["input_mtok", "output_mtok", "audio_hour", "credit"] as const;

export function PricesPanel() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const prices = useQuery({
    queryKey: queryKeys.prices,
    queryFn: ({ signal }) => api.get("/api/prices", { signal }),
  });
  const [editing, setEditing] = useState<Partial<Price> | null>(null);
  const [removing, setRemoving] = useState<Price | null>(null);
  const refresh = async () => {
    await qc.invalidateQueries({ queryKey: queryKeys.prices });
    await qc.invalidateQueries({ queryKey: queryKeys.costs.all });
  };
  const confirm = useMutation({
    mutationFn: () => api.post("/api/prices/confirm"),
    onSuccess: async (r) => {
      qc.setQueryData(queryKeys.prices, r);
      toast.show({ title: t("costs.prices.confirmed") });
      await qc.invalidateQueries({ queryKey: queryKeys.costs.all });
    },
    onError: (err) => toast.show({ title: describeError(t, err) }),
  });
  const remove = useMutation({
    mutationFn: (p: Price) =>
      api.delete("/api/prices", {
        body: { provider: p.provider, model: p.model, unit_type: p.unit_type },
      }),
    onSuccess: async () => {
      setRemoving(null);
      toast.show({ title: t("costs.prices.removed") });
      await refresh();
    },
  });
  const list = prices.data?.prices ?? [];
  return (
    <section id="prices" className="tune-section" aria-labelledby="prices-title">
      <h2 id="prices-title" className="tune-section__title">
        {t("costs.prices.title")}
      </h2>
      {prices.isPending && <Spinner label={t("common.loading")} showLabel />}
      {prices.error != null && (
        <ErrorCallout
          error={prices.error}
          actions={[{ label: t("common.retry"), onClick: () => void prices.refetch() }]}
        />
      )}
      {prices.data && (
        <>
          <p className="secondary">
            {prices.data.builtin_stale
              ? t("costs.prices.builtinStale", { date: prices.data.builtin_checked_at })
              : t("costs.prices.builtinChecked", { date: prices.data.builtin_checked_at })}
          </p>
          <div className="page__actions page__actions--start">
            <Button onClick={() => confirm.mutate()} disabled={confirm.isPending}>
              {t("costs.prices.confirm")}
            </Button>
            <Button onClick={() => setEditing({})}>{t("costs.prices.add")}</Button>
          </div>
          {list.length > 0 && (
            <div className="data-table-wrap">
              <table className="data-table">
                <caption className="visually-hidden">{t("costs.prices.title")}</caption>
                <thead>
                  <tr>
                    <th scope="col">{t("costs.prices.provider")}</th>
                    <th scope="col">{t("costs.prices.model")}</th>
                    <th scope="col">{t("costs.prices.unit")}</th>
                    <th scope="col" className="data-table__amount">
                      {t("costs.prices.price")}
                    </th>
                    <th scope="col">{t("costs.prices.source")}</th>
                    <th scope="col">{t("costs.prices.checked")}</th>
                    <th scope="col">
                      <span className="visually-hidden">{t("costs.prices.actions")}</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {list.map((p) => (
                    <tr key={`${p.provider}/${p.model}/${p.unit_type}`}>
                      <td>{p.provider}</td>
                      <td className="break-anywhere">{p.model}</td>
                      <td>{t(`costs.unitType.${p.unit_type}`, { defaultValue: p.unit_type })}</td>
                      <td className="num data-table__amount">{formatUnitPrice(p.usd_per_unit)}</td>
                      <td>
                        {t(`costs.prices.sourceName.${p.source}`, { defaultValue: p.source })}
                      </td>
                      <td className="num">
                        {p.checked_at}
                        {p.stale && (
                          <span className="data-table__problem"> {t("costs.prices.stale")}</span>
                        )}
                      </td>
                      <td>
                        <div className="page__actions">
                          <Button variant="text" onClick={() => setEditing(p)}>
                            {t("costs.prices.override")}
                          </Button>
                          {p.source === "override" && (
                            <Button variant="text" onClick={() => setRemoving(p)}>
                              {t("costs.prices.remove")}
                            </Button>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
      {editing && (
        <PriceDialog
          initial={editing}
          prices={list}
          onClose={() => setEditing(null)}
          onSaved={async () => {
            setEditing(null);
            toast.show({ title: t("costs.prices.saved") });
            await refresh();
          }}
        />
      )}
      <Dialog
        open={removing !== null}
        onOpenChange={(o) => !o && setRemoving(null)}
        title={t("costs.prices.removeTitle")}
        description={
          removing
            ? t("costs.prices.removeDescription", {
                name: `${removing.provider}／${removing.model}`,
                unit: t(`costs.unitType.${removing.unit_type}`, {
                  defaultValue: removing.unit_type,
                }),
              })
            : undefined
        }
        footer={
          <Button
            variant="danger"
            onClick={() => removing && remove.mutate(removing)}
            disabled={remove.isPending}
          >
            {t("costs.prices.remove")}
          </Button>
        }
      >
        {remove.error != null && <ErrorCallout error={remove.error} />}
      </Dialog>
    </section>
  );
}

function PriceDialog({
  initial,
  prices,
  onClose,
  onSaved,
}: {
  initial: Partial<Price>;
  prices: readonly Price[];
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const { t } = useTranslation();
  const [provider, setProvider] = useState(initial.provider ?? "");
  const [model, setModel] = useState(initial.model ?? "");
  const [unit, setUnit] = useState<string>(initial.unit_type ?? "input_mtok");
  const [usd, setUsd] = useState(initial.usd_per_unit != null ? String(initial.usd_per_unit) : "");
  // 已有單價時先確認「舊價 → 新價」再送出
  const [confirming, setConfirming] = useState(false);
  const save = useMutation({
    mutationFn: () =>
      api.put("/api/prices", {
        body: {
          provider: provider.trim(),
          model: model.trim(),
          unit_type: unit,
          usd_per_unit: Number(usd),
        },
      }),
    onSuccess: () => onSaved(),
    onError: () => setConfirming(false),
  });
  const fe = fieldErrorsOf(save.error);
  const n = Number(usd);
  const usdBad = usd.trim() === "" || !Number.isFinite(n) || n < 0 || n > 10000;
  const existing = prices.find(
    (p) => p.provider === provider.trim() && p.model === model.trim() && p.unit_type === unit,
  );
  const ready = provider.trim() !== "" && model.trim() !== "" && !usdBad;
  function submit(e: FormEvent) {
    e.preventDefault();
    if (!ready) return;
    if (existing && !confirming) {
      setConfirming(true);
      return;
    }
    save.mutate();
  }
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("costs.prices.dialogTitle")}
      description={t("costs.prices.dialogDescription")}
      footer={
        <Button
          variant="primary"
          type="submit"
          form="price-form"
          disabled={!ready || save.isPending}
        >
          {confirming ? t("costs.prices.confirmOverride") : t("costs.prices.save")}
        </Button>
      }
    >
      <form id="price-form" className="form-stack" onSubmit={submit} noValidate>
        <TextField
          label={t("costs.prices.provider")}
          value={provider}
          maxLength={200}
          onChange={(e) => {
            setProvider(e.target.value);
            setConfirming(false);
          }}
          error={fe.provider}
        />
        <TextField
          label={t("costs.prices.model")}
          value={model}
          maxLength={200}
          onChange={(e) => {
            setModel(e.target.value);
            setConfirming(false);
          }}
          error={fe.model}
        />
        <SelectField
          label={t("costs.prices.unit")}
          value={unit}
          options={UNIT_TYPES.map((u) => ({ value: u, label: t(`costs.unitType.${u}`) }))}
          onChange={(e) => {
            setUnit(e.target.value);
            setConfirming(false);
          }}
          error={fe.unit_type}
        />
        <TextField
          label={t("costs.prices.priceUsd")}
          type="number"
          min={0}
          max={10000}
          value={usd}
          onChange={(e) => {
            setUsd(e.target.value);
            setConfirming(false);
          }}
          error={
            fe.usd_per_unit ??
            (usd.trim() !== "" && usdBad ? t("costs.prices.priceRule") : undefined)
          }
        />
        {confirming && existing && (
          <p role="alert">
            {t("costs.prices.overrideWarning", {
              source: t(`costs.prices.sourceName.${existing.source}`, {
                defaultValue: existing.source,
              }),
              old: formatUnitPrice(existing.usd_per_unit),
              new: formatUnitPrice(n),
            })}
          </p>
        )}
        {save.error != null && Object.keys(fe).length === 0 && <ErrorCallout error={save.error} />}
      </form>
    </Dialog>
  );
}
