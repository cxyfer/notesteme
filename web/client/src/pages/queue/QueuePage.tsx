// 佇列（docs/ui/pages.md §6；LIB-R8、PIPE-R8、R10、R15）。
//
//   - 每個資源類別一條通道：方格數是並行上限、實心是使用中；上限可以直接 +／−（PATCH /api/queue/limits）；
//     每個類別與整個佇列都可以暫停／繼續。
//   - 任務列表：執行中與等待中的任務（只有內建 worker 的任務；API 不列 Agent 任務，頁面上明說）。
//     每列可以只取消這個步驟（POST /api/tasks/{id}/cancel），或停止整部影音（POST /api/items/{id}/cancel）。
//   - 失敗與停住：GET /api/queue 的 failed 清單，可以單獨或全部重試（FailedTasks.tsx）。
//   - 需要你處理：依步驟計數 failed／blocked 的影音（GET /api/items?status=…&limit=1 的 total），連到媒體庫的篩選。
//   - SSE：stage.status 讓 queue 失效；列表以任務 ID 為 key，重新讀取只更新受影響的列，不重排捲動位置。
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Link } from "@tanstack/react-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { describeError } from "../../api/describeError";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import {
  Button,
  Dialog,
  EmptyState,
  ErrorCallout,
  Spinner,
  StatusBadge,
  useToast,
  VirtualList,
  type RowFocusProps,
} from "../../components";
import { useAddDrawer } from "../../add/AddContext";
import { useServerEvent, useStreamReconnect } from "../../events/useEventStream";
import { formatDateTime, formatNumber } from "../../i18n";
import { usePageTitle } from "../../shell/usePageTitle";
import { STAGES } from "../library/filters";
import { useItemDetail } from "../library/LibraryRow";
import { FailedTasks } from "./FailedTasks";
import {
  filterTasks,
  RESOURCE_CLASSES,
  TASK_STATES,
  validateQueueSearch,
  type QueueSearch,
  type QueueTask,
} from "./filters";

const route = getRouteApi("/app/queue");

/** 點任務打開影音詳情的「紀錄」分頁（pages.md §6） */
const RUNS_TAB = { tab: "runs" } as const;
type ControlOp =
  { kind: "pause" | "resume"; class?: string } | { kind: "limit"; class: string; limit: number };
const PAUSE = "pause";
const RESUME = "resume";
const LIMIT = "limit";
const ATTENTION_DEBOUNCE_MS = 2000;
/** 會讓「需要你處理」的數字改變的步驟狀態（重新處理時離開 failed 會先變成 pending） */
const ATTENTION_EVENTS = new Set(["failed", "blocked", "pending", "done", "skipped"]);

type QueueStatus = NonNullable<Awaited<ReturnType<typeof fetchQueue>>>;

function fetchQueue(signal?: AbortSignal) {
  return api.get<"/api/queue">("/api/queue", { signal });
}

export function QueuePage() {
  const { t } = useTranslation();
  usePageTitle(t("pages.queue.title"));
  const rawSearch = route.useSearch();
  const search = useMemo(
    () => validateQueueSearch(rawSearch as Record<string, unknown>),
    [rawSearch],
  );
  const navigate = route.useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const addDrawer = useAddDrawer();

  const queue = useQuery({
    queryKey: queryKeys.queue,
    queryFn: ({ signal }) => fetchQueue(signal),
    staleTime: 5_000,
  });
  const q = queue.data;

  // 暫停、繼續、上限：成功時直接用回應更新快取
  const control = useMutation({
    mutationFn: (op: ControlOp) =>
      op.kind === "limit"
        ? api.patch<"/api/queue/limits">("/api/queue/limits", { body: { [op.class]: op.limit } })
        : api.post(op.kind === "pause" ? "/api/queue/pause" : "/api/queue/resume", {
            body: op.class ? { class: op.class } : {},
          }),
    onSuccess: (data, op) => {
      if (data) qc.setQueryData(queryKeys.queue, data);
      else void qc.invalidateQueries({ queryKey: queryKeys.queue });
      if (op.kind === "pause")
        toast.show({
          title: op.class
            ? t("queue.pausedClass", { cls: className(op.class) })
            : t("queue.pausedAll"),
        });
      if (op.kind === "resume")
        toast.show({
          title: op.class
            ? t("queue.resumedClass", { cls: className(op.class) })
            : t("queue.resumedAll"),
        });
    },
    onError: (err) => toast.show({ title: describeError(t, err) }),
  });
  const className = (c: string) => t(`queue.class.${c}`, { defaultValue: c });

  const setSearch = (patch: Partial<QueueSearch>) =>
    void navigate({
      search: (prev: QueueSearch) => validateQueueSearch({ ...prev, ...patch }),
    });

  const togglePauseAll = () => control.mutate({ kind: q?.paused ? RESUME : PAUSE });
  const resumeAll = () => control.mutate({ kind: RESUME });
  const tasks = useMemo(() => filterTasks(q?.tasks ?? [], search), [q?.tasks, search]);
  const [cancelFor, setCancelFor] = useState<CancelTarget | null>(null);

  // 佇列沒有啟用（501）：說明，不顯示會失敗的控制項
  const disabled = isApiError(queue.error) && queue.error.status === 501;

  return (
    <div className="queue">
      <div className="page__head">
        <h1 className="page__title">{t("pages.queue.title")}</h1>
        {q && (
          <Button onClick={togglePauseAll} disabled={control.isPending}>
            {q.paused ? t("queue.resumeAll") : t("queue.pauseAll")}
          </Button>
        )}
      </div>

      {q?.paused && (
        <div className="queue__banner" role="status">
          <StatusBadge status="waiting" label={t("queue.pausedBanner")} />
          <Button variant="primary" onClick={resumeAll}>
            {t("queue.resumeAll")}
          </Button>
        </div>
      )}

      {queue.isPending ? (
        <Spinner label={t("queue.loading")} showLabel />
      ) : !q ? (
        <ErrorCallout
          error={queue.error}
          category={disabled ? "fatal" : undefined}
          actions={
            disabled ? [] : [{ label: t("common.retry"), onClick: () => void queue.refetch() }]
          }
        />
      ) : (
        <>
          <Lanes
            q={q}
            busy={control.isPending}
            onLimit={(cls, limit) => control.mutate({ kind: LIMIT, class: cls, limit })}
            onToggle={(cls, paused) =>
              control.mutate({ kind: paused ? RESUME : PAUSE, class: cls })
            }
          />
          <FailedTasks failed={q.failed ?? []} />
          <Attention />

          <section aria-labelledby="tasks-title">
            <h2 id="tasks-title" className="section__title">
              {t("queue.tasks", {
                count: q.tasks?.length ?? 0,
                formatted: formatNumber(q.tasks?.length ?? 0),
              })}
            </h2>
            <p className="queue__note secondary">{t("queue.agentNote")}</p>
            <p className="queue__note secondary">{t("queue.taskActionsNote")}</p>
            <div className="queue__filters">
              <div className="field">
                <label className="field__label" htmlFor="queue-class">
                  {t("queue.filterClass")}
                </label>
                <select
                  id="queue-class"
                  className="field__input"
                  value={search.class ?? ""}
                  onChange={(e) =>
                    setSearch({ class: (e.target.value || undefined) as QueueSearch["class"] })
                  }
                >
                  <option value="">{t("queue.all")}</option>
                  {RESOURCE_CLASSES.map((c) => (
                    <option key={c} value={c}>
                      {className(c)}
                    </option>
                  ))}
                </select>
              </div>
              <div className="field">
                <label className="field__label" htmlFor="queue-state">
                  {t("queue.filterState")}
                </label>
                <select
                  id="queue-state"
                  className="field__input"
                  value={search.state ?? ""}
                  onChange={(e) =>
                    setSearch({ state: (e.target.value || undefined) as QueueSearch["state"] })
                  }
                >
                  <option value="">{t("queue.all")}</option>
                  {TASK_STATES.map((s) => (
                    <option key={s} value={s}>
                      {t(`queue.state.${s}`)}
                    </option>
                  ))}
                </select>
              </div>
              {(search.class || search.state) && (
                <Button variant="text" onClick={() => void navigate({ search: {} })}>
                  {t("library.filters.clearFilters")}
                </Button>
              )}
            </div>
            {(q.tasks?.length ?? 0) === 0 ? (
              <EmptyState
                message={t("queue.empty")}
                action={{ label: t("add.open"), onClick: () => addDrawer.open() }}
              />
            ) : tasks.length === 0 ? (
              <EmptyState
                message={t("queue.emptyFiltered")}
                action={{
                  label: t("library.filters.clearFilters"),
                  onClick: () => void navigate({ search: {} }),
                }}
              />
            ) : (
              <VirtualList
                className="queue__list"
                label={t("queue.listLabel")}
                items={tasks}
                getKey={(x) => x.id}
                estimateSize={56}
                renderItem={(task, { focusProps }) => (
                  <TaskRow
                    task={task}
                    focusProps={focusProps}
                    onCancelTask={() => setCancelFor({ task, scope: "task" })}
                    onCancel={() => setCancelFor({ task, scope: "item" })}
                  />
                )}
              />
            )}
          </section>
        </>
      )}
      <CancelDialog target={cancelFor} onClose={() => setCancelFor(null)} />
    </div>
  );
}

function Lanes({
  q,
  busy,
  onLimit,
  onToggle,
}: {
  q: QueueStatus;
  busy: boolean;
  onLimit: (cls: string, limit: number) => void;
  onToggle: (cls: string, paused: boolean) => void;
}) {
  const { t } = useTranslation();
  return (
    <ul className="lanes" aria-label={t("queue.lanes")}>
      {(q.lanes ?? []).map((l) => {
        const name = t(`queue.class.${l.class}`, { defaultValue: l.class });
        const slots = Math.min(l.limit, 64);
        return (
          <li key={l.class} className="lane" data-class={l.class}>
            <h2 className="lane__name">{name}</h2>
            <span className="lane__slots" aria-hidden="true">
              {Array.from({ length: slots }, (_, i) => (
                <span key={i} className="lane__slot" data-used={i < l.running || undefined} />
              ))}
            </span>
            <p className="lane__summary">
              {l.running === 0 && l.waiting === 0
                ? t("queue.idle", { limit: l.limit })
                : t("queue.usage", { running: l.running, limit: l.limit, waiting: l.waiting })}
              {(l.paused || q.paused) && (
                <>
                  {" "}
                  <StatusBadge
                    status="waiting"
                    label={l.paused ? t("queue.lanePaused") : t("queue.pausedWithQueue")}
                  />
                </>
              )}
            </p>
            <div className="lane__controls">
              <Button
                variant="outline"
                aria-label={t("queue.decrease", { cls: name })}
                disabled={busy || l.limit <= 1}
                onClick={() => onLimit(l.class, l.limit - 1)}
              >
                {t("queue.minus")}
              </Button>
              <span className="lane__limit num" aria-live="polite">
                <span className="visually-hidden">{t("queue.limitOf", { cls: name })}</span>
                {l.limit}
              </span>
              <Button
                variant="outline"
                aria-label={t("queue.increase", { cls: name })}
                disabled={busy || l.limit >= 64}
                onClick={() => onLimit(l.class, l.limit + 1)}
              >
                {t("queue.plus")}
              </Button>
              <Button variant="text" disabled={busy} onClick={() => onToggle(l.class, l.paused)}>
                {l.paused
                  ? t("queue.resumeClass", { cls: name })
                  : t("queue.pauseClass", { cls: name })}
              </Button>
            </div>
          </li>
        );
      })}
    </ul>
  );
}

/** 需要你處理：每個步驟 failed 或 blocked 的影音數（同一部影音可能在不同步驟各算一次） */
function Attention() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  // 失敗、停住或重新開始的事件讓計數重新讀取；處理中會有一連串事件，2 秒內只讀一次
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const refresh = useCallback(() => {
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(
      () => void qc.invalidateQueries({ queryKey: queryKeys.items.allAttention() }),
      ATTENTION_DEBOUNCE_MS,
    );
  }, [qc]);
  useServerEvent(
    (ev) => {
      if (ev.status && ATTENTION_EVENTS.has(ev.status)) refresh();
    },
    ["stage.status", "item.deleted", "item.restored"],
  );
  useStreamReconnect(refresh);
  const results = useQueries({
    queries: STAGES.map((stage) => ({
      queryKey: queryKeys.items.attention(stage),
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        api.get<"/api/items">("/api/items", {
          query: { status: [`${stage}:failed`, `${stage}:blocked`], limit: 1 },
          signal,
        }),
      staleTime: 30_000,
    })),
  });
  const counts = STAGES.map((stage, i) => ({ stage, total: results[i]?.data?.total ?? 0 })).filter(
    (c) => c.total > 0,
  );
  const failed = results.some((r) => r.isError);
  return (
    <section aria-labelledby="attention-title">
      <h2 id="attention-title" className="section__title">
        {t("queue.attention")}
      </h2>
      {failed ? (
        <p className="secondary">{t("queue.attentionUnavailable")}</p>
      ) : counts.length === 0 ? (
        <p className="secondary">
          {results.some((r) => r.isPending) ? t("common.loading") : t("queue.attentionNone")}
        </p>
      ) : (
        <ul className="attention">
          {counts.map((c) => (
            <li key={c.stage}>
              <Link to="/library" search={{ status: [`${c.stage}:failed`, `${c.stage}:blocked`] }}>
                {t("queue.attentionStage", {
                  stage: t(`stage.${c.stage}`, { defaultValue: c.stage }),
                  count: c.total,
                  formatted: formatNumber(c.total),
                })}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function TaskRow({
  task,
  focusProps,
  onCancelTask,
  onCancel,
}: {
  task: QueueTask;
  focusProps: RowFocusProps;
  onCancelTask: () => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  const detail = useItemDetail(task.item_id);
  const title = detail.data?.title || task.item;
  // 重試或等待中的任務有 not_before（最早可以再被領取的時間）
  const later = task.state === "pending" && !!task.not_before;
  return (
    <div className="trow" data-state={task.state}>
      <Link
        to="/items/$id"
        params={{ id: task.item_id }}
        search={RUNS_TAB}
        className="trow__title"
        lang={detail.data?.original_lang || undefined}
        {...focusProps}
      >
        {title}
      </Link>
      <span>{t(`stage.${task.stage}`, { defaultValue: task.stage })}</span>
      <StatusBadge status={task.state === "leased" ? "running" : later ? "waiting" : "pending"} />
      <span className="secondary">
        {later
          ? t("queue.notBefore", { time: formatDateTime(task.not_before!) })
          : task.attempts > 0
            ? t("queue.attempts", { count: task.attempts })
            : t(`queue.class.${task.resource_class}`, { defaultValue: task.resource_class })}
      </span>
      <Button variant="text" tabIndex={focusProps.tabIndex} onClick={onCancelTask}>
        {t("queue.cancelTask")}
      </Button>
      <Button variant="text" tabIndex={focusProps.tabIndex} onClick={onCancel}>
        {t("queue.stopItem")}
      </Button>
    </div>
  );
}

/** 取消的對象：只有這個任務所屬的步驟，或整部影音 */
type CancelTarget = { task: QueueTask; scope: "task" | "item" };

function CancelDialog({ target, onClose }: { target: CancelTarget | null; onClose: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const cancel = useMutation({
    mutationFn: (x: CancelTarget) =>
      x.scope === "task"
        ? api.post(apiPath("/api/tasks/{id}/cancel", { id: x.task.id }))
        : api.post(apiPath("/api/items/{id}/cancel", { id: x.task.item_id })),
    onSuccess: (_d, x) => {
      void qc.invalidateQueries({ queryKey: queryKeys.queue });
      void qc.invalidateQueries({ queryKey: queryKeys.items.detail(x.task.item_id) });
      toast.show({ title: x.scope === "task" ? t("queue.cancelTaskDone") : t("item.cancel.done") });
      onClose();
    },
  });
  const forTask = target?.scope === "task";
  const stage = target ? t(`stage.${target.task.stage}`, { defaultValue: target.task.stage }) : "";
  return (
    <Dialog
      open={target !== null}
      onOpenChange={(o) => {
        if (!o) {
          cancel.reset();
          onClose();
        }
      }}
      title={forTask ? t("queue.cancelTaskTitle") : t("item.cancel.title")}
      description={
        forTask
          ? t("queue.cancelTaskDescription", { item: target?.task.item ?? "", stage })
          : t("queue.stopItemDescription", { item: target?.task.item ?? "" })
      }
      footer={
        <Button
          variant="danger"
          disabled={cancel.isPending}
          onClick={() => target && cancel.mutate(target)}
        >
          {forTask ? t("queue.cancelTaskConfirm") : t("item.cancel.confirm")}
        </Button>
      }
    >
      {cancel.isError && <ErrorCallout error={cancel.error} />}
    </Dialog>
  );
}
