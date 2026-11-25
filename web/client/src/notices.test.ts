// public/third-party-notices.txt（隨 binary 散布的字型與圖示授權）要和目前安裝的套件版本與 LICENSE 一致。
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { describe, expect, it } from "vitest";

// @ts-expect-error -- 純 JS 的 build script，沒有型別宣告
import { PACKAGES, renderNotices } from "../scripts/gen-notices.mjs";

describe("第三方授權聲明", () => {
  it("public/third-party-notices.txt 是最新的（不一致時執行 npm run gen:notices）", () => {
    expect(readFileSync("public/third-party-notices.txt", "utf8")).toBe(
      // vitest 的工作目錄是 web/client
      renderNotices(pathToFileURL(`${process.cwd()}/`)),
    );
  });

  it("涵蓋 package.json 中每個字型與圖示套件", () => {
    const pkg = JSON.parse(readFileSync("package.json", "utf8")) as {
      dependencies: Record<string, string>;
    };
    const bundled = Object.keys(pkg.dependencies).filter(
      (n) => n.startsWith("@fontsource/") || n.startsWith("@phosphor-icons/"),
    );
    expect([...(PACKAGES as string[])].sort()).toEqual(bundled.sort());
  });
});
