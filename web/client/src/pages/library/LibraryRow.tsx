// 媒體庫的一列：縮圖、標題、頻道、平台、發布日、長度、精簡版時間帶、狀態欄，需要處理時加上錯誤說明。
//
// 列表 API 沒有步驟狀態，所以每一列另外讀 GET /api/items/{id}（只有渲染出來的列會讀，虛擬列表只渲染可視範圍）。
// query key 用影音 ID：SSE 的 stage.status 讓 items.detail(item_id) 失效，只有這一列原地更新（LIB-R9）。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import type { MouseEvent } from "react";
import { useTranslation } from "react-i18next";

import { StatusBand, type StatusBandStage } from "@shared";

import { api, apiPath } from "../../api/client";
import { describeError } from "../../api/describeError";
import { queryKeys } from "../../api/queryKeys";
import {
  ErrorCallout,
  Spinner,
  StatusBadge,
  Thumbnail,
  useToast,
  type ErrorAction,
  type RowFocusProps,
} from "../../components";
import { formatDate, formatTimecode } from "../../i18n";
import {
  BAND_STAGES,
  badgeStatus,
  bandStatus,
  calloutCategory,
  headline,
  needsAttention,
  type ApiItem,
  type ApiItemDetail,
} from "./stageView";

export function useItemDetail(id: string) {
  return useQuery({
    queryKey: queryKeys.items.detail(id),
    queryFn: ({ signal }) =>
      api.get<"/api/items/{id}">(apiPath("/api/items/{id}", { id }), { signal }),
    staleTime: 30_000,
  });
}

const INTERACTIVE = "a, button, input, select, textarea, label, [role='button']";

export interface LibraryRowProps {
  item: ApiItem;
  focusProps: RowFocusProps;
  onFilterChannel: (channel: string) => void;
  /** UI-7 批次操作：有 onSelect 時列的左側有勾選框；shift 是按住 Shift 點擊（選取一段範圍） */
  selected?: boolean;
  onSelect?: (on: boolean, shift: boolean) => void;
}

export function LibraryRow({
  item,
  focusProps,
  onFilterChannel,
  selected,
  onSelect,
}: LibraryRowProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const detail = useItemDetail(item.id);
  const d: ApiItemDetail | undefined = detail.data;
  // metadata 完成後標題與長度才會出現：detail 比列表新
  const it: ApiItem = d ?? item;
  const title = it.title || t("library.untitled");
  // 頻道名稱只在列表 API 有（ListedItem.channel_name）；詳情沒有，所以從列表的 item 取，沒有時退回頻道 ID
  const channelName =
    (item.channel_id === it.channel_id ? item.channel_name : undefined) || it.channel_id || "";
  const stageName = (s: string) => t(`stage.${s}`, { defaultValue: s });

  const byStage = new Map((d?.stages ?? []).map((s) => [s.stage, s]));
  const bands: StatusBandStage[] = BAND_STAGES.map((key) => {
    const run = byStage.get(key);
    const status = run ? badgeStatus(run) : "pending";
    return {
      key,
      status: bandStatus(run),
      label: t("library.stageStatus", { stage: stageName(key), status: t(`status.${status}`) }),
    };
  });
  const bandLabel = bands.map((b) => b.label).join(t("common.listSeparator"));

  const h = headline(d?.stages);
  const attention = d !== undefined && needsAttention(h);

  function onRowClick(e: MouseEvent<HTMLDivElement>) {
    if ((e.target as HTMLElement).closest(INTERACTIVE)) return;
    void navigate({ to: "/items/$id", params: { id: item.id } });
  }

  return (
    <div
      className="lrow"
      data-attention={attention || undefined}
      data-selectable={onSelect ? true : undefined}
      data-selected={selected || undefined}
      onClick={onRowClick}
    >
      {onSelect && (
        <input
          type="checkbox"
          className="lrow__pick"
          checked={!!selected}
          tabIndex={focusProps.tabIndex}
          aria-label={t("library.bulk.pick", { title })}
          onClick={(e) => onSelect(e.currentTarget.checked, e.shiftKey)}
          onChange={() => undefined}
        />
      )}
      <Thumbnail
        className="lrow__thumb"
        platform={it.platform}
        externalId={it.external_id}
        placeholder={t(`platform.${it.platform}`, { defaultValue: it.platform })}
      />
      <div className="lrow__main">
        <Link
          to="/items/$id"
          params={{ id: item.id }}
          className="lrow__title"
          lang={it.original_lang || undefined}
          {...focusProps}
        >
          {title}
        </Link>
        <p className="lrow__meta">
          {/* LIB-R19：未讀的影音（打開詳情不會自動標為已讀） */}
          {it.read_state === "unread" && (
            <span className="lrow__unread">{t("library.unread")}</span>
          )}
          <span>{t(`platform.${it.platform}`, { defaultValue: it.platform })}</span>
          {it.channel_id && (
            <button
              type="button"
              className="lrow__channel"
              tabIndex={focusProps.tabIndex}
              onClick={() => onFilterChannel(it.channel_id!)}
              aria-label={t("library.filterChannel", { channel: channelName })}
              title={channelName !== it.channel_id ? it.channel_id : undefined}
            >
              {t("library.channel", { channel: channelName })}
            </button>
          )}
          {it.published_at && (
            <span className="num">
              {t("library.published", { date: formatDate(it.published_at) })}
            </span>
          )}
        </p>
      </div>
      <span className="lrow__duration num">
        {it.duration_s != null ? (
          <>
            <span className="visually-hidden">{t("library.duration")}</span>
            {formatTimecode(it.duration_s)}
          </>
        ) : (
          <span>{t("library.durationUnknown")}</span>
        )}
      </span>
      <StatusBand
        className="lrow__band"
        stages={bands}
        label={bandLabel}
        durationS={it.duration_s}
        // pages.md §3：點時間帶打開詳情並定位到該時間
        onSeek={(s) =>
          void navigate({ to: "/items/$id", params: { id: item.id }, search: { t: Math.floor(s) } })
        }
        seekLabel={t("library.band.seek", { title })}
        seekTabIndex={focusProps.tabIndex}
      />
      <div className="lrow__status">
        {detail.isPending ? (
          <Spinner label={t("library.loadingStatus")} />
        ) : detail.isError ? (
          <span className="secondary">{t("library.statusUnavailable")}</span>
        ) : (
          <>
            <StatusBadge
              status={h.status}
              label={t("library.stageStatus", {
                stage: stageName(h.stage),
                status: t(`status.${h.status}`),
              })}
            />
            {!attention && h.run?.executor && h.status !== "done" && (
              <small className="lrow__note secondary">
                {t(`executor.${h.run.executor}`, { defaultValue: h.run.executor })}
              </small>
            )}
          </>
        )}
      </div>
      {attention && h.run && (
        <div className="lrow__attention">
          <AttentionCallout itemId={item.id} stage={h.stage} run={h.run} />
        </div>
      )}
    </div>
  );
}

function AttentionCallout({
  itemId,
  stage,
  run,
}: {
  itemId: string;
  stage: string;
  run: NonNullable<ReturnType<typeof headline>["run"]>;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const toast = useToast();
  const qc = useQueryClient();
  const stageName = t(`stage.${stage}`, { defaultValue: stage });
  const rerun = useMutation({
    mutationFn: () =>
      api.post<"/api/items/{id}/rerun">(apiPath("/api/items/{id}/rerun", { id: itemId }), {
        body: { from: stage },
      }),
    onSuccess: () => {
      toast.show({ title: t("library.rerunQueued", { stage: stageName }) });
      void qc.invalidateQueries({ queryKey: queryKeys.items.detail(itemId) });
    },
    onError: (err) => toast.show({ title: describeError(t, err) }),
  });

  const actions: ErrorAction[] = [];
  const errorClass = run.error_class;
  if (run.status === "failed" || errorClass === "retryable") {
    actions.push({
      label: t("library.actions.rerunFrom", { stage: stageName }),
      onClick: () => rerun.mutate(),
    });
  }
  if (run.status === "blocked" && run.block_reason !== "budget_blocked") {
    actions.push({
      label: t("library.actions.openSettings"),
      onClick: () => void navigate({ to: "/settings/$section", params: { section: "accounts" } }),
    });
  }
  actions.push({
    label: t("library.actions.openItem"),
    onClick: () => void navigate({ to: "/items/$id", params: { id: itemId } }),
  });

  const reason = errorClass
    ? t(`errorClass.${errorClass}`, { defaultValue: errorClass })
    : t(`status.${badgeStatus(run)}`);
  const message = run.message
    ? t("library.attentionMessage", { stage: stageName, reason, message: run.message })
    : t("library.attentionNoMessage", { stage: stageName, reason });
  return (
    <ErrorCallout
      error={undefined}
      live={false}
      category={calloutCategory(errorClass)}
      message={message}
      actions={actions}
    />
  );
}
