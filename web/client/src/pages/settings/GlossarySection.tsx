// 詞彙表（pages.md §12、GLOS、COR-R5）：全域與各頻道的詞彙表；頻道的術語候選（接受、拒絕）。
//
// 後端沒有頻道列表 API，頻道用 UUID 或 <platform>:<external_id>（例如 youtube:UCxxxx）輸入；
// 選擇只放在元件的 state，不加 route 的 search 參數。
// UI-5：最上面加上跨頻道的術語審核（GLOS-R7：改寫後接受、加到頻道或全域、多選），lazy 載入（TermReview.tsx）。
import { Link } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { lazy, Suspense, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, ErrorCallout, TextField, useToast } from "../../components";
import { formatDateTime } from "../../i18n";
import { Loading, Panel, SaveError } from "./shared";

type Glossary = components["schemas"]["Glossary"];
type Candidate = components["schemas"]["TermCandidate"];

const TermReview = lazy(() => import("./TermReview").then((m) => ({ default: m.TermReview })));

const GLOBAL = "global";
const GLOBAL_ID = "default";
const CHANNEL = "channel";
const ACCEPTED = "accepted";
const REJECTED = "rejected";

export function GlossarySection() {
  const { t } = useTranslation();
  const [input, setInput] = useState("");
  const [channel, setChannel] = useState<string>();

  function onOpen(e: FormEvent) {
    e.preventDefault();
    const v = input.trim();
    if (v) setChannel(v);
  }

  return (
    <>
      <Panel
        title={t("settings.glossary.review.title")}
        description={t("settings.glossary.review.hint")}
      >
        <Suspense fallback={<Loading />}>
          <TermReview />
        </Suspense>
      </Panel>
      <Panel
        title={t("settings.glossary.globalTitle")}
        description={t("settings.glossary.globalHint")}
      >
        <GlossaryEditor scope={GLOBAL} id={GLOBAL_ID} />
      </Panel>
      <Panel
        title={t("settings.glossary.channelTitle")}
        description={t("settings.glossary.channelHint")}
      >
        <form className="settings-inline-form" onSubmit={onOpen} noValidate>
          <TextField
            label={t("settings.glossary.channelLabel")}
            hint={t("settings.glossary.channelFormat")}
            autoComplete="off"
            spellCheck={false}
            value={input}
            onChange={(e) => setInput(e.target.value)}
          />
          <div className="settings-actions">
            <Button type="submit" disabled={input.trim() === ""}>
              {t("settings.glossary.open")}
            </Button>
          </div>
        </form>
      </Panel>
      {channel && (
        <>
          <Panel title={t("settings.glossary.channelGlossary", { channel })}>
            <GlossaryEditor key={channel} scope={CHANNEL} id={channel} />
          </Panel>
          <Panel
            title={t("settings.glossary.candidatesTitle", { channel })}
            description={t("settings.glossary.candidatesHint")}
          >
            <Candidates key={channel} channel={channel} />
          </Panel>
        </>
      )}
    </>
  );
}

function GlossaryEditor({ scope, id }: { scope: "global" | "channel"; id: string }) {
  const { t } = useTranslation();
  const query = useQuery({
    queryKey: queryKeys.glossary(scope, id),
    queryFn: () =>
      api.get<"/api/glossaries/{scope}/{id}">(
        apiPath("/api/glossaries/{scope}/{id}", { scope, id }),
      ),
  });
  if (query.isPending) return <Loading />;
  if (query.isError)
    return (
      <ErrorCallout
        error={query.error}
        actions={[{ label: t("common.retry"), onClick: () => void query.refetch() }]}
      />
    );
  // key：伺服器的內容改變（例如接受了候選）時重設編輯框
  return (
    <TermsForm
      key={`${query.data.updated_at ?? ""}:${(query.data.terms ?? []).length}`}
      scope={scope}
      id={id}
      glossary={query.data}
    />
  );
}

function TermsForm({
  scope,
  id,
  glossary,
}: {
  scope: "global" | "channel";
  id: string;
  glossary: Glossary;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const [text, setText] = useState((glossary.terms ?? []).join("\n"));
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const fieldId = `glossary-${scope}-${id}`;
  const terms = text
    .split("\n")
    .map((s) => s.trim())
    .filter(Boolean);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setPending(true);
    setError(undefined);
    try {
      const out = await api.put<"/api/glossaries/{scope}/{id}">(
        apiPath("/api/glossaries/{scope}/{id}", { scope, id }),
        {
          body: { terms },
        },
      );
      qc.setQueryData(queryKeys.glossary(scope, id), out);
      toast.show({ title: t("settings.glossary.saved", { count: (out.terms ?? []).length }) });
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }

  return (
    <form className="settings-form" onSubmit={onSubmit} noValidate>
      <div className="field">
        <label className="field__label" htmlFor={fieldId}>
          {t("settings.glossary.terms")}
        </label>
        <textarea
          id={fieldId}
          className="field__input settings-textarea"
          rows={8}
          spellCheck={false}
          value={text}
          aria-describedby={`${fieldId}-hint`}
          onChange={(e) => setText(e.target.value)}
        />
        <span id={`${fieldId}-hint`} className="field__hint">
          {t("settings.glossary.termsHint", { count: terms.length })}
          {glossary.updated_at
            ? ` ${t("settings.glossary.updatedAt", { time: formatDateTime(glossary.updated_at) })}`
            : ""}
        </span>
      </div>
      {error !== undefined && <SaveError error={error} />}
      <div className="settings-actions">
        <Button
          type="submit"
          variant={scope === "global" ? "primary" : "outline"}
          disabled={pending}
        >
          {t("settings.glossary.save")}
        </Button>
      </div>
    </form>
  );
}

function Candidates({ channel }: { channel: string }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const query = useQuery({
    queryKey: queryKeys.termCandidates(channel),
    queryFn: () =>
      api.get<"/api/channels/{id}/term-candidates">(
        apiPath("/api/channels/{id}/term-candidates", { id: channel }),
      ),
  });
  const [busy, setBusy] = useState<string>();
  const [error, setError] = useState<unknown>();
  const [stale, setStale] = useState<string>();

  async function decide(c: Candidate, state: "accepted" | "rejected") {
    setBusy(c.id);
    setError(undefined);
    setStale(undefined);
    try {
      await api.patch<"/api/channels/{id}/term-candidates/{candidate}">(
        apiPath("/api/channels/{id}/term-candidates/{candidate}", { id: channel, candidate: c.id }),
        { body: { state } },
      );
      toast.show({
        title: t(
          state === "accepted" ? "settings.glossary.accepted" : "settings.glossary.rejected",
          { term: c.term },
        ),
      });
      if (state === "accepted") {
        await qc.invalidateQueries({ queryKey: queryKeys.glossary("channel", channel) });
      }
    } catch (err) {
      // 409 term_candidate_decided：已經在別的地方（另一個分頁、CLI、MCP）決定了，重新讀取
      if (isApiError(err) && err.code === "term_candidate_decided") setStale(c.term);
      else setError(err);
    } finally {
      setBusy(undefined);
      await qc.invalidateQueries({ queryKey: queryKeys.termCandidates(channel) });
    }
  }

  if (query.isPending) return <Loading />;
  if (query.isError)
    return (
      <ErrorCallout
        error={query.error}
        actions={[{ label: t("common.retry"), onClick: () => void query.refetch() }]}
      />
    );
  const all = query.data.candidates ?? [];
  const pending = all.filter((c) => c.state === "pending");
  const decided = all.filter((c) => c.state !== "pending");
  return (
    <>
      {stale && (
        <p className="settings-note" role="status">
          {t("settings.glossary.alreadyDecided", { term: stale })}
        </p>
      )}
      {error !== undefined && <ErrorCallout error={error} />}
      {pending.length === 0 ? (
        <p className="settings-panel__description">{t("settings.glossary.noCandidates")}</p>
      ) : (
        <ul className="settings-rows">
          {pending.map((c) => (
            <li key={c.id} className="settings-row">
              <div className="settings-row__main">
                <span className="settings-row__name">{c.term}</span>
                <span className="settings-row__meta">
                  {formatDateTime(c.created_at)}
                  {c.item_id && (
                    <>
                      {" · "}
                      <Link to="/items/$id" params={{ id: c.item_id }}>
                        {t("settings.glossary.fromItem")}
                      </Link>
                    </>
                  )}
                </span>
              </div>
              <div className="settings-row__actions">
                <Button
                  disabled={busy !== undefined}
                  aria-label={t("settings.glossary.acceptNamed", { term: c.term })}
                  onClick={() => void decide(c, ACCEPTED)}
                >
                  {t("settings.glossary.accept")}
                </Button>
                <Button
                  variant="text"
                  disabled={busy !== undefined}
                  aria-label={t("settings.glossary.rejectNamed", { term: c.term })}
                  onClick={() => void decide(c, REJECTED)}
                >
                  {t("settings.glossary.reject")}
                </Button>
              </div>
            </li>
          ))}
        </ul>
      )}
      {decided.length > 0 && (
        <details className="settings-details">
          <summary>{t("settings.glossary.decidedSummary", { count: decided.length })}</summary>
          <ul className="settings-rows">
            {decided.map((c) => (
              <li key={c.id} className="settings-row">
                <span className="settings-row__name">{c.term}</span>
                <span className="settings-row__meta">
                  {t(`settings.glossary.state.${c.state}`)}
                </span>
              </li>
            ))}
          </ul>
        </details>
      )}
    </>
  );
}
