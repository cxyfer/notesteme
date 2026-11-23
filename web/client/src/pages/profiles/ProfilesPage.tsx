// 處理方案列表（docs/ui/pages.md §8）：名稱、預設、目前版本、各步驟的執行方式、最後修改時間。
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import { formatDateTime } from "../../i18n";
import { Button, EmptyState, ErrorCallout, Spinner } from "../../components";
import { usePageTitle } from "../../shell/usePageTitle";
import { NewProfileDialog } from "./ProfileDialogs";
import { LLM_STAGES, stageExecutor, type Profile } from "./profileModel";

export function ProfilesPage() {
  const { t } = useTranslation();
  usePageTitle(t("pages.profiles.title"));
  const profiles = useQuery({
    queryKey: queryKeys.profiles,
    queryFn: ({ signal }) => api.get("/api/profiles", { signal }),
  });
  const [creating, setCreating] = useState(false);
  const list = profiles.data?.profiles ?? [];
  return (
    <>
      <div className="page__head">
        <h1 className="page__title">{t("pages.profiles.title")}</h1>
        <Button onClick={() => setCreating(true)}>{t("profiles.new")}</Button>
      </div>
      <p className="secondary page__lead">{t("profiles.lead")}</p>
      {profiles.isPending && <Spinner label={t("common.loading")} />}
      {profiles.error && (
        <ErrorCallout
          error={profiles.error}
          actions={[{ label: t("common.retry"), onClick: () => void profiles.refetch() }]}
        />
      )}
      {profiles.data && list.length === 0 && (
        <EmptyState
          message={t("profiles.empty")}
          action={{ label: t("profiles.new"), onClick: () => setCreating(true) }}
        />
      )}
      {list.length > 0 && (
        <ul className="tune-list">
          {list.map((p) => (
            <ProfileRow key={p.name} profile={p} />
          ))}
        </ul>
      )}
      {creating && <NewProfileDialog onClose={() => setCreating(false)} />}
    </>
  );
}

function ProfileRow({ profile }: { profile: Profile }) {
  const { t } = useTranslation();
  const steps = LLM_STAGES.map((s) => {
    const ex = stageExecutor(profile.config, s);
    return `${t(`stage.${s}`)} ${ex ? t(`executor.${ex}`) : t("profiles.inherit")}`;
  }).join(t("common.listSeparator"));
  return (
    <li className="tune-row">
      <div className="tune-row__main">
        <Link to="/profiles/$id" params={{ id: profile.name }} className="tune-row__title">
          {profile.name}
        </Link>
        {profile.default && <span className="tag tag--strong">{t("profiles.defaultTag")}</span>}
        {profile.builtin && <span className="tag">{t("profiles.builtinTag")}</span>}
        <span className="secondary num">{t("profiles.version", { version: profile.version })}</span>
      </div>
      <p className="tune-row__meta secondary">{steps}</p>
      <p className="tune-row__meta secondary">
        {t("profiles.updatedAt", { at: formatDateTime(profile.updated_at) })}
      </p>
    </li>
  );
}
