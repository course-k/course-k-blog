// Text offset under a point in a textarea. Browsers do not expose this for
// textareas, so an invisible mirror with the same box, font, wrapping and
// scroll position is laid over it and hit-tested instead.
const copied = [
  "boxSizing", "width", "height", "paddingTop", "paddingRight", "paddingBottom", "paddingLeft",
  "borderTopWidth", "borderRightWidth", "borderBottomWidth", "borderLeftWidth", "borderStyle",
  "fontFamily", "fontSize", "fontWeight", "fontStyle", "fontVariant", "fontStretch",
  "letterSpacing", "lineHeight", "textTransform", "textIndent", "textAlign", "tabSize",
  "wordSpacing", "whiteSpace", "wordBreak", "overflowWrap", "direction", "writingMode",
] as const;

export function offsetFromPoint(textarea: HTMLTextAreaElement, x: number, y: number): number {
  const rect = textarea.getBoundingClientRect();
  const style = getComputedStyle(textarea);
  const mirror = document.createElement("div");
  for (const property of copied) mirror.style[property] = style[property];
  Object.assign(mirror.style, {
    position: "fixed",
    left: `${rect.left}px`,
    top: `${rect.top}px`,
    width: `${rect.width}px`,
    height: `${rect.height}px`,
    overflow: "hidden",
    whiteSpace: "pre-wrap",
    overflowWrap: "break-word",
    visibility: "visible",
    opacity: "0",
    zIndex: "2147483647",
    pointerEvents: "auto",
    margin: "0",
    borderColor: "transparent",
  });
  // Scrollbars take width from the textarea's content box; reserve the same.
  mirror.style.paddingRight = `${parseFloat(style.paddingRight) + (textarea.offsetWidth - textarea.clientWidth - parseFloat(style.borderLeftWidth) - parseFloat(style.borderRightWidth))}px`;
  const text = document.createTextNode(textarea.value + "​");
  mirror.append(text);
  document.body.append(mirror);
  mirror.scrollTop = textarea.scrollTop;
  mirror.scrollLeft = textarea.scrollLeft;
  try {
    const position = caretAt(x, y);
    if (position && position.node === text) return Math.min(position.offset, textarea.value.length);
    // Below the last line or beside it: fall back to the nearest end.
    return y > rect.bottom || (y >= rect.top && x > rect.right) ? textarea.value.length : 0;
  } finally {
    mirror.remove();
  }
}

function caretAt(x: number, y: number): { node: Node; offset: number } | null {
  const documentWithCaret = document as Document & {
    caretPositionFromPoint?: (x: number, y: number) => { offsetNode: Node; offset: number } | null;
  };
  if (documentWithCaret.caretPositionFromPoint) {
    const position = documentWithCaret.caretPositionFromPoint(x, y);
    return position ? { node: position.offsetNode, offset: position.offset } : null;
  }
  const range = document.caretRangeFromPoint?.(x, y);
  return range ? { node: range.startContainer, offset: range.startOffset } : null;
}
