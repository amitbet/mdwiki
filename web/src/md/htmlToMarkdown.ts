import TurndownService from "turndown";
import { gfm, tables, strikethrough } from "turndown-plugin-gfm";

const td = new TurndownService({
  codeBlockStyle: "fenced",
  bulletListMarker: "-",
  headingStyle: "atx",
  emDelimiter: "*",
});

td.use([gfm, tables, strikethrough]);

function commentIdFromNode(node: HTMLElement): string {
  const direct = (node.getAttribute("data-wiki-comment") || "").trim();
  if (direct) {
    return direct;
  }
  const classNames = (node.getAttribute("class") || "").split(/\s+/);
  for (const c of classNames) {
    if (c.startsWith("wiki-comment-id-")) {
      const id = c.slice("wiki-comment-id-".length).trim();
      if (id.length > 0) {
        return id;
      }
    }
  }
  return "";
}

// Preserve underline markup as inline HTML.
td.addRule("underline", {
  filter: ["u"],
  replacement(content: string) {
    return `<u>${content}</u>`;
  },
});

td.addRule("fencedCodeWithLanguage", {
  filter(node) {
    return node.nodeName === "PRE";
  },
  replacement(_content, node) {
    const pre = node as HTMLElement;
    const code = pre.querySelector("code");
    if (!code) {
      return "\n```\n\n```\n";
    }
    const classNames = (code.getAttribute("class") || "").split(/\s+/);
    let language = "";
    for (const c of classNames) {
      if (c.startsWith("language-")) {
        language = c.slice("language-".length).trim();
        break;
      }
    }
    const text = (code.textContent || "").replace(/\n$/, "");
    return `\n\`\`\`${language}\n${text}\n\`\`\`\n`;
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

// Convert highlighted comment segments back to markdown comment markers.
td.addRule("wikiCommentHighlight", {
  filter(node) {
    if (!(node instanceof HTMLElement)) {
      return false;
    }
    if (node.tagName !== "MARK") {
      return false;
    }
    return commentIdFromNode(node).length > 0;
  },
  replacement(content, node) {
    const el = node as HTMLElement;
    const id = commentIdFromNode(el);
    if (!id) {
      return content;
    }
    return `<!-- wiki:comment:start:${id} -->${content}<!-- wiki:comment:end:${id} -->`;
  },
});

td.addRule("mdwikiDiagram", {
  filter(node) {
    return node instanceof HTMLElement && node.tagName === "DIV" && node.hasAttribute("data-mdwiki-diagram");
  },
  replacement(_content, node) {
    const el = node as HTMLElement;
    const path = (el.getAttribute("data-mdwiki-diagram") || "").trim();
    const kind = (el.getAttribute("data-mdwiki-kind") || "").trim();
    const name = (el.getAttribute("data-mdwiki-name") || "").trim();
    if (!path) {
      return "";
    }
    const attrs = [
      `data-mdwiki-diagram=\"${path.replace(/\"/g, "&quot;")}\"`,
      kind ? `data-mdwiki-kind=\"${kind.replace(/\"/g, "&quot;")}\"` : "",
      name ? `data-mdwiki-name=\"${name.replace(/\"/g, "&quot;")}\"` : "",
    ]
      .filter(Boolean)
      .join(" ");
    return `\n<div ${attrs}>Diagram: ${name || path}</div>\n`;
  },
});

export function htmlToMarkdown(html: string): string {
  const out = td.turndown(html).replace(/\n{3,}/g, "\n\n").trim();
  return out.length > 0 ? `${out}\n` : "";
}
