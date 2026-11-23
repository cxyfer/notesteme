// 媒體庫的批次操作列（LIB-08、LIB-R13～R20、R23；pages.md §3）：選取多列後出現。lazy 載入（只有選取時才需要）。
//
//   - 重新處理、改用其他處理方案、刪除、匯出 bundle、（等你決定的檢視）繼續處理：先 dry_run，確認框顯示數量、
//     預估費用、預算是否超過、刪除的合計與不能處理的影音，確認後分批執行，顯示進度、可以取消，結果逐項列出失敗的原因。
//   - 標為已讀／未讀可以復原：直接執行，toast 提供「復原」（README §3.5）。
//   - 「選取符合篩選的全部」用同一組篩選條件讀出所有 ID（最多 500 部，LIB-R13）：是當下的快照。
import { useQuery } from "@tanstack/react-query";
import { useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { describeError } from "../../api/describeError";
import { queryKeys } from "../../api/queryKeys";
import { Button, Dialog, ErrorCallout, SelectField, Spinner, useToast } from "../../components";
import { formatBytes, formatNumber, formatUsd } from "../../i18n";
import {
  MAX_IDS,
  chunkSize,
  dryRun,
  paramsFor,
  runChunks,
  sendBatch,
  sendContinue,
  type BatchAction,
  type BatchItem,
  type BatchParams,
  type BatchResult,
  type BulkAction,
  type RunOutcome,
} from "./bulk";
import { STAGES } from "./filters";
import "../../styles/ui7.css";

export interface BulkBarProps {
  ids: readonly string[];
  /** 符合目前篩選的總數 */
  total: number;
  apiQuery: Record<string, unknown>;
  /** 「等你決定」的檢視：多一個「繼續處理」 */
  awaiting: boolean;
  onSelect: (ids: string[]) => void;
  onClear: () => void;
  /** 執行完（含部分失敗、取消）：重新載入列表；選取在關閉結果時才由 onClear 清除 */
  onDone: (action: BulkAction, outcome: RunOutcome) => void;
}

const DIALOG_ACTIONS = ["rerun", "change_profile", "export_bundle", "delete"] as const;

export function BulkBar({
  ids,
  total,
  apiQuery,
  awaiting,
  onSelect,
  onClear,
  onDone,
}: BulkBarProps) {
  const { t } = useTranslation();
  const toast = useToast();
  const [action, setAction] = useState<BulkAction>();
  const [selecting, setSelecting] = useState(false);
  const [busy, setBusy] = useState(false);
  const n = ids.length;

  async function selectMatching() {
    setSelecting(true);
    try {
      const all: string[] = [];
      let cursor: string | undefined;
      do {
        const page = await api.get("/api/items", {
          query: { ...apiQuery, limit: 200, cursor },
        });
        all.push(...(page.items ?? []).map((x) => x.id));
        cursor = page.next_cursor;
      } while (cursor && all.length < MAX_IDS);
      onSelect(all.slice(0, MAX_IDS));
    } catch (err) {
      toast.show({ title: describeError(t, err) });
    } finally {
      setSelecting(false);
    }
  }

  // 已讀／未讀：直接執行，復原是相反的動作
  async function mark(a: "mark_read" | "mark_unread", list: readonly string[], undo: boolean) {
    setBusy(true);
    const out = await runChunks(list, chunkSize(a), (c) => sendBatch(a, c, {}), {
      cancelledMessage: t("library.bulk.cancelledItem"),
    });
    setBusy(false);
    onDone(a, out);
    if (out.error !== undefined) {
      toast.show({ title: describeError(t, out.error) });
      return;
    }
    const okIds = out.items.filter((x) => x.ok && x.id).map((x) => x.id!);
    toast.show({
      title: t(`library.bulk.${a}Done`, { count: out.succeeded }),
      ...(undo || okIds.length === 0
        ? {}
        : {
            action: {
              label: t("common.undo"),
              onClick: () =>
                void mark(a === "mark_read" ? "mark_unread" : "mark_read", okIds, true),
            },
          }),
    });
  }

  return (
    <div className="bulk" role="region" aria-label={t("library.bulk.label")}>
      <p className="bulk__count num" aria-live="polite">
        {t("library.bulk.selected", { count: n, formatted: formatNumber(n) })}
      </p>
      {total > n &&
        (total <= MAX_IDS ? (
          <Button variant="text" disabled={selecting} onClick={() => void selectMatching()}>
            {t("library.bulk.selectMatching", { count: total, formatted: formatNumber(total) })}
          </Button>
        ) : (
          <span className="secondary">{t("library.bulk.tooMany", { max: MAX_IDS })}</span>
        ))}
      <Button variant="text" onClick={onClear}>
        {t("library.bulk.clear")}
      </Button>
      <div className="bulk__actions">
        {awaiting && (
          <Button variant="primary" onClick={() => setAction("continue")}>
            {t("library.awaiting.confirm")}
          </Button>
        )}
        <Button onClick={() => setAction("rerun")}>{t("library.bulk.rerun")}</Button>
        <Button onClick={() => setAction("change_profile")}>
          {t("library.bulk.change_profile")}
        </Button>
        <Button disabled={busy} onClick={() => void mark("mark_read", ids, false)}>
          {t("library.bulk.mark_read")}
        </Button>
        <Button disabled={busy} onClick={() => void mark("mark_unread", ids, false)}>
          {t("library.bulk.mark_unread")}
        </Button>
        <Button onClick={() => setAction("export_bundle")}>
          {t("library.bulk.export_bundle")}
        </Button>
        <Button variant="danger" onClick={() => setAction("delete")}>
          {t("library.bulk.delete")}
        </Button>
      </div>
      {action && (action === "continue" || DIALOG_ACTIONS.includes(action as never)) && (
        <BulkDialog
          action={action as Exclude<BulkAction, "mark_read" | "mark_unread">}
          ids={ids}
          onClose={(finished) => {
            setAction(undefined);
            // 看完結果才清除選取（清除後操作列與這個對話框都會卸載）
            if (finished) onClear();
          }}
          onDone={onDone}
        />
      )}
    </div>
  );
}

type Phase =
  | { kind: "form" }
  | { kind: "preview" }
  | { kind: "running"; done: number; total: number }
  | { kind: "result"; outcome: RunOutcome };

/** 需要先填參數的動作 */
const NEEDS_FORM = new Set<BulkAction>(["rerun", "change_profile"]);

function BulkDialog({
  action,
  ids,
  onClose,
  onDone,
}: {
  action: Exclude<BulkAction, "mark_read" | "mark_unread">;
  ids: readonly string[];
  onClose: (finished: boolean) => void;
  onDone: BulkBarProps["onDone"];
}) {
  const { t } = useTranslation();
  const [phase, setPhase] = useState<Phase>(
    NEEDS_FORM.has(action) ? { kind: "form" } : { kind: "preview" },
  );
  const [params, setParams] = useState<BatchParams>({ from: "transcript" });
  const stop = useRef(false);
  const [stopping, setStopping] = useState(false);
  const fromId = useId();

  const profiles = useQuery({
    queryKey: queryKeys.profiles,
    queryFn: ({ signal }) => api.get("/api/profiles", { signal }),
    enabled: action === "change_profile",
  });
  const profileOptions = (profiles.data?.profiles ?? []).map((p) => ({
    value: p.name,
    label: p.name,
  }));
  const profile = params.profile ?? profileOptions[0]?.value;

  // 繼續處理沒有 dry_run：用「從分段重新處理」的 dry_run 估算之後的步驟（DIG-R6 的下一步是 segment）
  const previewAction: BatchAction = action === "continue" ? "rerun" : action;
  // delete_outputs 不影響 dry_run：不放進 key，勾選時不重新計算
  const previewParams: BatchParams =
    action === "continue" ? { from: "segment" } : paramsFor(action, { ...params, profile }, true);
  const preview = useQuery({
    queryKey: ["bulk-preview", previewAction, previewParams, ids],
    queryFn: () => dryRun(previewAction, ids, previewParams),
    enabled: phase.kind === "preview",
    staleTime: 0,
    gcTime: 0,
    retry: false,
  });
  const p = preview.data;
  const titles = new Map((p?.items ?? []).map((x) => [x.id ?? x.ref, x.title ?? x.ref]));
  // 繼續處理：dry_run 的 ok 只代表能重跑，送出全部選取的，由伺服器判斷是不是等你決定
  const runIds =
    action === "continue"
      ? [...ids]
      : (p?.items ?? []).filter((x) => x.ok && x.id).map((x) => x.id!);
  const blocked = action === "continue" ? [] : (p?.items ?? []).filter((x) => !x.ok);

  async function run() {
    stop.current = false;
    setPhase({ kind: "running", done: 0, total: runIds.length });
    const send =
      action === "continue"
        ? (c: string[]) => sendContinue(c, (code) => skipMessage(t, code))
        : (c: string[]) => sendBatch(action, c, { ...params, profile });
    const outcome = await runChunks(runIds, chunkSize(action), send, {
      onProgress: (done, total) => setPhase({ kind: "running", done, total }),
      shouldStop: () => stop.current,
      cancelledMessage: t("library.bulk.cancelledItem"),
    });
    setPhase({ kind: "result", outcome });
    onDone(action, outcome);
  }

  const running = phase.kind === "running";
  const verb = t(`library.bulk.${action === "continue" ? "continue" : action}`);
  const count = action === "continue" ? ids.length : (p?.count ?? 0);

  let body: React.ReactNode;
  let footer: React.ReactNode;
  if (phase.kind === "form") {
    body = (
      <form
        id={fromId}
        className="bulk__form"
        onSubmit={(e) => {
          e.preventDefault();
          setPhase({ kind: "preview" });
        }}
      >
        {action === "change_profile" &&
          (profiles.isPending ? (
            <Spinner label={t("common.loading")} />
          ) : profiles.isError ? (
            <ErrorCallout error={profiles.error} />
          ) : (
            <SelectField
              label={t("library.bulk.profile")}
              options={profileOptions}
              value={profile ?? ""}
              onChange={(e) => setParams({ ...params, profile: e.target.value })}
            />
          ))}
        <SelectField
          label={t("library.bulk.from")}
          options={STAGES.map((s) => ({ value: s, label: t(`stage.${s}`) }))}
          value={params.from}
          onChange={(e) => setParams({ ...params, from: e.target.value })}
        />
      </form>
    );
    footer = (
      <Button
        variant="primary"
        type="submit"
        form={fromId}
        disabled={action === "change_profile" && !profile}
      >
        {t("library.bulk.preview")}
      </Button>
    );
  } else if (phase.kind === "preview") {
    body = preview.isPending ? (
      <Spinner label={t("library.bulk.previewing")} showLabel />
    ) : preview.isError ? (
      <ErrorCallout
        error={preview.error}
        actions={[{ label: t("common.retry"), onClick: () => void preview.refetch() }]}
      />
    ) : (
      p && (
        <Impact
          action={action}
          result={p}
          count={count}
          blocked={blocked}
          deleteOutputs={!!params.delete_outputs}
          onDeleteOutputs={(v) => setParams({ ...params, delete_outputs: v })}
        />
      )
    );
    footer = (
      <Button
        variant={action === "delete" ? "danger" : "primary"}
        disabled={!p || runIds.length === 0}
        onClick={() => void run()}
      >
        {t("library.bulk.confirm", { verb, count: runIds.length })}
      </Button>
    );
  } else if (phase.kind === "running") {
    body = (
      <div aria-live="polite">
        <p className="num">
          {t("library.bulk.progress", { done: phase.done, total: phase.total })}
        </p>
        <progress
          className="bulk__progress"
          max={phase.total}
          value={phase.done}
          aria-label={t("library.bulk.progressLabel")}
        />
      </div>
    );
    footer = (
      <Button
        disabled={stopping}
        onClick={() => {
          stop.current = true;
          setStopping(true);
        }}
      >
        {t("library.bulk.stop")}
      </Button>
    );
  } else {
    body = <Outcome action={action} outcome={phase.outcome} titles={titles} />;
    footer = <Button onClick={() => onClose(true)}>{t("common.close")}</Button>;
  }

  return (
    <Dialog
      open
      onOpenChange={(o) => !o && !running && onClose(phase.kind === "result")}
      closable={!running}
      title={t("library.bulk.title", { verb, count: ids.length })}
      description={t(`library.bulk.desc.${action}`)}
      footer={footer}
    >
      {body}
    </Dialog>
  );
}

function skipMessage(t: (k: string, o?: Record<string, unknown>) => string, code: string) {
  return t(`library.bulk.skip.${code}`, { defaultValue: code });
}

function Impact({
  action,
  result: p,
  count,
  blocked,
  deleteOutputs,
  onDeleteOutputs,
}: {
  action: BulkAction;
  result: BatchResult;
  count: number;
  blocked: BatchItem[];
  deleteOutputs: boolean;
  onDeleteOutputs: (v: boolean) => void;
}) {
  const { t } = useTranslation();
  const paid = action === "rerun" || action === "change_profile" || action === "continue";
  const d = p.deletion;
  const removable = d ? d.outputs - d.modified_outputs : 0;
  return (
    <div className="bulk__impact">
      <p className="bulk__lead num">{t("library.bulk.count", { count })}</p>
      {paid && (
        <ul className="delete__facts">
          <li>
            {t(action === "continue" ? "library.bulk.estimateApprox" : "library.bulk.estimate", {
              amount: formatUsd(p.estimated_usd ?? 0),
            })}
          </li>
          {(p.unknown_estimates ?? 0) > 0 && (
            <li>{t("library.bulk.unknown", { count: p.unknown_estimates })}</li>
          )}
          {p.budget?.remaining_usd != null && (
            <li>{t("library.bulk.remaining", { amount: formatUsd(p.budget.remaining_usd) })}</li>
          )}
        </ul>
      )}
      {paid && p.budget?.exceeds && (
        <ErrorCallout
          error={null}
          live={false}
          category="wait"
          message={t("library.bulk.exceeds")}
        />
      )}
      {action === "delete" && d && (
        <>
          <ul className="delete__facts">
            <li>
              {t("item.delete.artifacts", {
                count: d.artifacts,
                formatted: formatNumber(d.artifacts),
              })}
            </li>
            <li>{t("item.delete.bytes", { size: formatBytes(d.bytes) })}</li>
            <li>{t("item.delete.spent", { amount: formatUsd(d.spent_usd) })}</li>
          </ul>
          <p className="secondary">{t("item.delete.restoreHint")}</p>
          {removable > 0 && (
            <label className="check">
              <input
                type="checkbox"
                checked={deleteOutputs}
                onChange={(e) => onDeleteOutputs(e.target.checked)}
              />
              <span>
                {t("item.delete.deleteOutputs", {
                  count: removable,
                  formatted: formatNumber(removable),
                })}
              </span>
            </label>
          )}
          {d.modified_outputs > 0 && (
            <p className="secondary">
              {t("library.bulk.modifiedKept", { count: d.modified_outputs })}
            </p>
          )}
        </>
      )}
      {blocked.length > 0 && (
        <>
          <p>{t("library.bulk.skipped", { count: blocked.length })}</p>
          <ItemList items={blocked} titles={new Map()} />
        </>
      )}
    </div>
  );
}

function ItemList({ items, titles }: { items: BatchItem[]; titles: Map<string, string> }) {
  return (
    <ul className="bulk__items">
      {items.map((x, i) => (
        <li key={`${x.ref}-${i}`}>
          <span className="bulk__item-title">{x.title ?? titles.get(x.id ?? x.ref) ?? x.ref}</span>
          {x.message && <span className="secondary">{x.message}</span>}
        </li>
      ))}
    </ul>
  );
}

function Outcome({
  action,
  outcome,
  titles,
}: {
  action: BulkAction;
  outcome: RunOutcome;
  titles: Map<string, string>;
}) {
  const { t } = useTranslation();
  const failed = outcome.items.filter((x) => !x.ok);
  // 只接受站內的 API 路徑（L-M11：/api/items/{id}/bundle）
  const bundles = outcome.items.filter((x) => x.ok && x.bundle_url?.startsWith("/api/"));
  return (
    <div className="bulk__impact" role="status">
      <p className="bulk__lead num">
        {t("library.bulk.result", { ok: outcome.succeeded, failed: outcome.failed })}
      </p>
      {outcome.error !== undefined && <ErrorCallout error={outcome.error} />}
      {failed.length > 0 && (
        <>
          <p>{t("library.bulk.failedList")}</p>
          <ItemList items={failed} titles={titles} />
        </>
      )}
      {action === "delete" && outcome.succeeded > 0 && (
        <p className="secondary">{t("item.delete.restoreHint")}</p>
      )}
      {bundles.length > 0 && (
        <>
          <p>{t("library.bulk.bundles")}</p>
          <ul className="bulk__items">
            {bundles.map((x) => (
              <li key={x.id ?? x.ref}>
                <a href={x.bundle_url} download>
                  {titles.get(x.id ?? x.ref) ?? x.ref}
                </a>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}
