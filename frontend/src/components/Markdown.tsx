import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Root, Element, RootContent } from "hast";

type Heading = { id: string; text: string; depth: number };
const element = (
  tagName: string,
  properties: Element["properties"],
  children: Element["children"],
): Element => ({ type: "element", tagName, properties, children });
function plainText(node: RootContent): string {
  if (node.type === "text") return node.value;
  if (node.type === "element" && node.tagName === "img")
    return String(node.properties.alt || "");
  return "children" in node ? node.children.map(plainText).join("") : "";
}
// One parsed tree supplies both anchors and the TOC, including formatted or duplicate headings.
function readingStructure({ toc, prefix }: { toc: boolean; prefix: string }) {
  return (tree: Root) => {
    const headings: Heading[] = [];
    let count = 0;
    function walk(nodes: RootContent[]) {
      for (const node of nodes) {
        if (node.type !== "element") continue;
        if (/^h[1-6]$/.test(node.tagName)) {
          const id = `${prefix}-section-${++count}`;
          node.properties.id = id;
          const text = plainText(node);
          if (text && Number(node.tagName[1]) <= 3)
            headings.push({ id, text, depth: Number(node.tagName[1]) });
        }
        walk(node.children);
      }
    }
    walk(tree.children);
    if (!toc || !headings.length) return;
    const links = () =>
      element("nav", { ariaLabel: "目次" }, [
        element(
          "ol",
          {},
          headings.map((h) =>
            element("li", { className: h.depth === 3 ? ["toc-sub"] : [] }, [
              element("a", { href: `#${h.id}` }, [
                { type: "text", value: h.text },
              ]),
            ]),
          ),
        ),
      ]);
    tree.children = [
      element("div", { className: ["reading-layout"] }, [
        element(
          "div",
          { className: ["reading-content"] },
          tree.children as Element["children"],
        ),
        element("aside", { className: ["toc"] }, [
          element("div", { className: ["toc-desktop"] }, [
            element("p", {}, [{ type: "text", value: "目次" }]),
            links(),
          ]),
          element("details", { className: ["toc-mobile"] }, [
            element("summary", {}, [{ type: "text", value: "目次" }]),
            links(),
          ]),
        ]),
      ]),
    ];
  };
}

// Preview and public pages share Markdown rules. Raw HTML remains disabled.
export function Markdown({
  text,
  toc = false,
  prefix = "preview",
}: {
  text: string;
  toc?: boolean;
  prefix?: string;
}) {
  return (
    <div className="prose">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[[readingStructure, { toc, prefix }]]}
        skipHtml
      >
        {text}
      </ReactMarkdown>
    </div>
  );
}
