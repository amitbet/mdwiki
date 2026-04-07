import TurndownService from "turndown";
import { gfm, tables, strikethrough } from "turndown-plugin-gfm";

const td = new TurndownService({
  codeBlockStyle: "fenced",
  bulletListMarker: "-",
  headingStyle: "atx",
  emDelimiter: "*",
});

td.use([gfm, tables, strikethrough]);

// Preserve underline markup as inline HTML.
td.addRule("underline", {
  filter: ["u"],
  replacement(content: string) {
    return `<u>${content}</u>`;
  },
});

// Persist inline anchor markers as markdown comments.
td.addRule("wikiAnchor", {
  filter(node) {
    if (!(node instanceof HTMLElement)) {
      return false;
    }
    return node.tagName === "SPAN" && node.hasAttribute("data-wiki-anchor");
  },
  replacement(_content, node) {
    const el = node as HTMLElement;
    const anchorID = (el.getAttribute("data-wiki-anchor") || "").trim();
    if (!anchorID) {
      return "";
    }
    return `\n<!-- wiki:anchor:${anchorID} -->\n`;
  },
});

export function htmlToMarkdown(html: string): string {
  const out = td.turndown(html).replace(/\n{3,}/g, "\n\n").trim();
  return out.length > 0 ? `${out}\n` : "";
}
