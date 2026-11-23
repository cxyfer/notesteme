// Prompt 庫列表（docs/ui/pages.md §7）：依用途分組；每個 prompt 顯示目前版本、被哪些處理方案使用、最後修改時間。
// 「被哪些處理方案使用」：API 沒有這項資料，用 GET /api/profiles 在前端組合（只看處理方案，見 docs/ui/README.md 10.x）。
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import { Button, EmptyState, ErrorCallout, Spinner } from "../../components";
import { formatDateTime } from "../../i18n";
import { usePageTitle } from "../../shell/usePageTitle";
import { promptUsers } from "../profiles/profileModel";
import { NewPromptDialog } from "./NewPromptDialog";
import { groupOf, PROMPT_GROUPS, type PromptSummary } from "./promptModel";

export function PromptsPage() {
  const { t } = useTranslation();
  usePageTitle(t("pages.prompts.title"));
  const prompts = useQuery({
    queryKey: queryKeys.prompts.list(),
    queryFn: ({ signal }) => api.get("/api/prompts", { signal }),
  });
  const profiles = useQuery({
    queryKey: queryKeys.profiles,
    queryFn: ({ signal }) => api.get("/api/profiles", { signal }),
  });
  const [creating, setCreating] = useState(false);
  const users = promptUsers(profiles.data?.profiles ?? []);
  const list = prompts.data?.prompts ?? [];
  return (
    <>
      <div className="page__head">
        <h1 className="page__title">{t("pages.prompts.title")}</h1>
        <Button onClick={() => setCreating(true)}>{t("prompts.new")}</Button>
      </div>
      <p className="secondary page__lead">{t("prompts.lead")}</p>
      {prompts.isPending && <Spinner label={t("common.loading")} />}
      {prompts.error && (
        <ErrorCallout
          error={prompts.error}
          actions={[{ label: t("common.retry"), onClick: () => void prompts.refetch() }]}
        />
      )}
      {prompts.data && list.length === 0 && (
        <EmptyState
          message={t("prompts.empty")}
          action={{ label: t("prompts.new"), onClick: () => setCreating(true) }}
        />
      )}
      {PROMPT_GROUPS.map((g) => {
        const items = list.filter((p) => groupOf(p.kind) === g);
        if (items.length === 0) return null;
        return (
          <section key={g} className="tune-section" aria-labelledby={`prompts-${g}`}>
            <h2 id={`prompts-${g}`} className="tune-section__title">
              {t(`prompts.group.${g}`)}
            </h2>
            <ul className="tune-list">
              {items.map((p) => (
                <PromptRow
                  key={p.id}
                  prompt={p}
                  usedBy={users.get(p.id) ?? []}
                  usersKnown={!!profiles.data}
                />
              ))}
            </ul>
          </section>
        );
      })}
      {creating && <NewPromptDialog onClose={() => setCreating(false)} />}
    </>
  );
}

function PromptRow({
  prompt,
  usedBy,
  usersKnown,
}: {
  prompt: PromptSummary;
  usedBy: string[];
  usersKnown: boolean;
}) {
  const { t } = useTranslation();
  return (
    <li className="tune-row">
      <div className="tune-row__main">
        <Link to="/prompts/$id" params={{ id: prompt.id }} className="tune-row__title">
          {prompt.id}
        </Link>
        {prompt.builtin && <span className="tag">{t("prompts.builtinTag")}</span>}
        {groupOf(prompt.kind) === "other" && <span className="tag">{prompt.kind}</span>}
        <span className="secondary num">
          {t("prompts.currentVersion", { version: prompt.default_version })}
          {prompt.latest_version !== prompt.default_version &&
            ` ${t("prompts.latestVersion", { version: prompt.latest_version })}`}
        </span>
      </div>
      <p className="tune-row__meta secondary">
        {!usersKnown ? null : usedBy.length === 0 ? (
          t("prompts.unused")
        ) : (
          <>
            {t("prompts.usedBy")}{" "}
            {usedBy.map((name, i) => (
              <span key={name}>
                {i > 0 && t("common.listSeparator")}
                <Link to="/profiles/$id" params={{ id: name }}>
                  {name}
                </Link>
              </span>
            ))}
          </>
        )}
      </p>
      <p className="tune-row__meta secondary">
        {t("prompts.updatedAt", { at: formatDateTime(prompt.updated_at) })}
      </p>
    </li>
  );
}
