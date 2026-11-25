// 成本（docs/ui/pages.md §11；COST-R13～R16）：頂部一行本月花費與預算、依分組的長條圖（附同資料的表格）、
// 明細表格（cursor 分頁）與 CSV 匯出、計價表。分組與日期範圍存在 URL。這個頁面是 lazy route。
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { describeError } from "../../api/describeError";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import {
  Button,
  ErrorCallout,
  SelectField,
  Spinner,
  TableScroll,
  TextField,
  useToast,
} from "../../components";
import { formatDateTime, formatNumber, formatUsd } from "../../i18n";
import { usePageTitle } from "../../shell/usePageTitle";
import { formatUsdCents } from "./money";
import { PricesPanel } from "./PricesPanel";
import {
  GROUP_BY,
  MAX_RANGE_DAYS,
  daysInclusive,
  validateCostsSearch,
  type CostsSearch,
} from "./search";

type Summary = components["schemas"]["Summary"];
type Entry = components["schemas"]["Entry"];

const route = getRouteApi("/app/costs");
const ENTRY_LIMIT = 100;
/** 設定頁的「預算」分區（COST-R16） */
const BUDGET_SECTION = "budget";

export function CostsPage() {
  const { t } = useTranslation();
  usePageTitle(t("pages.costs.title"));
  const raw = route.useSearch();
  const search = validateCostsSearch(raw as Record<string, unknown>);
  const navigate = route.useNavigate();
  const groupBy = search.group_by ?? "day";
  const range = { from: search.from ?? "", to: search.to ?? "" };
  const q: Record<string, string> = { group_by: groupBy, ...range };

  const summary = useQuery({
    queryKey: queryKeys.costs.summary(q),
    queryFn: ({ signal }) =>
      api.get("/api/costs", {
        query: {
          group_by: groupBy,
          from: range.from || undefined,
          to: range.to || undefined,
        },
        signal,
      }),
    placeholderData: (prev) => prev,
  });
  const s = summary.data;

  const setSearch = (patch: Partial<CostsSearch>) =>
    void navigate({
      search: (prev: CostsSearch) => validateCostsSearch({ ...prev, ...patch }),
      replace: true,
    });

  return (
    <>
      <h1 className="page__title">{t("pages.costs.title")}</h1>
      {summary.isPending && <Spinner label={t("common.loading")} showLabel />}
      {summary.error != null && (
        <ErrorCallout
          error={summary.error}
          actions={[{ label: t("common.retry"), onClick: () => void summary.refetch() }]}
        />
      )}
      {s && <Headline s={s} />}
      {s && <Notices s={s} />}
      {s && (
        <RangeControls
          key={`${s.from}-${s.to}`}
          from={s.from}
          to={s.to}
          groupBy={groupBy}
          onApply={(from, to) => setSearch({ from, to })}
          onGroup={(g) => setSearch({ group_by: g as CostsSearch["group_by"] })}
        />
      )}
      {s && <GroupChart s={s} groupBy={groupBy} />}
      {s && <EntriesTable from={s.from} to={s.to} />}
      <PricesPanel />
    </>
  );
}

function Headline({ s }: { s: Summary }) {
  const { t } = useTranslation();
  const b = s.budget;
  return (
    <>
      <p className="cost-summary">
        {b.monthly_usd != null
          ? t("costs.headline", {
              spent: formatUsdCents(b.spent_usd),
              budget: formatUsdCents(b.monthly_usd),
              projected: formatUsdCents(b.projected_usd),
            })
          : t("costs.headlineNoBudget", {
              spent: formatUsdCents(b.spent_usd),
              projected: formatUsdCents(b.projected_usd),
            })}{" "}
        <Link to="/settings/$section" params={{ section: BUDGET_SECTION }}>
          {t("costs.adjustBudget")}
        </Link>
      </p>
      {b.reserved_usd > 0 && (
        <p className="secondary">
          {t("costs.reserved", { amount: formatUsdCents(b.reserved_usd) })}
        </p>
      )}
    </>
  );
}

function Notices({ s }: { s: Summary }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useMutation({
    mutationFn: () => api.post("/api/prices/confirm"),
    onSuccess: async () => {
      toast.show({ title: t("costs.prices.confirmed") });
      await qc.invalidateQueries({ queryKey: queryKeys.prices });
      await qc.invalidateQueries({ queryKey: queryKeys.costs.all });
    },
    onError: (err) => toast.show({ title: describeError(t, err) }),
  });
  const toPrices = () => document.getElementById("prices")?.scrollIntoView();
  return (
    <>
      {(s.notices ?? []).map((n) => (
        <ErrorCallout
          key={n.code}
          error={null}
          live={false}
          category="needs_action"
          message={n.message}
          // 依 code 對應動作；認不得的 code 只顯示訊息（不比對 actions 的文字）
          actions={
            n.code === "prices_stale"
              ? [
                  {
                    label: t("costs.prices.confirm"),
                    primary: true,
                    onClick: () => confirm.mutate(),
                  },
                  { label: t("costs.prices.adjust"), onClick: toPrices },
                ]
              : []
          }
        />
      ))}
      {s.unknown_calls > 0 && (
        <p className="secondary">
          {t("costs.unknownCalls", { count: s.unknown_calls })}{" "}
          <a href="#prices">{t("costs.prices.adjust")}</a>
        </p>
      )}
    </>
  );
}

function RangeControls({
  from,
  to,
  groupBy,
  onApply,
  onGroup,
}: {
  from: string;
  to: string;
  groupBy: string;
  onApply: (from: string, to: string) => void;
  onGroup: (g: string) => void;
}) {
  const { t } = useTranslation();
  const [f, setF] = useState(from);
  const [tt, setT] = useState(to);
  const days = daysInclusive(f, tt);
  const problem = !(days >= 1)
    ? t("costs.rangeInvalid")
    : days > MAX_RANGE_DAYS
      ? t("costs.rangeTooLong", { days: MAX_RANGE_DAYS })
      : undefined;
  return (
    <form
      className="cost-controls"
      onSubmit={(e) => {
        e.preventDefault();
        if (!problem) onApply(f, tt);
      }}
    >
      <TextField
        label={t("costs.from")}
        type="date"
        value={f}
        onChange={(e) => setF(e.target.value)}
        error={problem}
      />
      <TextField
        label={t("costs.to")}
        type="date"
        value={tt}
        onChange={(e) => setT(e.target.value)}
      />
      <Button type="submit" disabled={!!problem || (f === from && tt === to)}>
        {t("costs.apply")}
      </Button>
      <SelectField
        label={t("costs.groupBy")}
        value={groupBy}
        options={GROUP_BY.map((g) => ({ value: g, label: t(`costs.group.${g}`) }))}
        onChange={(e) => onGroup(e.target.value)}
      />
      {problem ? (
        <span className="secondary">{t("costs.exportDisabled")}</span>
      ) : (
        <a
          className="button button--outline"
          href={`/api/costs/export.csv?${new URLSearchParams({ from, to }).toString()}`}
          download
        >
          {t("costs.export")}
        </a>
      )}
    </form>
  );
}

/** 長條圖：每列一個分組，長條只是視覺輔助（aria-hidden），金額與次數以文字顯示在同一列。 */
function GroupChart({ s, groupBy }: { s: Summary; groupBy: string }) {
  const { t } = useTranslation();
  const groups = s.groups ?? [];
  const max = Math.max(0, ...groups.map((g) => g.usd + g.reserved_usd));
  return (
    <section className="tune-section" aria-labelledby="cost-chart-title">
      <h2 id="cost-chart-title" className="tune-section__title">
        {t("costs.chartTitle", {
          from: s.from,
          to: s.to,
          timezone: s.timezone,
          total: formatUsdCents(s.total_usd),
        })}
      </h2>
      {groups.length === 0 ? (
        <p className="secondary">{t("costs.emptyRange")}</p>
      ) : (
        <TableScroll label={t("costs.chartCaption", { group: t(`costs.group.${groupBy}`) })}>
          <table className="data-table">
            <caption className="visually-hidden">
              {t("costs.chartCaption", { group: t(`costs.group.${groupBy}`) })}
            </caption>
            <thead>
              <tr>
                <th scope="col">{t(`costs.group.${groupBy}`)}</th>
                <th scope="col" className="cost-bar-cell">
                  <span className="visually-hidden">{t("costs.bar")}</span>
                </th>
                <th scope="col" className="data-table__amount">
                  {t("costs.amount")}
                </th>
                <th scope="col" className="data-table__amount">
                  {t("costs.calls")}
                </th>
              </tr>
            </thead>
            <tbody>
              {groups.map((g) => (
                <tr key={g.key}>
                  <th scope="row">{groupLabel(t, groupBy, g.key, g.label)}</th>
                  <td className="cost-bar-cell" aria-hidden="true">
                    <span className="cost-bar-track">
                      <span
                        className="cost-bar"
                        style={{ width: `${max > 0 ? (g.usd / max) * 100 : 0}%` }}
                      />
                      {g.reserved_usd > 0 && (
                        <span
                          className="cost-bar cost-bar--reserved"
                          style={{ width: `${(g.reserved_usd / max) * 100}%` }}
                        />
                      )}
                    </span>
                  </td>
                  <td className="num data-table__amount">
                    {formatUsd(g.usd)}
                    {g.reserved_usd > 0 &&
                      t("costs.plusReserved", { amount: formatUsd(g.reserved_usd) })}
                  </td>
                  <td className="num data-table__amount">{formatNumber(g.calls)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
    </section>
  );
}

function groupLabel(
  t: ReturnType<typeof useTranslation>["t"],
  groupBy: string,
  key: string,
  label: string,
) {
  if (groupBy === "stage") return t(`stage.${key}`, { defaultValue: label || key });
  return label || key || t("costs.none");
}

function EntriesTable({ from, to }: { from: string; to: string }) {
  const { t } = useTranslation();
  const entries = useInfiniteQuery({
    queryKey: queryKeys.costs.entries({ from, to }),
    queryFn: ({ pageParam, signal }) =>
      api.get("/api/costs/entries", {
        query: { from, to, limit: String(ENTRY_LIMIT), cursor: pageParam || undefined },
        signal,
      }),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor || undefined,
  });
  const list = entries.data?.pages.flatMap((p) => p.entries ?? []) ?? [];
  return (
    <section className="tune-section" aria-labelledby="cost-entries-title">
      <h2 id="cost-entries-title" className="tune-section__title">
        {t("costs.entries.title")}
      </h2>
      {entries.isPending && <Spinner label={t("common.loading")} showLabel />}
      {entries.error != null && (
        <ErrorCallout
          error={entries.error}
          actions={[{ label: t("common.retry"), onClick: () => void entries.refetch() }]}
        />
      )}
      {entries.data && list.length === 0 && <p className="secondary">{t("costs.emptyRange")}</p>}
      {list.length > 0 && (
        <TableScroll label={t("costs.entries.title")}>
          <table className="data-table">
            <caption className="visually-hidden">{t("costs.entries.title")}</caption>
            <thead>
              <tr>
                <th scope="col">{t("costs.entries.at")}</th>
                <th scope="col">{t("costs.entries.item")}</th>
                <th scope="col">{t("costs.entries.stage")}</th>
                <th scope="col">{t("costs.entries.provider")}</th>
                <th scope="col">{t("costs.entries.model")}</th>
                <th scope="col">{t("costs.entries.units")}</th>
                <th scope="col" className="data-table__amount">
                  {t("costs.amount")}
                </th>
              </tr>
            </thead>
            <tbody>
              {list.map((e, i) => (
                <EntryRow key={`${e.at}-${e.item_id}-${e.attempt}-${i}`} e={e} />
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
      {entries.hasNextPage && (
        <Button onClick={() => void entries.fetchNextPage()} disabled={entries.isFetchingNextPage}>
          {entries.isFetchingNextPage ? t("common.loadingMore") : t("common.loadMore")}
        </Button>
      )}
    </section>
  );
}

function EntryRow({ e }: { e: Entry }) {
  const { t } = useTranslation();
  const title = e.item_title || e.ref || e.item_id;
  return (
    <tr>
      <td className="num">{formatDateTime(e.at)}</td>
      <td>
        {e.item_id && e.ref ? (
          <Link to="/items/$id" params={{ id: e.item_id }}>
            {title}
          </Link>
        ) : (
          title || t("costs.none")
        )}
      </td>
      <td>{t(`stage.${e.stage}`, { defaultValue: e.stage })}</td>
      <td>{e.provider}</td>
      <td>{e.model}</td>
      <td className="num">
        {t(`costs.units.${e.unit_type}`, {
          n: formatNumber(e.units),
          defaultValue: `${formatNumber(e.units)} ${e.unit_type}`,
        })}
      </td>
      <td className="num data-table__amount">
        {e.state === "unknown"
          ? t("costs.unknownPrice")
          : e.state === "reserved"
            ? t("costs.reservedAmount", { amount: formatUsd(e.usd) })
            : formatUsd(e.usd)}
        {e.outcome !== "ok" && (
          <span className="secondary">
            {" "}
            {t(`costs.outcome.${e.outcome}`, { defaultValue: e.outcome })}
          </span>
        )}
      </td>
    </tr>
  );
}
