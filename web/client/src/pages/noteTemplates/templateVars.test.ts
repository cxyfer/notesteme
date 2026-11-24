import { describe, expect, it } from "vitest";

import { scanTemplateVariables } from "./templateVars";

describe("筆記模板的變數掃描", () => {
  it("清單中的變數高亮，不認識的標出來；關鍵字、字串與註解不標", () => {
    const body =
      '{{.Frontmatter}}\n{{- if .Item.URL }}[原片]({{ .Item.URL }}){{else}}{{.Secret}}{{end -}}\n{{if eq .Lang "a.b"}}{{.Title}}{{end}}{{/* .Note */}}';
    const { tokens, issues } = scanTemplateVariables(body);
    expect(tokens.map((x) => [x.name, x.known])).toEqual([
      [".Frontmatter", true],
      [".Item.URL", true],
      [".Item.URL", true],
      [".Secret", false],
      [".Lang", true],
      [".Title", true],
    ]);
    expect(issues).toEqual([
      {
        from: body.indexOf(".Secret"),
        to: body.indexOf(".Secret") + 7,
        kind: "undefined",
        name: ".Secret",
      },
    ]);
  });

  it("沒有對應的 }} 時標成無法解析，之後不再找", () => {
    const { issues } = scanTemplateVariables("{{.Title}} {{.Secret");
    expect(issues).toEqual([{ from: 11, to: 20, kind: "unclosed", name: "" }]);
  });
});
