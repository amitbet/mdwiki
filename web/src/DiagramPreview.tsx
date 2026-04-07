import { useEffect, useRef } from "react";

/** After GFM HTML render, upgrade `language-mermaid` code blocks with Mermaid SVG. */
export function DiagramPreview({ html }: { html: string }) {
  const done = useRef(false);

  useEffect(() => {
    if (!html) return;
    done.current = false;
    const t = setTimeout(() => {
      const root = document.querySelector(".md-preview");
      if (!root || done.current) return;
      const codes = root.querySelectorAll("pre code.language-mermaid, pre code[class*='language-mermaid']");
      if (!codes.length) return;
      import("mermaid")
        .then((m) => {
          m.default.initialize({ startOnLoad: false, theme: "dark" });
          codes.forEach((code) => {
            const pre = code.parentElement;
            if (!pre?.parentNode) return;
            const div = document.createElement("div");
            div.className = "mermaid";
            div.textContent = code.textContent;
            pre.parentNode.replaceChild(div, pre);
          });
          m.default.run({ querySelector: ".md-preview .mermaid" });
          done.current = true;
        })
        .catch(() => {});
    }, 100);
    return () => clearTimeout(t);
  }, [html]);

  return null;
}
