// 步驟狀態與錯誤（OBS-2）：每個步驟的狀態、執行方式、來源紀錄摘要；failed／blocked／skipped 顯示錯誤類別、訊息、
// 可以直接處理的按鈕，以及相關的執行紀錄（API 沒有 log 片段，改列 GET /runs 的最近一筆）。
import { useTranslation } from "react-i18next";

import { Button, ErrorCallout, StatusBadge, type ErrorAction } from "../../components";
import { formatDateTime } from "../../i18n";
import { STAGES } from "../library/filters";
import { badgeStatus, calloutCategory, type ApiStage } from "../library/stageView";
import { mapActions, type ActionKind } from "./actions";
import { stageConfig, type ApiRun } from "./data";

export interface StageHandlers {
  onAction: (stage: string, action: ActionKind) => void;
  onShowRuns: () => void;
}

/** 依 pipeline 的順序排列；不認得的步驟放最後 */
export function orderedStages(stages: readonly ApiStage[]): ApiStage[] {
  const rank = (s: string) => {
    const i = (STAGES as readonly string[]).indexOf(s);
    return i < 0 ? STAGES.length : i;
  };
  return [...stages].sort((a, b) => rank(a.stage) - rank(b.stage));
}

export function StagePanel({
  stages,
  runs,
  handlers,
}: {
  stages: readonly ApiStage[];
  runs: readonly ApiRun[] | undefined;
  handlers: StageHandlers;
}) {
  const { t } = useTranslation();
  const list = orderedStages(stages);
  if (list.length === 0) return <p className="secondary">{t("item.stages.none")}</p>;
  return (
    <ol className="stages" aria-label={t("item.stages.title")}>
      {list.map((s) => (
        <StageRow
          key={s.stage}
          stage={s}
          run={runs?.find((r) => r.stage === s.stage)}
          handlers={handlers}
        />
      ))}
    </ol>
  );
}

function StageRow({
  stage,
  run,
  handlers,
}: {
  stage: ApiStage;
  run: ApiRun | undefined;
  handlers: StageHandlers;
}) {
  const { t } = useTranslation();
  const name = t(`stage.${stage.stage}`, { defaultValue: stage.stage });
  const status = badgeStatus(stage);
  const cfg = stageConfig(run);
  const executor = stage.executor || cfg.executor;
  const problem = stage.status === "failed" || stage.status === "blocked";
  const explainSkip = stage.status === "skipped" && !!stage.message;

  // 動作：API 的 actions 先；失敗的步驟一定可以重新處理；等待 Agent 一定可以改用 API
  const mapped = mapActions(stage.actions, stage.action_codes);
  const actions: ErrorAction[] = mapped.map((a, i) => ({
    label: a.key ? t(`actions.${a.key}`) : a.raw,
    primary: i === 0,
    onClick: () => handlers.onAction(stage.stage, a.action),
  }));
  const hasKind = (k: ActionKind["kind"]) => mapped.some((a) => a.action.kind === k);
  // 摘要後暫停（awaiting_user）：動作是「繼續處理」與「取消」，不另外加重新處理
  if ((problem || explainSkip) && !hasKind("rerun") && !hasKind("continue")) {
    actions.push({
      label: t("item.actions.rerunFrom", { stage: name }),
      onClick: () => handlers.onAction(stage.stage, { kind: "rerun" }),
    });
  }
  if (stage.status === "awaiting_agent" && !hasKind("switchToApi")) {
    actions.push({
      label: t("actions.switchToApi"),
      onClick: () => handlers.onAction(stage.stage, { kind: "switchToApi" }),
    });
  }
  if (mapped.some((a) => a.action.kind === "unknown")) {
    actions.push({
      label: t("item.actions.openSettings"),
      onClick: () => handlers.onAction(stage.stage, { kind: "settings", section: "services" }),
    });
  }
  if (problem || explainSkip) {
    actions.push({ label: t("item.stages.showRuns"), onClick: handlers.onShowRuns });
  }

  const reason = stage.error_class
    ? t(`errorClass.${stage.error_class}`, { defaultValue: stage.error_class })
    : t(`status.${status}`);

  return (
    <li className="stage" data-stage={stage.stage} data-status={stage.status}>
      <div className="stage__head">
        <h3 className="stage__name">{name}</h3>
        <StatusBadge status={status} />
        {executor && (
          <span className="stage__executor secondary">
            {t(`executor.${executor}`, { defaultValue: executor })}
          </span>
        )}
      </div>
      <dl className="stage__facts secondary">
        {(cfg.provider || cfg.model) && (
          <div>
            <dt>{t("item.stages.model")}</dt>
            <dd>{[cfg.provider, cfg.model].filter(Boolean).join(" / ")}</dd>
          </div>
        )}
        {cfg.prompt && (
          <div>
            <dt>{t("item.stages.prompt")}</dt>
            <dd>{cfg.prompt}</dd>
          </div>
        )}
        {stage.started_at && (
          <div>
            <dt>{t("item.stages.started")}</dt>
            <dd className="num">{formatDateTime(stage.started_at)}</dd>
          </div>
        )}
        {stage.finished_at && (
          <div>
            <dt>{t("item.stages.finished")}</dt>
            <dd className="num">{formatDateTime(stage.finished_at)}</dd>
          </div>
        )}
        {stage.not_before && stage.status === "waiting" && (
          <div>
            <dt>{t("item.stages.nextCheck")}</dt>
            <dd className="num">{formatDateTime(stage.not_before)}</dd>
          </div>
        )}
        {stage.attempts > 1 && (
          <div>
            <dt>{t("item.stages.attempts")}</dt>
            <dd className="num">{stage.attempts}</dd>
          </div>
        )}
      </dl>
      {(problem || explainSkip) && (
        <ErrorCallout
          error={undefined}
          live={false}
          category={explainSkip ? "fatal" : calloutCategory(stage.error_class)}
          message={
            stage.message
              ? t("item.stages.problem", { reason, message: stage.message })
              : t("item.stages.problemNoMessage", { reason })
          }
          actions={actions}
        />
      )}
      {!problem && !explainSkip && actions.length > 0 && (
        <div className="stage__actions">
          {stage.message && <p className="secondary">{stage.message}</p>}
          <div className="callout__actions">
            {actions.map((a) => (
              <Button key={a.label} onClick={a.onClick}>
                {a.label}
              </Button>
            ))}
          </div>
        </div>
      )}
      {!problem && !explainSkip && actions.length === 0 && stage.message && (
        <p className="stage__message secondary">{stage.message}</p>
      )}
    </li>
  );
}
