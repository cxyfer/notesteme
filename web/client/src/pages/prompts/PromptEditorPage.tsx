// Prompt 編輯頁（docs/ui/pages.md §7）：版本列（切換、比較、另存新版本、設為預設）、CodeMirror 編輯器、試跑面板、刪除。
// 這個模組只由 lazy route 載入（router.tsx），CodeMirror 不進初始 bundle。
//
// - 版本建立後不能修改（PRM-R2）：編輯器的內容一律「另存新版本」；正在使用的處理方案不會自動改用（PRM-R5）。
// - 內建的 prompt（builtin）唯讀，只能「另存為新的 prompt」（PRM-R4）。
// - 試跑只能引用已儲存的版本（<id>@N）；有未儲存的修改時提示先另存新版本。
// - 刪除是硬刪除（不可復原）：先確認；仍被使用時後端回 409 prompt_in_use 與 used_by，列出使用者（PRM-R8、P7）。
import "./prompt-editor.css";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { describeError } from "../../api/describeError";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import { Button, Dialog, ErrorCallout, SelectField, Spinner, useToast } from "../../components";
import { formatDateTime, formatTimecode } from "../../i18n";
import { usePageTitle } from "../../shell/usePageTitle";
import { NotFoundContent } from "../NotFoundPage";
import { NewPromptDialog } from "./NewPromptDialog";
import {
  EMPTY_PARAMS,
  missingParams,
  RunParamsFields,
  RunResult,
  type PlaygroundResult,
  type RunParams,
} from "./playground";
import { PromptCodeEditor } from "./PromptCodeEditor";
import {
  groupOf,
  scanVariables,
  PROMPT_VARIABLES,
  type PromptSummary,
  type PromptVersion,
  type VarIssue,
} from "./promptModel";

const route = getRouteApi("/app/prompts/$id");

export function PromptEditorPage() {
  const { t } = useTranslation();
  const { id } = route.useParams();
  usePageTitle(`${id} · ${t("pages.prompts.title")}`);
  const prompts = useQuery({
    queryKey: queryKeys.prompts.list(),
    queryFn: ({ signal }) => api.get("/api/prompts", { signal }),
  });
  const versions = useQuery({
    queryKey: queryKeys.prompts.versions(id),
    queryFn: ({ signal }) =>
      api.get<"/api/prompts/{id}/versions">(apiPath("/api/prompts/{id}/versions", { id }), {
        signal,
      }),
  });
  const heading = <h1 className="page__title">{id}</h1>;
  const summary = prompts.data?.prompts?.find((p) => p.id === id);
  if (prompts.isPending || versions.isPending) {
    return (
      <>
        {heading}
        <Spinner label={t("common.loading")} />
      </>
    );
  }
  const err = versions.error ?? prompts.error;
  if (err || !summary) {
    if ((isApiError(err) && err.status === 404) || (!err && !summary)) return <NotFoundContent />;
    return (
      <>
        {heading}
        <ErrorCallout
          error={err}
          actions={[
            {
              label: t("common.retry"),
              onClick: () => {
                void prompts.refetch();
                void versions.refetch();
              },
            },
          ]}
        />
      </>
    );
  }
  return (
    <PromptEditor
      key={id}
      heading={heading}
      summary={summary}
      versions={versions.data?.versions ?? []}
    />
  );
}

function PromptEditor({
  heading,
  summary,
  versions,
}: {
  heading: React.ReactNode;
  summary: PromptSummary;
  versions: PromptVersion[];
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const id = summary.id;
  const [selected, setSelected] = useState(summary.default_version);
  const current = versions.find((v) => v.version === selected) ?? versions.at(-1);
  const [body, setBody] = useState(current?.body ?? "");
  const [pendingSwitch, setPendingSwitch] = useState<number | null>(null);
  const [saveAsNew, setSaveAsNew] = useState(false);
  const [comparing, setComparing] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [params, setParams] = useState<RunParams>(EMPTY_PARAMS);
  const dirty = !!current && body !== current.body;
  const vars = PROMPT_VARIABLES;
  const { issues } = scanVariables(body, vars);
  const labelId = useId();
  const issuesId = useId();

  /** 切換到某個版本並載入它的內文 */
  function select(v: number) {
    setSelected(v);
    setBody(versions.find((x) => x.version === v)?.body ?? "");
  }

  const issueMessage = (i: VarIssue) =>
    i.kind === "undefined"
      ? t("prompts.issue.undefined", { name: i.name, vars: vars.join(t("common.listSeparator")) })
      : i.kind === "invalid"
        ? t("prompts.issue.invalid", { text: `{{${i.name}}}` })
        : t("prompts.issue.unclosed", { open: "{{", close: "}}" });

  const invalidate = () => qc.invalidateQueries({ queryKey: queryKeys.prompts.all });
  const addVersion = useMutation({
    mutationFn: () =>
      api.post<"/api/prompts/{id}/versions">(apiPath("/api/prompts/{id}/versions", { id }), {
        body: { body },
      }),
    onSuccess: async (res) => {
      await invalidate();
      setSelected(res.version);
      toast.show({ title: t("prompts.savedAs", { version: res.version }) });
    },
  });
  const setDefault = useMutation({
    mutationFn: (version: number) =>
      api.post<"/api/prompts/{id}/default">(apiPath("/api/prompts/{id}/default", { id }), {
        body: { version },
      }),
    onSuccess: async () => {
      await invalidate();
      toast.show({ title: t("common.setAsDefaultDone") });
    },
  });

  function trySwitch(v: number) {
    if (v === selected) return;
    if (dirty) setPendingSwitch(v);
    else select(v);
  }

  const readOnly = summary.builtin;
  return (
    <>
      <nav className="crumbs" aria-label={t("prompts.breadcrumb")}>
        <Link to="/prompts">{t("pages.prompts.title")}</Link>
        <span aria-hidden="true"> › </span>
        <span>{t(`prompts.group.${groupOf(summary.kind)}`)}</span>
      </nav>
      <div className="page__head">
        {heading}
        {readOnly && <span className="tag">{t("prompts.builtinTag")}</span>}
        <span className="tag">
          {t(`prompts.kind.${summary.kind}`, { defaultValue: summary.kind })}
        </span>
      </div>

      <div className="version-bar">
        <div role="group" aria-label={t("prompts.versions")} className="version-bar__list">
          {[...versions].reverse().map((v) => (
            <button
              key={v.version}
              type="button"
              className="version-bar__item"
              aria-pressed={v.version === selected}
              onClick={() => trySwitch(v.version)}
              title={formatDateTime(v.created_at)}
            >
              {v.version === summary.default_version
                ? t("prompts.versionInUse", { version: v.version })
                : t("prompts.versionN", { version: v.version })}
            </button>
          ))}
        </div>
        <div className="page__actions">
          <Button disabled={versions.length < 2} onClick={() => setComparing(true)}>
            {t("prompts.compare")}
          </Button>
          {readOnly ? (
            <Button variant="primary" onClick={() => setSaveAsNew(true)}>
              {t("prompts.saveAsNew")}
            </Button>
          ) : (
            <>
              <Button
                disabled={!dirty || issues.length > 0 || addVersion.isPending}
                variant="primary"
                onClick={() => addVersion.mutate()}
              >
                {addVersion.isPending ? t("common.saving") : t("prompts.saveVersion")}
              </Button>
              <Button
                disabled={dirty || selected === summary.default_version || setDefault.isPending}
                onClick={() => setDefault.mutate(selected)}
              >
                {t("common.setAsDefault")}
              </Button>
              <Button onClick={() => setSaveAsNew(true)}>{t("prompts.saveAsNewShort")}</Button>
              <Button variant="danger" onClick={() => setDeleting(true)}>
                {t("prompts.delete")}
              </Button>
            </>
          )}
        </div>
      </div>
      <p className="secondary">
        {readOnly
          ? t("prompts.builtinReadOnly")
          : dirty
            ? t("prompts.unsavedHint", { version: selected })
            : selected !== summary.default_version
              ? t("prompts.notDefaultHint", { version: selected, current: summary.default_version })
              : t("prompts.versionHint")}
      </p>
      {addVersion.error && (
        <ErrorCallout
          error={addVersion.error}
          message={
            isApiError(addVersion.error) && addVersion.error.body.undefined_variables?.length
              ? t("prompts.undefinedOnSave", {
                  names: addVersion.error.body.undefined_variables.join(t("common.listSeparator")),
                })
              : undefined
          }
        />
      )}
      {setDefault.error && <ErrorCallout error={setDefault.error} />}

      <div className="prompt-layout">
        <section className="prompt-editor" aria-labelledby={labelId}>
          <h2 id={labelId} className="tune-section__title">
            {t("prompts.editorLabel", { version: selected })}
          </h2>
          <PromptCodeEditor
            value={body}
            onChange={setBody}
            readOnly={readOnly}
            labelledBy={labelId}
            describedBy={issues.length ? issuesId : undefined}
            variables={vars}
            issueMessage={issueMessage}
          />
          {issues.length > 0 && (
            <ul id={issuesId} className="field-errors">
              {issues.map((i) => (
                <li key={`${i.from}-${i.kind}`}>{issueMessage(i)}</li>
              ))}
            </ul>
          )}
          <p className="secondary prompt-editor__vars">
            {t("prompts.availableVars")}{" "}
            {vars.map((v, i) => (
              <span key={v}>
                {i > 0 && t("common.listSeparator")}
                <code>{v}</code>
              </span>
            ))}
          </p>
        </section>
        <PlaygroundPanel
          id={id}
          version={selected}
          dirty={dirty}
          params={params}
          onParams={setParams}
        />
      </div>

      {pendingSwitch !== null && (
        <Dialog
          open
          onOpenChange={(o) => !o && setPendingSwitch(null)}
          title={t("prompts.discardTitle")}
          description={t("prompts.discardBody", { version: pendingSwitch })}
          footer={
            <Button
              variant="danger"
              onClick={() => {
                select(pendingSwitch);
                setPendingSwitch(null);
              }}
            >
              {t("common.discard")}
            </Button>
          }
        />
      )}
      {saveAsNew && (
        <NewPromptDialog
          from={{ id, kind: summary.kind, body }}
          onClose={() => setSaveAsNew(false)}
        />
      )}
      {comparing && (
        <CompareDialog
          id={id}
          versions={versions}
          initialB={selected}
          params={params}
          onClose={() => setComparing(false)}
        />
      )}
      {deleting && (
        <DeleteDialog
          id={id}
          versions={versions.length}
          onClose={() => setDeleting(false)}
          onDeleted={async () => {
            setDeleting(false);
            qc.removeQueries({ queryKey: queryKeys.prompts.versions(id) });
            await invalidate();
            toast.show({ title: t("prompts.deleted", { id }) });
            await navigate({ to: "/prompts" });
          }}
        />
      )}
    </>
  );
}

function PlaygroundPanel({
  id,
  version,
  dirty,
  params,
  onParams,
}: {
  id: string;
  version: number;
  dirty: boolean;
  params: RunParams;
  onParams: (p: RunParams) => void;
}) {
  const { t } = useTranslation();
  const hid = useId();
  const run = useMutation({
    mutationFn: () =>
      api.post("/api/prompts/playground", {
        body: { prompt: `${id}@${version}`, ...params },
      }) as Promise<PlaygroundResult>,
  });
  const missing = missingParams(params);
  return (
    <section className="playground" aria-labelledby={hid}>
      <h2 id={hid} className="tune-section__title">
        {t("prompts.run.title")}
      </h2>
      <RunParamsFields params={params} onChange={onParams} />
      <p className="secondary">
        {dirty
          ? t("prompts.run.usesSaved", { version })
          : t("prompts.run.usesVersion", { version })}{" "}
        {t("prompts.run.noWrite")}
      </p>
      <div className="playground__actions">
        <span className="secondary">{t("prompts.run.estimateBefore")}</span>
        <Button
          variant="outline"
          disabled={missing.length > 0 || run.isPending}
          onClick={() => run.mutate()}
        >
          {run.isPending ? t("prompts.run.running") : t("prompts.run.start")}
        </Button>
      </div>
      {missing.length > 0 && <p className="field__hint">{t("prompts.run.fillAll")}</p>}
      {run.isPending && <Spinner label={t("prompts.run.running")} showLabel />}
      {run.error && <RunError error={run.error} />}
      {run.data && <RunResult result={run.data} />}
    </section>
  );
}

/** 試跑的錯誤：501（這個類型還不能試跑）、429（retry_after_s）、409 needs_action 各有說明。 */
function RunError({ error }: { error: unknown }) {
  const { t } = useTranslation();
  let message: string | undefined;
  if (isApiError(error)) {
    if (error.status === 501) message = t("prompts.run.notSupported", { reason: error.message });
    else if (error.code === "rate_limited" && error.body.retry_after_s)
      message = t("prompts.run.rateLimited", { seconds: error.body.retry_after_s });
    else if (error.code === "needs_action")
      message = `${describeError(t, error)}${
        error.body.actions?.length
          ? ` ${t("prompts.run.actions", { actions: error.body.actions.join(t("common.listSeparator")) })}`
          : ""
      }`;
  }
  return <ErrorCallout error={error} message={message} />;
}

function CompareDialog({
  id,
  versions,
  initialB,
  params: initialParams,
  onClose,
}: {
  id: string;
  versions: PromptVersion[];
  initialB: number;
  params: RunParams;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const nums = versions.map((v) => v.version);
  const [b, setB] = useState(initialB);
  const [a, setA] = useState(nums.filter((n) => n !== initialB).at(-1) ?? nums[0]!);
  const [withOutputs, setWithOutputs] = useState(false);
  const [params, setParams] = useState(initialParams);
  const compare = useMutation({
    mutationFn: () =>
      api.post<"/api/prompts/{id}/compare">(apiPath("/api/prompts/{id}/compare", { id }), {
        body: { a, b, ...(withOutputs ? params : {}) },
      }),
  });
  const options = nums.map((n) => ({
    value: String(n),
    label: t("prompts.versionN", { version: n }),
  }));
  const missing = withOutputs ? missingParams(params) : [];
  const diff = compare.data?.body_diff ?? [];
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("prompts.compareTitle", { id })}
      footer={
        <Button
          variant="primary"
          disabled={a === b || missing.length > 0 || compare.isPending}
          onClick={() => compare.mutate()}
        >
          {compare.isPending ? t("prompts.comparing") : t("prompts.compareRun")}
        </Button>
      }
    >
      <div className="compare">
        <div className="form-row">
          <SelectField
            label={t("prompts.compareA")}
            value={String(a)}
            onChange={(e) => setA(Number(e.target.value))}
            options={options}
          />
          <SelectField
            label={t("prompts.compareB")}
            value={String(b)}
            onChange={(e) => setB(Number(e.target.value))}
            options={options}
          />
        </div>
        {a === b && <p className="field__hint">{t("prompts.compareSame")}</p>}
        <label className="check">
          <input
            type="checkbox"
            checked={withOutputs}
            onChange={(e) => setWithOutputs(e.target.checked)}
          />
          {t("prompts.compareWithOutputs")}
        </label>
        {withOutputs && (
          <>
            <p className="secondary">{t("prompts.compareCost")}</p>
            <RunParamsFields params={params} onChange={setParams} />
          </>
        )}
        {compare.error && <RunError error={compare.error} />}
        {compare.data && (
          <>
            <h3 className="compare__title">
              {t("prompts.compareDiff", { a: compare.data.a, b: compare.data.b })}
            </h3>
            <ol className="line-diff" aria-label={t("prompts.compareDiffLabel")}>
              {diff.map((l, i) => (
                <li key={i} className={`line-diff__line line-diff__line--${l.op}`}>
                  <span className="line-diff__op" aria-hidden="true">
                    {l.op === "insert" ? "+" : l.op === "delete" ? "−" : " "}
                  </span>
                  <span className="visually-hidden">
                    {l.op === "equal" ? "" : t(`prompts.diffOp.${l.op}`)}
                  </span>
                  <span className="line-diff__text">{l.text}</span>
                </li>
              ))}
            </ol>
            {compare.data.outputs && (
              <CompareOutputs a={compare.data.outputs.a} b={compare.data.outputs.b} />
            )}
          </>
        )}
      </div>
    </Dialog>
  );
}

function CompareOutputs({ a, b }: { a: PlaygroundResult; b: PlaygroundResult }) {
  const { t } = useTranslation();
  const bByIndex = new Map((b.output ?? []).map((c) => [c.index, c]));
  return (
    <>
      <h3 className="compare__title">{t("prompts.compareOutputs")}</h3>
      <div className="compare__summary">
        <div>
          <p className="field__label">{t("prompts.versionN", { version: a.prompt.version })}</p>
          <RunResult result={a} compact />
        </div>
        <div>
          <p className="field__label">{t("prompts.versionN", { version: b.prompt.version })}</p>
          <RunResult result={b} compact />
        </div>
      </div>
      <table className="compare__table">
        <thead>
          <tr>
            <th scope="col">{t("prompts.compareTime")}</th>
            <th scope="col">{t("prompts.versionN", { version: a.prompt.version })}</th>
            <th scope="col">{t("prompts.versionN", { version: b.prompt.version })}</th>
          </tr>
        </thead>
        <tbody>
          {(a.output ?? []).map((c) => (
            <tr key={c.index}>
              <td className="num">{formatTimecode(c.start_ms / 1000)}</td>
              <td>{c.text}</td>
              <td>{bByIndex.get(c.index)?.text ?? ""}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}

function DeleteDialog({
  id,
  versions,
  onClose,
  onDeleted,
}: {
  id: string;
  versions: number;
  onClose: () => void;
  onDeleted: () => Promise<void>;
}) {
  const { t } = useTranslation();
  const del = useMutation({
    mutationFn: () => api.delete<"/api/prompts/{id}">(apiPath("/api/prompts/{id}", { id })),
    onSuccess: onDeleted,
  });
  const inUse = isApiError(del.error) && del.error.code === "prompt_in_use";
  const usedBy = inUse && isApiError(del.error) ? (del.error.body.used_by ?? []) : [];
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("prompts.deleteTitle", { id })}
      description={t("prompts.deleteBody", { count: versions })}
      footer={
        !inUse && (
          <Button variant="danger" disabled={del.isPending} onClick={() => del.mutate()}>
            {t("prompts.deleteConfirm")}
          </Button>
        )
      }
    >
      {inUse && (
        <div className="delete-blocked" role="alert">
          <p>{t("prompts.inUse")}</p>
          <ul>
            {usedBy.map((u) => (
              <li key={u}>
                <UsedByEntry entry={u} />
              </li>
            ))}
          </ul>
          <p className="secondary">{t("prompts.inUseNext")}</p>
        </div>
      )}
      {del.error && !inUse && <ErrorCallout error={del.error} />}
    </Dialog>
  );
}

/** used_by 的一筆：處理方案名稱連到它的編輯頁；settings、channel:…、item:…、「另有 N 個」原樣顯示（純文字）。 */
function UsedByEntry({ entry }: { entry: string }) {
  const { t } = useTranslation();
  if (entry === "settings") return <>{t("prompts.usedBySettings")}</>;
  if (entry.startsWith("channel:"))
    return <>{t("prompts.usedByChannel", { channel: entry.slice("channel:".length) })}</>;
  if (entry.startsWith("item:")) {
    const ref = entry.slice("item:".length);
    return (
      <Link to="/items/$id" params={{ id: ref }}>
        {t("prompts.usedByItem", { item: ref })}
      </Link>
    );
  }
  if (/^[a-z0-9][a-z0-9_-]{0,63}$/.test(entry))
    return (
      <Link to="/profiles/$id" params={{ id: entry }}>
        {t("prompts.usedByProfile", { name: entry })}
      </Link>
    );
  return <>{entry}</>;
}
