// 影音詳情（docs/ui/pages.md §4；LIB-R5～R7、R11、R12；PIPE-R7、R13、R15；OBS-2）。
//
//   標題列（標題、頻道、平台、長度、語言）與主要動作（重新處理、下載、停止處理、刪除）
//   完整版時間帶（StatusBand variant="full"）：與播放器同步的播放頭，點擊、方向鍵移動
//   播放器（YouTube／Bilibili 嵌入、本機／Podcast 的 HTML5 media）＋ 分頁：字幕、步驟、紀錄、輸出
//
// 路由參數可能是影音代號（yt:…）：detail 讀到之後，其他 API 與 query key 一律用 detail.id，SSE 的失效表才對得上。
// 這個頁面用 lazy route 載入（router.tsx），不算進初始 bundle。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { useCallback, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";

import { StatusBand, type StatusBandStage } from "@shared";

import { api, apiPath } from "../../api/client";
import { describeError } from "../../api/describeError";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, EmptyState, ErrorCallout, Spinner, useToast } from "../../components";
import { useServerEvent } from "../../events/useEventStream";
import { formatDate, formatTimecode } from "../../i18n";
import { usePageTitle } from "../../shell/usePageTitle";
import { BAND_STAGES, badgeStatus, bandStatus, type ApiItemDetail } from "../library/stageView";
import type { ActionKind } from "./actions";
import { cueAt } from "./cues";
import { CueList } from "./CueList";
import {
  availableModes,
  defaultMode,
  latestConfig,
  modeVariant,
  targetLangs,
  useCues,
  useOutputs,
  useRuns,
} from "./data";
import { DeleteDialog } from "./DeleteDialog";
import { FramesPanel, NotePanel } from "./NotePanels";
import { OutputsPanel, downloadUrl } from "./OutputsPanel";
import { Player, type PlayerHandle } from "./Player";
import { RerunDialog } from "./RerunDialog";
import { RunsPanel } from "./RunsPanel";
import {
  ITEM_TABS,
  validateItemSearch,
  type CueMode,
  type ItemSearch,
  type ItemTab,
} from "./search";
import { CancelDialog, CookieDialog } from "./SmallDialogs";
import { StagePanel, orderedStages } from "./StagePanel";
import { SummaryBlock } from "./SummaryBlock";
import "../../styles/ui5.css";

const route = getRouteApi("/app/items/$id");

const ACTIVE = new Set(["pending", "running", "waiting", "awaiting_agent"]);

function byStageStatus(stages: readonly { stage: string; status: string }[], stage: string) {
  return stages.find((s) => s.stage === stage)?.status;
}

export function ItemPage() {
  const { t } = useTranslation();
  const { id: param } = route.useParams();
  const rawSearch = route.useSearch();
  const search = useMemo(
    () => validateItemSearch(rawSearch as Record<string, unknown>),
    [rawSearch],
  );
  const navigate = route.useNavigate();
  const qc = useQueryClient();
  const toast = useToast();

  const detail = useQuery({
    queryKey: queryKeys.items.detail(param),
    queryFn: ({ signal }) =>
      api.get<"/api/items/{id}">(apiPath("/api/items/{id}", { id: param }), { signal }),
    staleTime: 10_000,
  });
  const d = detail.data;
  usePageTitle(d?.title || t("pages.item.title"));

  // 用代號進來時，SSE 只讓 detail(<ID>) 失效：這裡另外讓路由參數的 key 失效
  useServerEvent(
    (ev) => {
      if (!d || param === d.id) return;
      if (ev.item_id === d.id || ev.item === d.ref)
        void qc.invalidateQueries({ queryKey: queryKeys.items.detail(param) });
    },
    ["stage.status", "item.deleted", "item.restored"],
  );

  const backToLibrary = () => void navigate({ to: "/library" });
  const restore = async (itemId: string) => {
    try {
      await api.post(apiPath("/api/trash/{id}/restore", { id: itemId }));
      void qc.invalidateQueries({ queryKey: queryKeys.items.lists() });
      void qc.invalidateQueries({ queryKey: queryKeys.trash });
      await detail.refetch();
    } catch (err) {
      toast.show({ title: describeError(t, err) });
    }
  };
  if (detail.isPending) {
    return (
      <>
        <h1 className="page__title">{t("pages.item.title")}</h1>
        <Spinner label={t("item.loading")} showLabel />
      </>
    );
  }
  if (!d) {
    const err = detail.error;
    const inTrash = isApiError(err) && err.code === "in_trash";
    const notFound = isApiError(err) && err.status === 404 && !inTrash;
    return (
      <>
        <h1 className="page__title">{t("pages.item.title")}</h1>
        {notFound ? (
          <EmptyState
            message={t("item.notFound")}
            action={{
              label: t("pages.notFound.back"),
              onClick: backToLibrary,
            }}
          />
        ) : (
          <ErrorCallout
            error={err}
            actions={
              inTrash
                ? [
                    {
                      label: t("item.restore"),
                      primary: true,
                      onClick: () => void restore(err.body.item_id || param),
                    },
                  ]
                : [{ label: t("common.retry"), onClick: () => void detail.refetch() }]
            }
          />
        )}
      </>
    );
  }
  return (
    <ItemView
      d={d}
      search={search}
      setSearch={(patch, replace) =>
        void navigate({
          search: (prev: ItemSearch) => validateItemSearch({ ...prev, ...patch }),
          replace,
        })
      }
    />
  );
}

function ItemView({
  d,
  search,
  setSearch,
}: {
  d: ApiItemDetail;
  search: ItemSearch;
  setSearch: (patch: Partial<Record<keyof ItemSearch, unknown>>, replace?: boolean) => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const id = d.id;
  const title = d.title || t("library.untitled");
  const stages = useMemo(() => orderedStages(d.stages ?? []), [d.stages]);
  const runs = useRuns(id);
  const outputs = useOutputs(id);
  const edited = useCues(id, "edited");
  const hasEdited = !!edited.data?.cues.length;
  const modes = useMemo(() => availableModes(stages, hasEdited), [stages, hasEdited]);
  // 沒有指定模式時，第一次決定的預設就固定下來：之後步驟狀態改變（SSE）不會換掉使用者正在看的模式
  const computed = defaultMode(modes, stages);
  const [initialMode, setInitialMode] = useState<CueMode | undefined>(computed);
  if (initialMode === undefined && computed) setInitialMode(computed);
  const mode: CueMode | undefined =
    search.mode && modes.includes(search.mode)
      ? search.mode
      : initialMode && modes.includes(initialMode)
        ? initialMode
        : computed;
  const correctDone = byStageStatus(stages, "correct") === "done";
  const tab: ItemTab = search.tab ?? "subtitles";
  const targets = targetLangs(runs.data?.runs ?? undefined);
  const originalVariant = modes.includes("corrected")
    ? "corrected"
    : modes.includes("normalized")
      ? "normalized"
      : "source";

  // ---- 播放器與播放頭 ----
  const player = useRef<PlayerHandle>(null);
  const [time, setTime] = useState(search.t ?? 0);
  const seek = useCallback((s: number) => {
    setTime(s);
    player.current?.seek(s);
  }, []);

  // hover 時間帶時顯示那個時間點的字幕句（和字幕分頁共用同一份快取）
  const hoverCues = useCues(id, mode ? modeVariant(mode) : undefined);
  const describeTime = useCallback(
    (s: number) => {
      const cs = hoverCues.data?.cues ?? [];
      const i = cueAt(cs, s);
      return i >= 0 && s < cs[i]!.end ? cs[i]!.text : "";
    },
    [hoverCues.data],
  );

  const byStage = new Map(stages.map((s) => [s.stage, s]));
  const stageName = (s: string) => t(`stage.${s}`, { defaultValue: s });
  const bands: StatusBandStage[] = BAND_STAGES.map((key) => {
    const run = byStage.get(key);
    return {
      key,
      status: bandStatus(run),
      label: t("library.stageStatus", {
        stage: stageName(key),
        status: t(`status.${run ? badgeStatus(run) : "pending"}`),
      }),
    };
  });

  // ---- 宣告（aria-live）：這部影音的步驟完成、失敗、需要處理 ----
  const [announcement, setAnnouncement] = useState("");
  useServerEvent(
    (ev) => {
      if (ev.item_id !== id && ev.item !== d.ref) return;
      const stage = stageName(ev.stage ?? "");
      if (ev.status === "failed") setAnnouncement(t("item.live.failed", { stage }));
      else if (ev.status === "blocked") setAnnouncement(t("item.live.blocked", { stage }));
      else if (ev.status === "done") setAnnouncement(t("item.live.done", { stage }));
    },
    ["stage.status"],
  );

  // ---- 動作 ----
  const [rerunFrom, setRerunFrom] = useState<string | null>(null);
  const [cookieOpen, setCookieOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [cancelOpen, setCancelOpen] = useState(false);
  const switchExecutor = useMutation({
    mutationFn: (stage: string) =>
      api.post<"/api/items/{id}/switch-executor">(
        apiPath("/api/items/{id}/switch-executor", { id }),
        { body: { stage, executor: "api" } },
      ),
    onSuccess: (_r, stage) => {
      void qc.invalidateQueries({ queryKey: queryKeys.items.detail(id) });
      toast.show({ title: t("item.actions.switched", { stage: stageName(stage) }) });
    },
    onError: (err) => toast.show({ title: describeError(t, err) }),
  });
  const updateYtDlp = useMutation({
    mutationFn: () => api.post("/api/tools/yt-dlp/update", { body: {} }),
    onSuccess: (r) =>
      toast.show({
        title: r?.rolled_back
          ? t("item.actions.ytdlpRolledBack", { version: r.version })
          : t("item.actions.ytdlpUpdated", { version: r?.version ?? "" }),
      }),
    onError: (err) => toast.show({ title: describeError(t, err) }),
  });
  // DIG-R7：摘要後暫停的影音繼續處理
  const continueItem = useMutation({
    // 回 202：ResponseOf 只認 200／201，型別自己標
    mutationFn: () =>
      api.post(apiPath("/api/items/{id}/continue", { id })) as Promise<
        components["schemas"]["ContinueOutputBody"] | undefined
      >,
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: queryKeys.items.detail(id) });
      toast.show({ title: t("item.summary.continued", { stage: stageName(r?.continued ?? "") }) });
    },
    onError: (err) => {
      void qc.invalidateQueries({ queryKey: queryKeys.items.detail(id) });
      toast.show({ title: describeError(t, err) });
    },
  });
  const awaitingStage = stages.find(
    (s) => s.status === "blocked" && s.block_reason === "awaiting_user",
  )?.stage;
  const onAction = (stage: string, a: ActionKind) => {
    switch (a.kind) {
      case "rerun":
        setRerunFrom(a.from ?? stage);
        break;
      case "continue":
        continueItem.mutate();
        break;
      case "cancel":
        setCancelOpen(true);
        break;
      case "switchToApi":
        switchExecutor.mutate(stage);
        break;
      case "uploadCookie":
        setCookieOpen(true);
        break;
      case "updateYtDlp":
        updateYtDlp.mutate();
        break;
      case "settings":
        void navigate({ to: "/settings/$section", params: { section: a.section } });
        break;
      case "unknown":
        void navigate({ to: "/settings/$section", params: { section: "services" } });
        break;
    }
  };

  const showRuns = () => setSearch({ tab: "runs" }, true);
  const active = stages.some((s) => ACTIVE.has(s.status));
  const attention = stages.filter((s) => s.status === "failed" || s.status === "blocked").length;
  const profileName = latestConfig(runs.data?.runs ?? undefined)?.profile;
  const platformName = t(`platform.${d.platform}`, { defaultValue: d.platform });
  const external =
    d.platform === "youtube" || d.platform === "bilibili" || d.platform === "podcast";

  return (
    <div className="item">
      <nav className="item__crumbs" aria-label={t("item.breadcrumb")}>
        <Link to="/library">{t("pages.library.title")}</Link>
      </nav>
      <div className="item__head">
        <h1 className="item__title" lang={d.original_lang || undefined}>
          {title}
        </h1>
        <div className="item__actions">
          <Button onClick={() => setRerunFrom("")}>{t("item.actions.rerun")}</Button>
          <a className="button button--outline" href={downloadUrl(id, "srt")} download>
            {t("item.actions.downloadSrt")}
          </a>
          {/* BND-R1：現行版本的字幕、筆記、章節、贊助片段與選中的畫面（API 沒有選擇產出種類的參數） */}
          <a
            className="button button--outline"
            href={apiPath("/api/items/{id}/bundle", { id })}
            download
          >
            {t("item.actions.exportBundle")}
          </a>
          {active && (
            <Button onClick={() => setCancelOpen(true)}>{t("item.actions.cancel")}</Button>
          )}
          <Button variant="danger" onClick={() => setDeleteOpen(true)}>
            {t("item.actions.delete")}
          </Button>
        </div>
      </div>
      <dl className="item__meta">
        {d.channel_id && (
          <div>
            <dt>{t("item.meta.channel")}</dt>
            <dd>{d.channel_id}</dd>
          </div>
        )}
        <div>
          <dt>{t("item.meta.platform")}</dt>
          <dd>
            {external && d.url ? (
              <a href={d.url} target="_blank" rel="noreferrer noopener">
                {t("item.meta.openOn", { platform: platformName })}
              </a>
            ) : (
              platformName
            )}
          </dd>
        </div>
        {d.published_at && (
          <div>
            <dt>{t("item.meta.published")}</dt>
            <dd className="num">{formatDate(d.published_at)}</dd>
          </div>
        )}
        <div>
          <dt>{t("item.meta.duration")}</dt>
          <dd className="num">
            {d.duration_s != null ? formatTimecode(d.duration_s) : t("library.durationUnknown")}
          </dd>
        </div>
        {d.original_lang && (
          <div>
            <dt>{t("item.meta.lang")}</dt>
            <dd>{d.original_lang}</dd>
          </div>
        )}
        {typeof profileName === "string" && profileName && (
          <div>
            <dt>{t("item.meta.profile")}</dt>
            <dd>{profileName}</dd>
          </div>
        )}
        {(d.sources ?? []).find((s) => s.state === "used") && (
          <div>
            <dt>{t("item.meta.source")}</dt>
            <dd>{(d.sources ?? []).find((s) => s.state === "used")!.source}</dd>
          </div>
        )}
      </dl>

      <SummaryBlock
        summary={d.summary}
        awaiting={awaitingStage}
        busy={continueItem.isPending}
        onContinue={() => continueItem.mutate()}
        onCancel={() => setCancelOpen(true)}
      />

      <StatusBand
        variant="full"
        className="item__band"
        stages={bands}
        label={t("item.band.label")}
        durationS={d.duration_s}
        position={time}
        onSeek={seek}
        formatTime={formatTimecode}
        valueText={(tc) => t("item.band.valueText", { time: tc })}
        describeTime={describeTime}
        describeLang={hoverCues.data?.lang}
      />

      <p className="visually-hidden" aria-live="polite">
        {announcement}
      </p>

      <div className="item__body">
        <div className="item__player">
          <Player
            ref={player}
            itemId={id}
            platform={d.platform}
            externalId={d.external_id}
            part={d.part}
            title={title}
            start={search.t}
            onTime={setTime}
          />
        </div>
        <div className="item__tabs">
          <Tabs
            tab={tab}
            onChange={(next) => setSearch({ tab: next }, true)}
            labels={{
              subtitles: t("item.tabs.subtitles"),
              frames: t("item.tabs.frames"),
              note: t("item.tabs.note"),
              stages: attention
                ? t("item.tabs.stagesAttention", { count: attention })
                : t("item.tabs.stages"),
              runs: t("item.tabs.runs"),
              outputs: t("item.tabs.outputs"),
            }}
          />
          <div
            className="item__panel"
            role="tabpanel"
            id={`panel-${tab}`}
            aria-labelledby={`tab-${tab}`}
          >
            {tab === "subtitles" &&
              (mode ? (
                <CueList
                  itemId={id}
                  mode={mode}
                  modes={modes}
                  onModeChange={(m) => setSearch({ mode: m }, true)}
                  originalVariant={originalVariant}
                  targetLangs={targets}
                  correctDone={correctDone}
                  currentTime={time}
                  onSeek={seek}
                />
              ) : (
                <p className="secondary">{t("item.cues.notYet")}</p>
              ))}
            {tab === "frames" && <FramesPanel itemId={id} onSeek={seek} />}
            {tab === "note" && (
              <NotePanel
                itemId={id}
                itemUrl={d.url || undefined}
                onSeek={seek}
                onRegenerate={() => setRerunFrom("note")}
              />
            )}
            {tab === "stages" && (
              <StagePanel
                stages={stages}
                runs={runs.data?.runs ?? undefined}
                handlers={{ onAction, onShowRuns: showRuns }}
              />
            )}
            {tab === "runs" && (
              <RunsPanel
                runs={runs.data?.runs ?? undefined}
                isPending={runs.isPending}
                error={runs.error}
                onRetry={() => void runs.refetch()}
              />
            )}
            {tab === "outputs" && (
              <OutputsPanel
                itemId={id}
                outputs={outputs.data?.outputs ?? undefined}
                isPending={outputs.isPending}
                error={outputs.error}
                onRetry={() => void outputs.refetch()}
                hasTranslation={modes.includes("translated")}
              />
            )}
          </div>
        </div>
      </div>

      <RerunDialog
        open={rerunFrom !== null}
        onOpenChange={(o) => !o && setRerunFrom(null)}
        itemId={id}
        from={rerunFrom || undefined}
        stages={stages.map((s) => s.stage)}
      />
      <CookieDialog
        open={cookieOpen}
        onOpenChange={setCookieOpen}
        platform={d.platform}
        itemId={id}
      />
      <CancelDialog open={cancelOpen} onOpenChange={setCancelOpen} itemId={id} />
      <DeleteDialog open={deleteOpen} onOpenChange={setDeleteOpen} itemId={id} title={title} />
    </div>
  );
}

function Tabs({
  tab,
  onChange,
  labels,
}: {
  tab: ItemTab;
  onChange: (t: ItemTab) => void;
  labels: Record<ItemTab, string>;
}) {
  const { t } = useTranslation();
  const refs = useRef<Partial<Record<ItemTab, HTMLButtonElement | null>>>({});
  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    const i = ITEM_TABS.indexOf(tab);
    const next = {
      ArrowRight: ITEM_TABS[(i + 1) % ITEM_TABS.length],
      ArrowLeft: ITEM_TABS[(i - 1 + ITEM_TABS.length) % ITEM_TABS.length],
      Home: ITEM_TABS[0],
      End: ITEM_TABS[ITEM_TABS.length - 1],
    }[e.key];
    if (!next) return;
    e.preventDefault();
    onChange(next);
    refs.current[next]?.focus();
  }
  return (
    <div className="tabs" role="tablist" aria-label={t("item.tabs.label")} onKeyDown={onKeyDown}>
      {ITEM_TABS.map((k) => (
        <button
          key={k}
          ref={(el) => void (refs.current[k] = el)}
          type="button"
          role="tab"
          id={`tab-${k}`}
          aria-selected={k === tab}
          aria-controls={`panel-${k}`}
          tabIndex={k === tab ? 0 : -1}
          className="tabs__tab"
          onClick={() => onChange(k)}
        >
          {labels[k]}
        </button>
      ))}
    </div>
  );
}
