// ESLint（flat config）。重點規則：
//   - react-hooks：hooks 的規則。
//   - i18next/no-literal-string：JSX 中不可寫死使用者可見的字串（NFR-I18N-1），一律用 t()。
//   - 禁止 dangerouslySetInnerHTML（NFR-SEC-2）與 eval／new Function（CSP 不允許 unsafe-eval）。
import js from "@eslint/js";
import i18next from "eslint-plugin-i18next";
import reactHooks from "eslint-plugin-react-hooks";
import globals from "globals";
import tseslint from "typescript-eslint";

export default tseslint.config(
  { ignores: ["dist", "node_modules", "coverage", "src/api/schema.d.ts"] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ["**/*.{ts,tsx}"],
    languageOptions: { globals: globals.browser },
    plugins: { "react-hooks": reactHooks, i18next },
    rules: {
      ...reactHooks.configs.recommended.rules,
      "no-eval": "error",
      "no-implied-eval": "error",
      "no-new-func": "error",
      "no-restricted-syntax": [
        "error",
        {
          selector: "JSXAttribute[name.name='dangerouslySetInnerHTML']",
          message:
            "不可使用 dangerouslySetInnerHTML（NFR-SEC-2）：不可信的文字一律以文字節點輸出。",
        },
      ],
      "i18next/no-literal-string": [
        "error",
        {
          mode: "jsx-only",
          "jsx-attributes": {
            exclude: [
              "className",
              "style",
              "type",
              "key",
              "id",
              "width",
              "height",
              "to",
              "href",
              "variant",
              "size",
              "name",
              "autoComplete",
              "role",
              "lang",
              "side",
              "status",
              "rel",
              "method",
              "inputMode",
              "viewBox",
              "d",
              "fill",
              "stroke",
              "tone",
              "kind",
              "swipeDirection",
              "category",
              "htmlFor",
              "data-.*",
              "aria-hidden",
              "aria-live",
              "aria-current",
              "aria-invalid",
              "aria-describedby",
              "aria-labelledby",
              "aria-controls",
              // UI-3a：事件處理器裡的字串是程式用的（欄位路徑、列舉值），不是顯示的文字
              "on[A-Z].*",
              "form",
              "accept",
              "list",
              "stage",
              "path",
            ],
          },
        },
      ],
    },
  },
  {
    // 測試與設定檔不受 i18n 規則限制。
    files: ["**/*.test.{ts,tsx}", "src/test/**", "*.config.{js,ts}", "scripts/**"],
    rules: { "i18next/no-literal-string": "off" },
  },
  {
    files: ["scripts/**/*.mjs", "*.config.js"],
    languageOptions: { globals: globals.node },
  },
);
