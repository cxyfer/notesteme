// Prompt 的編輯器（CodeMirror 6）：{{變數}} 高亮，未定義的變數與無法解析的寫法用 lint 標成錯誤。
// 只在 /prompts/$id 與 /note-templates/$id（lazy route）載入，不進初始 bundle。內容一律是純文字（CodeMirror 不解析 HTML）。
// 變數的寫法由 scan 決定：預設是 prompt 的 {{ name }}；筆記模板傳入 Go template 的掃描（noteTemplates/templateVars.ts）。
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { linter, lintGutter, type Diagnostic } from "@codemirror/lint";
import { Compartment, EditorState, RangeSetBuilder } from "@codemirror/state";
import {
  Decoration,
  EditorView,
  keymap,
  lineNumbers,
  ViewPlugin,
  type DecorationSet,
  type ViewUpdate,
} from "@codemirror/view";
import { useEffect, useRef } from "react";

import { scanVariables, type VarIssue, type VarToken } from "./promptModel";

/** 找出內文中的變數（tokens 高亮、issues 標成錯誤） */
export type VariableScanner = (
  body: string,
  known: readonly string[],
) => { tokens: VarToken[]; issues: VarIssue[] };

export interface PromptCodeEditorProps {
  value: string;
  onChange: (value: string) => void;
  readOnly?: boolean;
  /** 可見的標籤由呼叫端提供；這裡用 aria-labelledby 連過去 */
  labelledBy: string;
  describedBy?: string;
  variables: readonly string[];
  /** 錯誤訊息（依語言）：由呼叫端產生 */
  issueMessage: (issue: VarIssue) => string;
  /** 變數的寫法；預設是 prompt 的 {{ name }} */
  scan?: VariableScanner;
}

const varMark = Decoration.mark({ class: "cm-var" });
const unknownMark = Decoration.mark({ class: "cm-var cm-var--unknown" });

function variableHighlighter(known: () => readonly string[], scan: () => VariableScanner) {
  function build(view: EditorView): DecorationSet {
    const b = new RangeSetBuilder<Decoration>();
    const { tokens } = scan()(view.state.doc.toString(), known());
    for (const tok of tokens) b.add(tok.from, tok.to, tok.known ? varMark : unknownMark);
    return b.finish();
  }
  return ViewPlugin.fromClass(
    class {
      decorations: DecorationSet;
      constructor(view: EditorView) {
        this.decorations = build(view);
      }
      update(u: ViewUpdate) {
        if (u.docChanged) this.decorations = build(u.view);
      }
    },
    { decorations: (v) => v.decorations },
  );
}

export function PromptCodeEditor({
  value,
  onChange,
  readOnly = false,
  labelledBy,
  describedBy,
  variables,
  issueMessage,
  scan = scanVariables,
}: PromptCodeEditorProps) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  // 回呼與清單放在 ref，避免每次渲染都重建編輯器
  const latest = useRef({ onChange, variables, issueMessage, scan });
  useEffect(() => {
    latest.current = { onChange, variables, issueMessage, scan };
  });
  const readOnlyConf = useRef(new Compartment());
  const attrsConf = useRef(new Compartment());

  const contentAttrs = (ro: boolean) =>
    EditorView.contentAttributes.of({
      "aria-labelledby": labelledBy,
      ...(describedBy ? { "aria-describedby": describedBy } : {}),
      "aria-multiline": "true",
      "aria-readonly": ro ? "true" : "false",
      role: "textbox",
      spellcheck: "false",
    });

  useEffect(() => {
    if (!host.current) return;
    const v = new EditorView({
      parent: host.current,
      state: EditorState.create({
        doc: value,
        extensions: [
          lineNumbers(),
          history(),
          keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
          EditorView.lineWrapping,
          variableHighlighter(
            () => latest.current.variables,
            () => latest.current.scan,
          ),
          linter(
            (view): Diagnostic[] =>
              latest.current
                .scan(view.state.doc.toString(), latest.current.variables)
                .issues.map((i) => ({
                  from: i.from,
                  to: i.to,
                  severity: "error",
                  message: latest.current.issueMessage(i),
                })),
            { delay: 200 },
          ),
          lintGutter(),
          readOnlyConf.current.of([
            EditorState.readOnly.of(readOnly),
            EditorView.editable.of(!readOnly),
          ]),
          attrsConf.current.of(contentAttrs(readOnly)),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) latest.current.onChange(u.state.doc.toString());
          }),
        ],
      }),
    });
    view.current = v;
    return () => {
      v.destroy();
      view.current = null;
    };
    // 編輯器只建立一次；value 與 readOnly 的變化由下面的 effect 同步
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 外部換了內容（切換版本、放棄修改）時取代整份文件
  useEffect(() => {
    const v = view.current;
    if (!v) return;
    const cur = v.state.doc.toString();
    if (cur !== value) v.dispatch({ changes: { from: 0, to: cur.length, insert: value } });
  }, [value]);

  useEffect(() => {
    view.current?.dispatch({
      effects: [
        readOnlyConf.current.reconfigure([
          EditorState.readOnly.of(readOnly),
          EditorView.editable.of(!readOnly),
        ]),
        attrsConf.current.reconfigure(contentAttrs(readOnly)),
      ],
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [readOnly, labelledBy, describedBy]);

  return <div className="prompt-editor__cm" ref={host} />;
}
