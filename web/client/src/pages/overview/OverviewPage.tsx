// 總覽（docs/ui/pages.md §1、LIB-R21～R23）：現在在處理什麼、有什麼需要我處理、今天發生了什麼。lazy route。
//
//   - GET /api/overview?tz=<瀏覽器時區>（L-M8）；時區不被接受（422）時不帶 tz 再讀一次（伺服器用預算時區）。
//   - 即時更新：步驟與影音的 SSE 事件以 1 秒節流讓總覽失效；今天、本月、磁碟有些變化沒有事件，另外每分鐘重讀。
//   - 系統告警與沒有送出的通知見 AlertsSection（讀 /api/system 與 /api/notifications，OPS-R13、NOTIFY-R8）。
//   - 「需要你處理」的 actions 是後端的繁中按鈕文字：成本的兩個動作在這裡對應，其他沿用影音詳情的對照表。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";

import { StatusBand, type StatusBandStage } from "@shared";

import { useAddDrawer } from "../../add/AddContext";
import { api, apiPath } from "../../api/client";
import { describeError } from "../../api/describeError";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { EmptyState, ErrorCallout, Spinner, useToast, type ErrorAction } from "../../components";
import { useServerEvent, useStreamReconnect } from "../../events/useEventStream";
import { formatBytes, formatDateTime, formatNumber } from "../../i18n";
import { usePageTitle } from "../../shell/usePageTitle";
import { formatUsdCents } from "../costs/money";
import { mapActions } from "../item/actions";
import { useItemDetail } from "../library/LibraryRow";
import { BAND_STAGES, bandStatus, badgeStatus } from "../library/stageView";
import { AlertsSection } from "./AlertsSection";
import "../../styles/ui7.css";

type Overview = components["schemas"]["OverviewResult"];
type NeedItem = components["schemas"]["OverviewNeedItem"];
type NowItem = components["schemas"]["OverviewNowItem"];

/** 瀏覽器的 IANA 時區；取不到時讓伺服器用預算時區 */
export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone ?? "";
  } catch {
    return "";
  }
}

async function fetchOverview(tz: string, signal: AbortSignal): Promise<Overview> {
  try {
    return await api.get("/api/overview", { query: tz ? { tz } : {}, signal });
  } catch (err) {
    if (tz && isApiError(err) && err.status === 422) return api.get("/api/overview", { signal });
    throw err;
  }
}

export function OverviewPage() {
  const { t } = useTranslation();
  usePageTitle(t("overview.title"));
  const qc = useQueryClient();
  const tz = browserTimeZone();
  const key = queryKeys.overview(tz);
  const q = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => fetchOverview(tz, signal),
    refetchInterval: 60_000,
  });

  // 事件很密（每個任務的狀態都會送）：1 秒內的變化合併成一次重讀
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const refresh = () => {
    if (timer.current) return;
    timer.current = setTimeout(() => {
      timer.current = undefined;
      void qc.invalidateQueries({ queryKey: queryKeys.overviewAll });
    }, 1000);
  };
  useEffect(() => () => clearTimeout(timer.current), []);
  useServerEvent(refresh, [
    "stage.status",
    "item.created",
    "item.deleted",
    "item.restored",
    "item.purged",
  ]);
  useStreamReconnect(refresh);

  // 回應的形狀不對（例如代理回了別的 JSON）時當成錯誤，不讓整頁崩潰
  const o = q.data?.now && q.data.needs_you && q.data.month && q.data.disk ? q.data : undefined;
  const blank =
    !!o &&
    o.now.total === 0 &&
    o.needs_you.total === 0 &&
    !o.today?.length &&
    !o.recent_done?.length;

  return (
    <div className="overview">
      <div className="page__head">
        <h1 className="page__title">{t("overview.title")}</h1>
      </div>
      {q.isPending ? (
        <Spinner label={t("common.loading")} showLabel />
      ) : !o ? (
        <ErrorCallout
          error={q.error ?? null}
          actions={[{ label: t("common.retry"), onClick: () => void q.refetch() }]}
        />
      ) : (
        <>
          {blank && <EmptyLibrary />}
          <Notices notices={o.notices ?? []} />
          <AlertsSection />
          <NowSection now={o.now} />
          <NeedsSection needs={o.needs_you} />
          <TodaySection o={o} />
          <FootLine o={o} />
        </>
      )}
    </div>
  );
}

/** 各區塊都沒有東西時，看媒體庫是不是真的空的（只有等待中的影音時各區塊也是空的） */
function EmptyLibrary() {
  const { t } = useTranslation();
  const add = useAddDrawer();
  const q = useQuery({
    queryKey: [...queryKeys.overviewAll, "any-item"],
    queryFn: ({ signal }) => api.get("/api/items", { query: { limit: 1 }, signal }),
  });
  if (q.data?.total !== 0) return null;
  return (
    <EmptyState
      message={t("library.empty")}
      action={{ label: t("add.open"), onClick: () => add.open() }}
    />
  );
}

function Notices({ notices }: { notices: components["schemas"]["Notice"][] }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  // 依 code 對應動作（不比對 actions 的文字）；認不得的 code 只顯示訊息
  const actionsOf = (code: string): ErrorAction[] => {
    if (code === "terms_pending")
      return [
        {
          label: t("overview.review"),
          primary: true,
          onClick: () =>
            void navigate({ to: "/settings/$section", params: { section: "glossary" } }),
        },
      ];
    if (code === "prices_stale")
      return [{ label: t("costs.prices.adjust"), onClick: () => void navigate({ to: "/costs" }) }];
    return [];
  };
  return notices.map((n) => (
    <ErrorCallout
      key={n.code}
      error={null}
      live={false}
      category="needs_action"
      message={n.message}
      actions={actionsOf(n.code)}
    />
  ));
}

function NowSection({ now }: { now: Overview["now"] }) {
  const { t } = useTranslation();
  return (
    <section className="ov-sec" aria-labelledby="ov-now">
      <div className="ov-sec__head">
        <h2 id="ov-now">{t("overview.now")}</h2>
        <span className="secondary num">{t("overview.processing", { count: now.total })}</span>
      </div>
      {now.total === 0 ? (
        <p className="secondary">{t("overview.idle")}</p>
      ) : (
        <ul className="ov-list">
          {(now.items ?? []).map((it) => (
            <NowRow key={it.id} it={it} />
          ))}
        </ul>
      )}
      {now.more > 0 && (
        <p>
          <Link to="/queue">{t("overview.more", { count: now.more })}</Link>
        </p>
      )}
    </section>
  );
}

function NowRow({ it }: { it: NowItem }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const detail = useItemDetail(it.id);
  const d = detail.data;
  const stageName = (s: string) => t(`stage.${s}`, { defaultValue: s });
  const byStage = new Map((d?.stages ?? []).map((s) => [s.stage, s]));
  const bands: StatusBandStage[] = BAND_STAGES.map((k) => {
    const run = byStage.get(k);
    return {
      key: k,
      status: bandStatus(run),
      label: t("library.stageStatus", {
        stage: stageName(k),
        status: t(`status.${run ? badgeStatus(run) : "pending"}`),
      }),
    };
  });
  const p = it.progress;
  return (
    <li className="ov-now">
      <Link to="/items/$id" params={{ id: it.id }} className="ov-now__title">
        {it.title || it.ref}
      </Link>
      <span className="ov-now__stage num">
        {stageName(it.stage)}
        {p.total > 0 && ` ${formatNumber(p.done)}/${formatNumber(p.total)}`}
      </span>
      {d && (
        <StatusBand
          className="ov-now__band"
          stages={bands}
          label={bands.map((b) => b.label).join(t("common.listSeparator"))}
          durationS={d.duration_s}
          // pages.md §1：點時間帶打開詳情並定位到該時間
          onSeek={(s) =>
            void navigate({ to: "/items/$id", params: { id: it.id }, search: { t: Math.floor(s) } })
          }
          seekLabel={t("library.band.seek", { title: it.title || it.ref })}
        />
      )}
    </li>
  );
}

function NeedsSection({ needs }: { needs: Overview["needs_you"] }) {
  const { t } = useTranslation();
  // 沒有項目時整個區塊隱藏（pages.md §1）
  if (needs.total === 0) return null;
  return (
    <section className="ov-sec" aria-labelledby="ov-needs">
      <div className="ov-sec__head">
        <h2 id="ov-needs">{t("status.needs_action")}</h2>
        <span className="secondary num">
          {t("overview.count", { count: needs.total, formatted: formatNumber(needs.total) })}
        </span>
      </div>
      <ul className="ov-list">
        {(needs.items ?? []).map((n) => (
          <NeedRow key={n.id} n={n} />
        ))}
      </ul>
      {needs.total > (needs.items?.length ?? 0) && (
        <p>
          <Link to="/library">{t("overview.allInLibrary")}</Link>
        </p>
      )}
    </section>
  );
}

function NeedRow({ n }: { n: NeedItem }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const stageName = t(`stage.${n.stage}`, { defaultValue: n.stage });
  const done = (title: string) => {
    toast.show({ title });
    void qc.invalidateQueries({ queryKey: queryKeys.overviewAll });
    void qc.invalidateQueries({ queryKey: queryKeys.items.detail(n.id) });
  };
  const rerun = useMutation({
    mutationFn: (from: string) =>
      api.post<"/api/items/{id}/rerun">(apiPath("/api/items/{id}/rerun", { id: n.id }), {
        body: { from },
      }),
    onSuccess: (_, from) =>
      done(t("library.rerunQueued", { stage: t(`stage.${from}`, { defaultValue: from }) })),
    onError: (err) => toast.show({ title: describeError(t, err) }),
  });
  const cont = useMutation({
    mutationFn: () => api.post("/api/items/continue", { body: { ids: [n.id] } }),
    onSuccess: () => done(t("overview.continued")),
    onError: (err) => toast.show({ title: describeError(t, err) }),
  });
  const openItem = () =>
    void navigate({ to: "/items/$id", params: { id: n.id }, search: { tab: "stages" } });
  const settings = (section: string) =>
    void navigate({ to: "/settings/$section", params: { section } });

  const actions: ErrorAction[] = [];
  for (const raw of n.actions ?? []) {
    // 成本的動作（internal/cost/service.go 的 ActionAdjustBudget、ActionViewCosts）
    if (raw === "調整預算") {
      actions.push({ label: t("overview.adjustBudget"), onClick: () => settings("budget") });
      continue;
    }
    if (raw === "查看成本") {
      actions.push({
        label: t("overview.viewCosts"),
        onClick: () => void navigate({ to: "/costs" }),
      });
      continue;
    }
    const [a] = mapActions([raw]);
    if (!a) continue;
    const label = a.key ? t(`actions.${a.key}`) : a.raw;
    const k = a.action;
    const onClick =
      k.kind === "rerun"
        ? () => rerun.mutate(k.from ?? n.stage)
        : k.kind === "continue"
          ? () => cont.mutate()
          : k.kind === "settings"
            ? () => settings(k.section)
            : k.kind === "uploadCookie"
              ? () => settings("accounts")
              : openItem; // 取消、改用 API、更新 yt-dlp 等需要更多說明的動作在影音詳情做
    actions.push({ label, onClick });
  }
  actions.push({ label: t("overview.view"), onClick: openItem });
  if (actions[0]) actions[0].primary = true;

  const reason = n.reason
    ? t(`errorClass.${n.reason}`, {
        defaultValue: t(`status.${n.reason}`, { defaultValue: n.reason }),
      })
    : t(`status.${n.status}`);
  return (
    <li>
      <p className="ov-need__title">
        <Link to="/items/$id" params={{ id: n.id }}>
          {n.title || n.ref}
        </Link>
      </p>
      <ErrorCallout
        error={null}
        live={false}
        category={n.reason === "budget_blocked" ? "wait" : "needs_action"}
        message={
          n.message
            ? t("library.attentionMessage", { stage: stageName, reason, message: n.message })
            : t("library.attentionNoMessage", { stage: stageName, reason })
        }
        actions={actions}
      />
    </li>
  );
}

const TIME = new Map<string, Intl.DateTimeFormat>();
function hhmm(at: string, lang: string) {
  let f = TIME.get(lang);
  if (!f) {
    f = new Intl.DateTimeFormat(lang, { hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
    TIME.set(lang, f);
  }
  return f.format(new Date(at));
}

function TodaySection({ o }: { o: Overview }) {
  const { t, i18n } = useTranslation();
  const today = o.today ?? [];
  const done = o.recent_done ?? [];
  return (
    <>
      <section className="ov-sec" aria-labelledby="ov-today">
        <div className="ov-sec__head">
          <h2 id="ov-today">{t("overview.today")}</h2>
        </div>
        {today.length === 0 ? (
          <p className="secondary">{t("overview.quiet")}</p>
        ) : (
          <ol className="ov-events">
            {today.map((e, i) => (
              <li key={`${e.at}-${i}`}>
                <time className="num secondary" dateTime={e.at}>
                  {hhmm(e.at, i18n.language)}
                </time>
                <span>
                  {t(`overview.ev.${e.type}`, {
                    title: e.title || e.item || "",
                    defaultValue: t("overview.ev.other", {
                      type: e.type,
                      title: e.title || e.item || "",
                    }),
                  })}
                </span>
              </li>
            ))}
          </ol>
        )}
      </section>
      {done.length > 0 && (
        <section className="ov-sec" aria-labelledby="ov-done">
          <div className="ov-sec__head">
            <h2 id="ov-done">{t("overview.recentDone")}</h2>
          </div>
          <ul className="ov-events">
            {done.map((d) => (
              <li key={d.id}>
                <time className="num secondary" dateTime={d.done_at}>
                  {formatDateTime(d.done_at)}
                </time>
                <Link to="/items/$id" params={{ id: d.id }}>
                  {d.title || d.ref}
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  );
}

function FootLine({ o }: { o: Overview }) {
  const { t } = useTranslation();
  const m = o.month;
  const disk = o.disk;
  return (
    <footer className="ov-foot">
      <p>
        <span className="num">
          {m.monthly_usd == null
            ? t("overview.spentNoBudget", { spent: formatUsdCents(m.spent_usd) })
            : t("overview.spent", {
                spent: formatUsdCents(m.spent_usd),
                budget: formatUsdCents(m.monthly_usd),
              })}
        </span>{" "}
        <Link to="/costs">{t("overview.viewCosts")}</Link>
      </p>
      <p className="secondary num">
        {disk.data_bytes == null
          ? t("overview.diskUnknown")
          : disk.free_bytes == null
            ? t("overview.diskUsed", { used: formatBytes(disk.data_bytes) })
            : t("overview.disk", {
                used: formatBytes(disk.data_bytes),
                free: formatBytes(disk.free_bytes),
              })}
      </p>
    </footer>
  );
}
