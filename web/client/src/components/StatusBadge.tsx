// 狀態標籤（design-language.md §6.4）：每個狀態都有不同形狀的圖示加文字，不只靠顏色（NFR-A11Y-3）。
import { useTranslation } from "react-i18next";

import { Icon, type IconComponent } from "./Icon";
import {
  CheckCircle,
  Circle,
  CircleHalf,
  Clock,
  Prohibit,
  Question,
  Robot,
  Wallet,
  WarningCircle,
  X,
} from "./icons";

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

// §6.4 的形狀對應到 Phosphor 圖示（§5.4 的 Regular；「完成」是 §6.4 指定的實心勾，用 fill）
const ICONS: Record<Status, { icon: IconComponent; weight?: "fill" }> = {
  pending: { icon: Circle }, // 空心圓
  running: { icon: CircleHalf }, // 半實心圓
  waiting: { icon: Clock }, // 時鐘
  awaiting_agent: { icon: Robot }, // Agent
  done: { icon: CheckCircle, weight: "fill" }, // 實心勾
  skipped: { icon: Prohibit }, // 斜線圓
  failed: { icon: X }, // 叉號
  needs_action: { icon: WarningCircle }, // 驚嘆號
  budget_blocked: { icon: Wallet }, // 錢包
  awaiting_user: { icon: Question }, // 問號
};

export function StatusBadge({ status, label }: { status: Status; label?: string }) {
  const { t } = useTranslation();
  return (
    <span className={`status status--${status}`} data-status={status}>
      <Icon
        icon={ICONS[status].icon}
        weight={ICONS[status].weight}
        size={16}
        className="status__icon"
      />
      <span>{label ?? t(`status.${status}`)}</span>
    </span>
  );
}
