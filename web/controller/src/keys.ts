// Keyboard shortcuts for the remote. Rules (docs/protocol.md, mando web):
// - never while typing in a field; - ignore auto-repeat; - leave browser
//   shortcuts (Ctrl/Alt/Meta) alone; - Space/Enter on a focused button is a
//   click, not a shortcut (avoids double sending).
export type Shortcut = "presentation.next" | "presentation.previous" | null;

export function shortcutFor(e: KeyboardEvent): Shortcut {
  if (e.repeat || e.ctrlKey || e.altKey || e.metaKey || e.isComposing) return null;
  const t = e.target as HTMLElement | null;
  if (t && (t.closest("input, textarea, select, [contenteditable=''], [contenteditable='true']"))) return null;
  const onButton = !!t?.closest("button, a, summary");
  switch (e.key) {
    case "ArrowRight": case "PageDown": return "presentation.next";
    case "ArrowLeft": case "PageUp": return "presentation.previous";
    case " ": return onButton ? null : "presentation.next";
  }
  return null;
}
