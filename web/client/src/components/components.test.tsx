import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import { ApiError } from "../api/errors";
import { expectNoA11yViolations } from "../test/axe";
import {
  Button,
  Dialog,
  Drawer,
  ErrorCallout,
  Spinner,
  STATUSES,
  StatusBadge,
  TextField,
  ToastProvider,
  useToast,
} from "./index";

describe("Button", () => {
  it("預設 type=button，variant 對應 class", async () => {
    const { container } = render(<Button variant="primary">開始處理</Button>);
    const b = screen.getByRole("button", { name: "開始處理" });
    expect(b.getAttribute("type")).toBe("button");
    expect(b.className).toContain("button--primary");
    await expectNoA11yViolations(container);
  });
});

describe("TextField", () => {
  it("標籤、說明與錯誤都連到輸入框", async () => {
    const { container } = render(
      <TextField label="密碼" hint="至少 8 個字" error="密碼不正確。" />,
    );
    const input = screen.getByLabelText("密碼");
    expect(input.getAttribute("aria-invalid")).toBe("true");
    const ids = input.getAttribute("aria-describedby")!.split(" ");
    expect(ids.map((id) => document.getElementById(id)?.textContent)).toEqual([
      "至少 8 個字",
      "密碼不正確。",
    ]);
    await expectNoA11yViolations(container);
  });
});

function DialogHarness({ drawer = false }: { drawer?: boolean }) {
  const [open, setOpen] = useState(false);
  const C = drawer ? Drawer : Dialog;
  return (
    <>
      <Button onClick={() => setOpen(true)}>開啟</Button>
      <C open={open} onOpenChange={setOpen} title="新增" description="貼上網址">
        <TextField label="網址" />
      </C>
    </>
  );
}

describe.each([false, true])("Dialog／Drawer（drawer=%s）", (drawer) => {
  it("開啟後焦點在對話框內，Esc 關閉並把焦點還給觸發按鈕", async () => {
    render(<DialogHarness drawer={drawer} />);
    const trigger = screen.getByRole("button", { name: "開啟" });
    await userEvent.click(trigger);
    const dialog = await screen.findByRole("dialog", { name: "新增" });
    expect(dialog.contains(document.activeElement)).toBe(true);
    await expectNoA11yViolations(document.body);
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(trigger));
  });
});

function ToastHarness({ onUndo }: { onUndo: () => void }) {
  const toast = useToast();
  return (
    <Button
      onClick={() => toast.show({ title: "已移除", action: { label: "復原", onClick: onUndo } })}
    >
      移除
    </Button>
  );
}

describe("Toast", () => {
  it("顯示訊息與動作按鈕", async () => {
    const onUndo = vi.fn();
    render(
      <ToastProvider>
        <ToastHarness onUndo={onUndo} />
      </ToastProvider>,
    );
    await userEvent.click(screen.getByRole("button", { name: "移除" }));
    expect(await screen.findByText("已移除")).toBeTruthy();
    await expectNoA11yViolations(document.body);
    await userEvent.click(screen.getByRole("button", { name: "復原" }));
    expect(onUndo).toHaveBeenCalledOnce();
  });
});

describe("Spinner", () => {
  it("有 status 角色與文字", async () => {
    const { container } = render(<Spinner />);
    expect(screen.getByRole("status").textContent).toBe("載入中…");
    await expectNoA11yViolations(container);
  });
});

describe("StatusBadge", () => {
  it("每個狀態都有文字與不同的圖示", async () => {
    const { container } = render(
      <ul>
        {STATUSES.map((s) => (
          <li key={s}>
            <StatusBadge status={s} />
          </li>
        ))}
      </ul>,
    );
    expect(screen.getByText("需要你處理")).toBeTruthy();
    expect(screen.getByText("等待 Agent")).toBeTruthy();
    const icons = new Set([...container.querySelectorAll("svg")].map((s) => s.innerHTML));
    expect(icons.size).toBe(STATUSES.length);
    await expectNoA11yViolations(container);
  });
});

describe("ErrorCallout", () => {
  it("顯示類別、訊息與修正動作；字典有 code 時用字典的文字", async () => {
    const onFix = vi.fn();
    const { container } = render(
      <ErrorCallout
        error={new ApiError(0, "network_error", "Failed to fetch")}
        actions={[{ label: "重試", onClick: onFix, primary: true }]}
      />,
    );
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain("暫時無法完成");
    expect(alert.textContent).toContain("無法連線到伺服器");
    await userEvent.click(screen.getByRole("button", { name: "重試" }));
    expect(onFix).toHaveBeenCalledOnce();
    await expectNoA11yViolations(container);
  });

  it("字典沒有的 code 用後端的訊息；都沒有時顯示 code", () => {
    render(
      <>
        <ErrorCallout error={new ApiError(409, "profile_exists", "處理方案已存在。")} />
        <ErrorCallout error={new ApiError(500, "weird_code", "")} />
      </>,
    );
    const [a, b] = screen.getAllByRole("alert");
    expect(a!.textContent).toContain("需要你處理");
    expect(a!.textContent).toContain("處理方案已存在。");
    expect(b!.textContent).toContain("weird_code");
  });
});
