// 圖示（design-language.md §5.4、§6.4）：Icon 元件、狀態圖示的形狀、打包時只留 Regular 字重。
import { render } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

import { keepRegularWeight } from "../../scripts/phosphor-regular";
import { expectNoA11yViolations } from "../test/axe";
import { Icon } from "./Icon";
import { X } from "./icons";
import { STATUSES, StatusBadge } from "./StatusBadge";

const DEFS = "node_modules/@phosphor-icons/react/dist/defs";

describe("Icon", () => {
  it("aria-hidden、不可聚焦、顏色繼承文字色；預設 20px，與文字一起時 16px", () => {
    const { container } = render(
      <p>
        <Icon icon={X} />
        <Icon icon={X} size={16} className="extra" />
        關閉
      </p>,
    );
    const [a, b] = [...container.querySelectorAll("svg")];
    for (const svg of [a!, b!]) {
      expect(svg.getAttribute("aria-hidden")).toBe("true");
      expect(svg.getAttribute("focusable")).toBe("false");
      expect(svg.getAttribute("fill")).toBe("currentColor");
      expect(svg.getAttribute("viewBox")).toBe("0 0 256 256");
    }
    expect(a!.getAttribute("width")).toBe("20");
    expect(b!.getAttribute("width")).toBe("16");
    expect(b!.getAttribute("class")).toBe("icon extra");
  });

  it("每個狀態的圖示形狀都不同（不只靠顏色，NFR-A11Y-3）；文字仍在（axe 無違規）", async () => {
    const { container } = render(
      <ul>
        {STATUSES.map((s) => (
          <li key={s}>
            <StatusBadge status={s} />
          </li>
        ))}
      </ul>,
    );
    const shapes = [...container.querySelectorAll(".status__icon")].map((svg) => svg.innerHTML);
    expect(shapes).toHaveLength(STATUSES.length);
    expect(new Set(shapes).size).toBe(STATUSES.length);
    expect(container.querySelector('[data-status="done"]')?.textContent).toBe("完成");
    await expectNoA11yViolations(container);
  });
});

describe("打包時只留 Regular（scripts/phosphor-regular.ts）", () => {
  const weights = (code: string) => [...code.matchAll(/\n {2}\[\n {4}"(\w+)",/g)].map((m) => m[1]);

  it("一般圖示只剩 regular", () => {
    const raw = readFileSync(`${DEFS}/House.es.js`, "utf8");
    expect(weights(raw)).toEqual(["bold", "duotone", "fill", "light", "regular", "thin"]);
    const out = keepRegularWeight(raw, "House");
    expect(weights(out)).toEqual(["regular"]);
    expect(out.length).toBeLessThan(raw.length / 3);
    // 留下的 regular 與原本的一字不差
    const regular = /\n {2}\[\n {4}"regular",\n[\s\S]*?\n {2}\]/;
    expect(regular.exec(out)?.[0]).toBe(regular.exec(raw)?.[0]);
  });

  it("「完成」的實心勾（§6.4）另外保留 fill", () => {
    const out = keepRegularWeight(readFileSync(`${DEFS}/CheckCircle.es.js`, "utf8"), "CheckCircle");
    expect(weights(out)).toEqual(["fill", "regular"]);
  });

  it("格式不符預期時原樣保留", () => {
    expect(keepRegularWeight("export default 1;", "House")).toBe("export default 1;");
  });

  it("用到的圖示都從單一圖示的路徑匯入（不整包匯入）", () => {
    for (const f of ["src/components/icons.ts", "src/shell/navIcons.ts"]) {
      const src = readFileSync(f, "utf8");
      expect(src).not.toMatch(/from "@phosphor-icons\/react"/);
      expect(src).toMatch(/@phosphor-icons\/react\/dist\/csr\//);
    }
  });
});
