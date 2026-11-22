// 狀態標籤（design-language.md §6.4）：每個狀態都有不同形狀的圖示加文字，不只靠顏色（NFR-A11Y-3）。
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

export const STATUSES = [
  "pending",
  "running",
  "waiting",
  "awaiting_agent",
  "done",
  "skipped",
  "failed",
  "needs_action",
  "budget_blocked",
  "awaiting_user",
] as const;
export type Status = (typeof STATUSES)[number];

const ring = <circle cx="8" cy="8" r="6.25" fill="none" stroke="currentColor" strokeWidth="1.5" />;

const ICONS: Record<Status, ReactNode> = {
  pending: ring,
  running: (
    <>
      {ring}
      <path d="M8 1.75a6.25 6.25 0 0 1 0 12.5Z" fill="currentColor" />
    </>
  ),
  waiting: (
    <>
      {ring}
      <path
        d="M8 4.5V8l2.5 1.5"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
      />
    </>
  ),
  awaiting_agent: (
    <>
      <rect
        x="2.5"
        y="4"
        width="11"
        height="9"
        rx="2"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
      />
      <path
        d="M8 1.5V4M6 8.5h.01M10 8.5h.01"
        stroke="currentColor"
        strokeWidth="1.8"
        strokeLinecap="round"
      />
    </>
  ),
  done: (
    <>
      <circle cx="8" cy="8" r="7" fill="currentColor" />
      <path
        d="m4.75 8.25 2.25 2.25 4.25-4.5"
        fill="none"
        stroke="var(--surface-panel)"
        strokeWidth="1.6"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </>
  ),
  skipped: (
    <>
      {ring}
      <path d="m3.6 12.4 8.8-8.8" stroke="currentColor" strokeWidth="1.5" />
    </>
  ),
  failed: (
    <path d="m3.5 3.5 9 9m0-9-9 9" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
  ),
  needs_action: (
    <>
      {ring}
      <path
        d="M8 4.5v4.25M8 11.25h.01"
        stroke="currentColor"
        strokeWidth="1.8"
        strokeLinecap="round"
      />
    </>
  ),
  budget_blocked: (
    <>
      <rect
        x="1.75"
        y="3.5"
        width="12.5"
        height="9.5"
        rx="1.5"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
      />
      <path d="M10.5 8.25h1.5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
    </>
  ),
  awaiting_user: (
    <>
      {ring}
      <path
        d="M6.25 6.25a1.75 1.75 0 1 1 2.4 1.62c-.4.17-.65.55-.65.98v.4M8 11.25h.01"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
      />
    </>
  ),
};

export function StatusBadge({ status, label }: { status: Status; label?: string }) {
  const { t } = useTranslation();
  return (
    <span className={`status status--${status}`} data-status={status}>
      <svg className="status__icon" viewBox="0 0 16 16" aria-hidden="true" focusable="false">
        {ICONS[status]}
      </svg>
      <span>{label ?? t(`status.${status}`)}</span>
    </span>
  );
}
