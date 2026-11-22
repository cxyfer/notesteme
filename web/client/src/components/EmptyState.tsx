// 空狀態（docs/ui/README.md §4.3）：說明可以做什麼，並附上按鈕。
import type { ReactNode } from "react";

import { Button } from "./Button";

export interface EmptyStateProps {
  message: ReactNode;
  action?: { label: string; onClick: () => void };
}

export function EmptyState({ message, action }: EmptyStateProps) {
  return (
    <div className="empty">
      <p className="empty__message">{message}</p>
      {action && (
        <Button variant="outline" onClick={action.onClick}>
          {action.label}
        </Button>
      )}
    </div>
  );
}
