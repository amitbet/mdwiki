import TurndownService from "turndown";
import { gfm, tables, strikethrough } from "turndown-plugin-gfm";

const td = new TurndownService({
  codeBlockStyle: "fenced",
  bulletListMarker: "-",
  headingStyle: "atx",
  emDelimiter: "*",
});

td.use([gfm, tables, strikethrough]);

const TAB_SENTINEL = "\uE000";

function preserveVisibleSpaces(text: string): string {
  return text
    .replace(/^ +/g, (match) => "\u00a0".repeat(match.length))
    .replace(/ {2,}/g, (match) => ` ${"\u00a0".repeat(match.length - 1)}`)
    .replace(/ +$/g, (match) => "\u00a0".repeat(match.length));
}

function prepareHTMLForWhitespacePreservation(html: string): string {
  if (typeof DOMParser === "undefined") {
    return html;
  }
  const doc = new DOMParser().parseFromString(html, "text/html");
  const walker = doc.createTreeWalker(doc.body, NodeFilter.SHOW_TEXT);
  const textNodes: Text[] = [];
  let current = walker.nextNode();
  while (current) {
    textNodes.push(current as Text);
    current = walker.nextNode();
  }
  for (const node of textNodes) {
    const parent = node.parentElement;
    if (!parent) {
      continue;
    }
    if (parent.closest("pre, code")) {
      continue;
    }
    if (!/[ \t]{2,}|^[ \t]|[ \t]$/.test(node.data)) {
      continue;
    }
    node.data = preserveVisibleSpaces(node.data).replace(/\t/g, TAB_SENTINEL);
  }
  return doc.body.innerHTML;
}

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

function escapeMarkdownImageAlt(text: string): string {
  return text.replace(/[[\]\\]/g, "\\$&");
}

function escapeMarkdownDestination(url: string): string {
  return url.replace(/>/g, "%3E");
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

td.addRule("wikiTaskBlock", {
  filter(node) {
    return node instanceof HTMLElement && node.tagName === "DIV" && node.getAttribute("data-wiki-task-block") === "true";
  },
  replacement(content) {
    return `\n<!-- wiki:tasks:start -->\n${content.trim()}\n<!-- wiki:tasks:end -->\n`;
  },
});

td.addRule("emptyParagraph", {
  filter(node) {
    if (!(node instanceof HTMLElement) || node.tagName !== "P") {
      return false;
    }
    const text = (node.textContent || "").replace(/\u00a0/g, "").trim();
    const childNodes = Array.from(node.childNodes);
    if (text.length > 0) {
      return false;
    }
    return childNodes.length === 0 || childNodes.every((child) => {
      return child.nodeType === Node.ELEMENT_NODE && (child as HTMLElement).tagName === "BR";
    });
  },
  replacement() {
    return "\n<!-- mdwiki:blank-line -->\n";
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

td.addRule("imageWithSafeDestination", {
  filter(node) {
    return node instanceof HTMLElement && node.tagName === "IMG";
  },
  replacement(_content, node) {
    const el = node as HTMLElement;
    const src = (el.getAttribute("src") || "").trim();
    if (!src) {
      return "";
    }
    const alt = escapeMarkdownImageAlt((el.getAttribute("alt") || "").trim());
    return `![${alt}](<${escapeMarkdownDestination(src)}>)`;
  },
});

function encodeVisibleSpaces(text: string): string {
  return text
    .replace(/^ +/, (match) => "&nbsp;".repeat(match.length))
    .replace(/^\u00a0+/, (match) => "&nbsp;".repeat(match.length))
    .replace(/ {2,}/g, (match) => ` ${"&nbsp;".repeat(match.length - 1)}`)
    .replace(/\u00a0{2,}/g, (match) => "&nbsp;".repeat(match.length))
    .replace(/ \u00a0+/g, (match) => ` ${"&nbsp;".repeat(match.length - 1)}`)
    .replace(/ +$/g, (match) => "&nbsp;".repeat(match.length));
}

function encodeIndentedPlaintext(markdown: string): string {
  const lines = markdown.split("\n");
  const out: string[] = [];
  let fencedBy = "";
  for (const line of lines) {
    const normalized = line.replace(
      /^\u00a0+/,
      (match) => "\t".repeat(Math.floor(match.length / 4)) + " ".repeat(match.length % 4),
    );
    const trimmed = normalized.trimStart();
    const fenceMatch = trimmed.match(/^(```+|~~~+)/);
    if (fenceMatch) {
      const marker = fenceMatch[1][0];
      if (!fencedBy) {
        fencedBy = marker;
      } else if (fencedBy === marker) {
        fencedBy = "";
      }
      out.push(normalized);
      continue;
    }
    if (fencedBy) {
      out.push(normalized);
      continue;
    }
    const leadingIndent = normalized.match(/^[ \t]+/)?.[0] ?? "";
    if (leadingIndent.length === 0) {
      out.push(normalized);
      continue;
    }
    if (
      trimmed === "" ||
      /^(?:<!--\s*mdwiki:blank-line\s*-->|`{3,}|~{3,}|\|)/.test(trimmed)
    ) {
      out.push(normalized);
      continue;
    }
    const prefixMatch = normalized.match(/^(\s{0,3}(?:(?:>\s*)?(?:(?:[-+*]|\d+[.)])\s+)?))/);
    const prefix = prefixMatch?.[0] ?? "";
    const rest = normalized.slice(prefix.length);
    if (/^(?:[#>]|[-+*]\s|\d+[.)]\s)/.test(trimmed) && prefix.length > 0) {
      out.push(`${prefix}${encodeVisibleSpaces(rest)}`);
      continue;
    }
    out.push(encodeVisibleSpaces(normalized));
  }
  return out.join("\n");
}

export function htmlToMarkdown(html: string): string {
  const out = encodeIndentedPlaintext(td.turndown(prepareHTMLForWhitespacePreservation(html)).replaceAll(TAB_SENTINEL, "\t"));
  return out.length > 0 ? out.replace(/\s+$/, "") + "\n" : "";
}
