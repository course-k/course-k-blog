import test from "node:test";
import assert from "node:assert/strict";
import {
  displayAt,
  findImages,
  formatDisplay,
  insertImageSlots,
  parseDisplay,
  pendingIds,
  removePending,
  resolvePending,
  updateImageDisplay,
} from "./imageMarkdown.ts";

const id = "A".repeat(43);

test("reserved grammar round-trips and invalid values fall back safely", () => {
  assert.equal(formatDisplay({ width: 50, align: "left" }), "blog-image:v1;width=50;align=left");
  assert.equal(formatDisplay({ width: null, align: "right", title: "図 1;\"x\"" }), "blog-image:v1;width=auto;align=right;title=%E5%9B%B3%201%3B%22x%22");
  assert.deepEqual(parseDisplay("blog-image:v1;width=auto;align=right;title=%E5%9B%B3%201%3B%22x%22").display, { width: null, align: "right", title: "図 1;\"x\"" });
  for (const bad of ["blog-image:v1;width=9;align=left", "blog-image:v1;align=left;width=50", "blog-image:v2;width=50;align=left", "blog-image:v1;width=50;align=left;caption=x", "blog-image:v1;width=50;align=<script>"]) {
    const parsed = parseDisplay(bad);
    assert.equal(parsed.valid, false, bad);
    assert.deepEqual(parsed.display, { width: null, align: "left" });
  }
  assert.deepEqual(parseDisplay("ordinary tooltip").display, { width: null, align: "left", title: "ordinary tooltip" });
  assert.deepEqual(parseDisplay(null).display, { width: null, align: "left" });
});

test("only the selected occurrence of a repeated URL changes", () => {
  const markdown = `![a](/api/images/${id})\n\n![b](/api/images/${id})\n`;
  const [first, second] = findImages(markdown);
  const updated = updateImageDisplay(markdown, second.start, { width: 40, align: "center" });
  assert.equal(updated, `![a](/api/images/${id})\n\n![b](/api/images/${id} "blog-image:v1;width=40;align=center")\n`);
  assert.deepEqual(displayAt(updated!, first.start), { width: null, align: "left" });
  const again = updateImageDisplay(updated!, findImages(updated!)[1].start, { width: null, align: "center" });
  assert.match(again!, /width=auto;align=center"\)\n$/);
});

test("ordinary titles move into the reserved title key and are restored", () => {
  const markdown = `![図](/api/images/${id} "説明 \\"引用\\"")`;
  const updated = updateImageDisplay(markdown, 0, { width: 30, align: "right" })!;
  assert.equal(updated, `![図](/api/images/${id} "blog-image:v1;width=30;align=right;title=%E8%AA%AC%E6%98%8E%20%22%E5%BC%95%E7%94%A8%22")`);
  assert.equal(parseDisplay(findImages(updated)[0].title).display.title, '説明 "引用"');
});

test("code, inline code and links that only look like images are untouched", () => {
  const markdown = "```md\n![x](/a.png)\n```\n\n`![y](/b.png)` [![z](/c.png)](https://example.com) \\![w](/d.png)";
  const images = findImages(markdown);
  assert.deepEqual(images.map((image) => image.url), ["/c.png"]);
  const updated = updateImageDisplay(markdown, images[0].start, { width: 20, align: "left" })!;
  assert.ok(updated.startsWith("```md\n![x](/a.png)\n```\n\n`![y](/b.png)` [![z](/c.png \"blog-image:v1;width=20;align=left\")](https://example.com)"));
});

test("reference images are normalized to inline without changing the shared definition", () => {
  const markdown = `![one][pic]\n\n![two][pic]\n\n[pic]: /api/images/${id} "shared"\n`;
  const images = findImages(markdown);
  assert.equal(images.length, 2);
  assert.equal(images[1].kind, "reference");
  const updated = updateImageDisplay(markdown, images[1].start, { width: 60, align: "left" })!;
  assert.equal(updated, `![one][pic]\n\n![two](/api/images/${id} "blog-image:v1;width=60;align=left;title=shared")\n\n[pic]: /api/images/${id} "shared"\n`);
});

test("alt text and destinations with brackets, escapes and angle brackets are preserved", () => {
  const markdown = "![a [nested] \\] alt](<./my image.png> 'old')";
  const updated = updateImageDisplay(markdown, 0, { width: 10, align: "center" })!;
  assert.equal(updated, "![a [nested] \\] alt](<./my image.png> \"blog-image:v1;width=10;align=center;title=old\")");
});

test("multiple pasted images become separate paragraphs in order", () => {
  const { markdown, caret } = insertImageSlots("前の文\n後の文", 4, 4, [
    { clientImageId: "id-1", alt: "a[1].png" },
    { clientImageId: "id-2", alt: "b.png" },
  ]);
  assert.equal(markdown, "前の文\n\n![a\\[1\\].png](pending-image:id-1)\n\n![b.png](pending-image:id-2)\n\n後の文");
  assert.equal(markdown.slice(0, caret).endsWith("(pending-image:id-2)"), true);
  assert.deepEqual(pendingIds(markdown), ["id-1", "id-2"]);
  const replaced = insertImageSlots("abcdef", 2, 4, [{ clientImageId: "x", alt: "x" }]);
  assert.equal(replaced.markdown, "ab\n\n![x](pending-image:x)\n\nef");
});

test("pending slots resolve independently and can be removed for saving", () => {
  let markdown = "冒頭\n\n![a](pending-image:id-1)\n\n中央\n\n![b](pending-image:id-2)\n\n末尾";
  assert.equal(removePending(markdown), "冒頭\n\n中央\n\n末尾");
  markdown = resolvePending(markdown, "id-2", `/api/images/${id}`);
  assert.equal(markdown, `冒頭\n\n![a](pending-image:id-1)\n\n中央\n\n![b](/api/images/${id})\n\n末尾`);
  assert.equal(removePending(markdown), `冒頭\n\n中央\n\n![b](/api/images/${id})\n\n末尾`);
  assert.equal(removePending(markdown, "id-1"), `冒頭\n\n中央\n\n![b](/api/images/${id})\n\n末尾`);
  assert.equal(resolvePending("no slot", "id-1", "/x"), "no slot");
  assert.equal(removePending("![only](pending-image:z)"), "");
});
