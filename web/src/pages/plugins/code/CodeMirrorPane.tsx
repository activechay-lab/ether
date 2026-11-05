import { autocompletion } from "@codemirror/autocomplete";
import { useLocale } from "../../../i18n/react";
import { javascript } from "@codemirror/lang-javascript";
import { json } from "@codemirror/lang-json";
import { unifiedMergeView } from "@codemirror/merge";
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { EditorView } from "@codemirror/view";
import { tags } from "@lezer/highlight";
import { tsAutocomplete, tsFacet, tsHover, tsLinter, tsSync } from "@valtown/codemirror-ts";
import CodeMirror from "@uiw/react-codemirror";
import { useMemo } from "react";
import { PLUGIN_PATH, pluginEnv } from "./tsEnv";

/** Ether's tokens as a CodeMirror theme; light and dark follow the page. */
const etherTheme = EditorView.theme({
  "&": {
    height: "100%",
    backgroundColor: "rgb(var(--ch-surface))",
    color: "rgb(var(--ch-fg))",
    fontSize: "13px",
  },
  ".cm-scroller": {
    fontFamily: "var(--font-mono, ui-monospace, SFMono-Regular, Menlo, monospace)",
    lineHeight: "1.65",
  },
  ".cm-content": { padding: "12px 0", caretColor: "rgb(var(--ch-primary))" },
  ".cm-gutters": {
    backgroundColor: "rgb(var(--ch-surface))",
    color: "rgb(var(--ch-fg-subtle))",
    border: "none",
    paddingLeft: "8px",
  },
  ".cm-activeLine": { backgroundColor: "rgb(var(--ch-bg-subtle) / 0.7)" },
  ".cm-activeLineGutter": { backgroundColor: "transparent", color: "rgb(var(--ch-fg-muted))" },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection": {
    backgroundColor: "rgb(var(--ch-primary) / 0.18) !important",
  },
  ".cm-tooltip": {
    backgroundColor: "rgb(var(--ch-elevated))",
    color: "rgb(var(--ch-fg))",
    border: "1px solid rgb(var(--ch-border))",
    borderRadius: "10px",
    boxShadow: "0 12px 32px -12px rgb(0 0 0 / 0.35)",
    overflow: "hidden",
  },
  ".cm-tooltip-autocomplete > ul": { fontFamily: "inherit", maxHeight: "16rem" },
  ".cm-tooltip-autocomplete > ul > li": { padding: "4px 10px" },
  ".cm-tooltip-autocomplete > ul > li[aria-selected]": {
    backgroundColor: "rgb(var(--ch-primary) / 0.14)",
    color: "rgb(var(--ch-fg))",
  },
  ".cm-completionDetail": { color: "rgb(var(--ch-fg-muted))", fontStyle: "normal" },
  ".cm-tooltip-hover": { padding: "8px 12px", maxWidth: "28rem", fontSize: "12px" },
  ".cm-changedLine": { backgroundColor: "rgb(var(--ch-success) / 0.08)" },
  ".cm-changedText": { background: "rgb(var(--ch-success) / 0.22)" },
  ".cm-deletedChunk": { backgroundColor: "rgb(var(--ch-danger) / 0.08)" },
  ".cm-diagnostic-error": { borderLeftColor: "rgb(var(--ch-danger))" },
  ".cm-lintRange-error": {
    backgroundImage: "none",
    textDecoration: "underline wavy rgb(var(--ch-danger))",
    textUnderlineOffset: "3px",
  },
});

const etherHighlight = HighlightStyle.define([
  {
    tag: [tags.keyword, tags.modifier, tags.controlKeyword],
    color: "rgb(var(--ch-accent-violet))",
  },
  { tag: [tags.string, tags.special(tags.string)], color: "rgb(var(--ch-accent-emerald))" },
  { tag: [tags.number, tags.bool, tags.null], color: "rgb(var(--ch-accent-amber))" },
  {
    tag: [tags.function(tags.variableName), tags.function(tags.propertyName)],
    color: "rgb(var(--ch-accent-sky))",
  },
  { tag: [tags.propertyName], color: "rgb(var(--ch-accent-teal))" },
  { tag: [tags.typeName, tags.className], color: "rgb(var(--ch-accent-rose))" },
  { tag: [tags.comment], color: "rgb(var(--ch-fg-subtle))", fontStyle: "italic" },
  { tag: [tags.punctuation, tags.bracket], color: "rgb(var(--ch-fg-muted))" },
]);

export default function CodeMirrorPane({
  file,
  value,
  onChange,
  original,
}: {
  file: string;
  value: string;
  onChange: (value: string) => void;
  /** When set, shows `value` as the agent's change against this original. */
  original?: string;
}) {
  const { locale } = useLocale();
  const extensions = useMemo(() => {
    const base = [etherTheme, syntaxHighlighting(etherHighlight), EditorView.lineWrapping];
    if (file !== "plugin.ts") return [...base, json()];
    const env = pluginEnv(value);
    if (original !== undefined)
      return [
        ...base,
        javascript({ typescript: true }),
        unifiedMergeView({ original, mergeControls: false }),
        EditorView.editable.of(false),
      ];
    return [
      ...base,
      javascript({ typescript: true }),
      tsFacet.of({ env, path: PLUGIN_PATH }),
      tsSync(),
      tsLinter(),
      autocompletion({ override: [tsAutocomplete()] }),
      tsHover(),
    ];
    // The TypeScript environment is created once per file.
  }, [file, original, locale]);
  return (
    <CodeMirror
      value={value}
      onChange={onChange}
      extensions={extensions}
      theme="none"
      height="100%"
      className="h-full"
      basicSetup={{ foldGutter: false, highlightActiveLine: true, autocompletion: false }}
      aria-label={file}
    />
  );
}
