// Markdown image positions, reserved display settings and pending upload slots.
// Everything here edits the Markdown structurally (via the parsed tree's
// offsets), so code blocks, inline code and other occurrences are untouched.
import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import type { Root, Nodes, Definition } from "mdast";

export type ImageAlign = "left" | "center" | "right";
export type ImageDisplay = {
  /** Percentage of the content width, 10–100; null keeps the natural size. */
  width: number | null;
  align: ImageAlign;
  /** An ordinary tooltip title moved aside when settings are written. */
  title?: string;
};
export const DISPLAY_PREFIX = "blog-image:";
export const PENDING_PREFIX = "pending-image:";
export const DEFAULT_DISPLAY: ImageDisplay = { width: null, align: "left" };

const widthPattern = /^(auto|[1-9][0-9]|100)$/;
const titlePattern = /^(?:[A-Za-z0-9\-_.!~*'()]|%[0-9A-Fa-f]{2})+$/;

/** Parses the v1 grammar; invalid reserved titles fall back to the defaults. */
export function parseDisplay(title: string | null | undefined): {
  display: ImageDisplay;
  reserved: boolean;
  valid: boolean;
} {
  if (!title || !title.startsWith(DISPLAY_PREFIX))
    return {
      display: title ? { ...DEFAULT_DISPLAY, title } : { ...DEFAULT_DISPLAY },
      reserved: false,
      valid: true,
    };
  const invalid = { display: { ...DEFAULT_DISPLAY }, reserved: true, valid: false };
  if (!title.startsWith(`${DISPLAY_PREFIX}v1;`)) return invalid;
  const parts = title.slice(`${DISPLAY_PREFIX}v1;`.length).split(";");
  if (parts.length !== 2 && parts.length !== 3) return invalid;
  const [widthPart, alignPart, titlePart] = parts;
  if (!widthPart.startsWith("width=") || !alignPart.startsWith("align="))
    return invalid;
  const width = widthPart.slice(6);
  const align = alignPart.slice(6);
  if (!widthPattern.test(width)) return invalid;
  if (align !== "left" && align !== "center" && align !== "right")
    return invalid;
  const display: ImageDisplay = {
    width: width === "auto" ? null : Number(width),
    align,
  };
  if (titlePart !== undefined) {
    if (!titlePart.startsWith("title=")) return invalid;
    const encoded = titlePart.slice(6);
    if (!titlePattern.test(encoded)) return invalid;
    try {
      display.title = decodeURIComponent(encoded);
    } catch {
      return invalid;
    }
  }
  return { display, reserved: true, valid: true };
}

export function formatDisplay(display: ImageDisplay): string {
  const width =
    display.width === null
      ? "auto"
      : String(Math.min(100, Math.max(10, Math.round(display.width))));
  const title = display.title
    ? `;title=${encodeURIComponent(display.title)}`
    : "";
  return `${DISPLAY_PREFIX}v1;width=${width};align=${display.align}${title}`;
}

export type ImageOccurrence = {
  start: number;
  end: number;
  kind: "inline" | "reference";
  url: string;
  alt: string;
  title: string | null;
};

const parser = unified().use(remarkParse).use(remarkGfm);

function normalizeLabel(label: string) {
  return label.toLowerCase().replace(/\s+/g, " ").trim();
}

/** All images in document order with their source offsets. */
export function findImages(markdown: string): ImageOccurrence[] {
  const tree = parser.parse(markdown) as Root;
  const definitions = new Map<string, Definition>();
  const images: ImageOccurrence[] = [];
  const references: { node: Nodes & { type: "imageReference" } }[] = [];
  const walk = (node: Nodes) => {
    if (node.type === "definition") {
      const key = normalizeLabel(node.identifier);
      if (!definitions.has(key)) definitions.set(key, node);
    }
    if (node.type === "image" && node.position)
      images.push({
        start: node.position.start.offset ?? 0,
        end: node.position.end.offset ?? 0,
        kind: "inline",
        url: node.url,
        alt: node.alt ?? "",
        title: node.title ?? null,
      });
    if (node.type === "imageReference") references.push({ node });
    if ("children" in node) for (const child of node.children) walk(child);
  };
  walk(tree);
  for (const { node } of references) {
    const definition = definitions.get(normalizeLabel(node.identifier));
    if (!definition || !node.position) continue;
    images.push({
      start: node.position.start.offset ?? 0,
      end: node.position.end.offset ?? 0,
      kind: "reference",
      url: definition.url,
      alt: node.alt ?? "",
      title: definition.title ?? null,
    });
  }
  return images.sort((a, b) => a.start - b.start);
}

/** Offset of the `]` that closes the label starting after `![`. */
function labelEnd(source: string): number {
  let depth = 0;
  for (let i = 2; i < source.length; i++) {
    const char = source[i];
    if (char === "\\") {
      i++;
      continue;
    }
    if (char === "[") depth++;
    if (char === "]") {
      if (depth === 0) return i;
      depth--;
    }
  }
  return -1;
}

/** Offset just after the destination in an inline image `![..](dest "t")`. */
function destinationEnd(source: string, open: number): number {
  let i = open + 1;
  while (i < source.length && /\s/.test(source[i])) i++;
  if (source[i] === "<") {
    for (i++; i < source.length; i++) {
      if (source[i] === "\\") i++;
      else if (source[i] === ">") return i + 1;
    }
    return -1;
  }
  let depth = 0;
  for (; i < source.length; i++) {
    const char = source[i];
    if (char === "\\") {
      i++;
      continue;
    }
    if (/\s/.test(char)) return i;
    if (char === "(") depth++;
    if (char === ")") {
      if (depth === 0) return i;
      depth--;
    }
  }
  return -1;
}

function quoteTitle(title: string) {
  return `"${title.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
}

function formatDestination(url: string) {
  return /[\s()<>]/.test(url) || url === ""
    ? `<${url.replace(/[<>]/g, (c) => encodeURIComponent(c))}>`
    : url;
}

/** Rewrites one occurrence with a new title (null removes it). Reference
 * images become inline so a shared definition keeps its other uses. */
function rewriteOccurrence(
  markdown: string,
  occurrence: ImageOccurrence,
  title: string | null,
  url?: string,
): string | null {
  const source = markdown.slice(occurrence.start, occurrence.end);
  const close = labelEnd(source);
  if (!source.startsWith("![") || close < 0) return null;
  const label = source.slice(0, close + 1);
  const suffix = title === null ? ")" : ` ${quoteTitle(title)})`;
  let replacement: string;
  if (occurrence.kind === "inline" && url === undefined) {
    const end = destinationEnd(source, close + 1);
    if (source[close + 1] !== "(" || end < 0) return null;
    replacement = source.slice(0, end) + suffix;
  } else {
    replacement = `${label}(${formatDestination(url ?? occurrence.url)}${suffix}`;
  }
  return (
    markdown.slice(0, occurrence.start) +
    replacement +
    markdown.slice(occurrence.end)
  );
}

/** Applies display settings to the image starting at `start` only. */
export function updateImageDisplay(
  markdown: string,
  start: number,
  next: Omit<ImageDisplay, "title">,
): string | null {
  const occurrence = findImages(markdown).find((image) => image.start === start);
  if (!occurrence) return null;
  const current = parseDisplay(occurrence.title).display;
  return rewriteOccurrence(
    markdown,
    occurrence,
    formatDisplay({ ...next, title: current.title }),
  );
}

export function displayAt(markdown: string, start: number): ImageDisplay | null {
  const occurrence = findImages(markdown).find((image) => image.start === start);
  return occurrence ? parseDisplay(occurrence.title).display : null;
}

function escapeAlt(text: string) {
  return text.replace(/[\\[\]]/g, "\\$&").replace(/[\r\n]+/g, " ");
}

/** Inserts one image paragraph per upload at [from, to), with blank lines
 * around them. Returns the new text and the caret after the inserted block. */
export function insertImageSlots(
  markdown: string,
  from: number,
  to: number,
  slots: { clientImageId: string; alt: string }[],
): { markdown: string; caret: number } {
  const before = markdown.slice(0, from);
  const after = markdown.slice(to);
  const block = slots
    .map(
      (slot) =>
        `![${escapeAlt(slot.alt)}](${PENDING_PREFIX}${slot.clientImageId})`,
    )
    .join("\n\n");
  const lead = before === "" || before.endsWith("\n\n") ? "" : before.endsWith("\n") ? "\n" : "\n\n";
  const trail = after === "" || after.startsWith("\n\n") ? "" : after.startsWith("\n") ? "\n" : "\n\n";
  const inserted = lead + block + trail;
  return {
    markdown: before + inserted + after,
    caret: before.length + lead.length + block.length,
  };
}

export function pendingIds(markdown: string): string[] {
  return findImages(markdown)
    .filter((image) => image.url.startsWith(PENDING_PREFIX))
    .map((image) => image.url.slice(PENDING_PREFIX.length));
}

/** Replaces only the pending slot for one upload with its official URL. */
export function resolvePending(
  markdown: string,
  clientImageId: string,
  url: string,
): string {
  let result = markdown;
  const slots = findImages(markdown)
    .filter((image) => image.url === PENDING_PREFIX + clientImageId)
    .reverse();
  for (const slot of slots) {
    const source = result.slice(slot.start, slot.end);
    const close = labelEnd(source);
    const open = close + 1;
    const end = destinationEnd(source, open);
    if (close < 0 || source[open] !== "(" || end < 0) continue;
    result =
      result.slice(0, slot.start) +
      source.slice(0, open + 1) +
      url +
      source.slice(end) +
      result.slice(slot.end);
  }
  return result;
}

/** Removes pending slots: for saving (all) or for "削除" (one). Surrounding
 * blank lines collapse so no empty paragraph remains. */
export function removePending(markdown: string, clientImageId?: string): string {
  const slots = findImages(markdown)
    .filter((image) =>
      clientImageId === undefined
        ? image.url.startsWith(PENDING_PREFIX)
        : image.url === PENDING_PREFIX + clientImageId,
    )
    .reverse();
  let result = markdown;
  for (const slot of slots) {
    let start = slot.start;
    let end = slot.end;
    const lineStart = result.lastIndexOf("\n", start - 1) + 1;
    const lineEndIndex = result.indexOf("\n", end);
    const lineEnd = lineEndIndex < 0 ? result.length : lineEndIndex;
    const aloneOnLine =
      result.slice(lineStart, start).trim() === "" &&
      result.slice(end, lineEnd).trim() === "";
    if (aloneOnLine) {
      start = lineStart;
      end = lineEnd;
      if (result.slice(end, end + 2) === "\n\n") end += 2;
      else if (result[end] === "\n") end += 1;
      else if (start >= 2 && result.slice(start - 2, start) === "\n\n") start -= 2;
      else if (start >= 1 && result[start - 1] === "\n") start -= 1;
    }
    result = result.slice(0, start) + result.slice(end);
  }
  return result;
}
