import rehypeRaw from "rehype-raw";
import rehypeSanitize, { defaultSchema } from "rehype-sanitize";
import rehypeStringify from "rehype-stringify";
import remarkGfm from "remark-gfm";
import remarkParse from "remark-parse";
import remarkRehype from "remark-rehype";
import { unified } from "unified";

function expandCommentMarkers(markdown: string): string {
  // Legacy anchor cleanup from previous implementation.
  let out = markdown.replace(/<span\s+data-wiki-anchor="[^"]+"\s*><\/span>/g, "");
  out = out.replace(/<!--\s*wiki:anchor:[a-zA-Z0-9_-]+\s*-->/g, "");
  // Markdown-level comment markers become real highlights in the editor.
  out = out.replace(
    /<!--\s*wiki:comment:start:([a-zA-Z0-9_-]+)\s*-->([\s\S]*?)<!--\s*wiki:comment:end:\1\s*-->/g,
    '<mark data-wiki-comment="$1" class="wiki-comment-highlight wiki-comment-id-$1">$2</mark>',
  );
  out = out.replace(
    /<!--\s*wiki:tasks:start\s*-->([\s\S]*?)<!--\s*wiki:tasks:end\s*-->/g,
    '<div data-wiki-task-block="true">$1</div>',
  );
  return out;
}

const processor = unified()
  .use(remarkParse)
  .use(remarkGfm)
  .use(remarkRehype, { allowDangerousHtml: true })
  .use(rehypeRaw)
  .use(rehypeSanitize, {
    ...defaultSchema,
    tagNames: [...(defaultSchema.tagNames ?? []), "mark", "div", "input"],
    attributes: {
      ...(defaultSchema.attributes ?? {}),
      pre: [...(((defaultSchema.attributes ?? {}).pre as any[]) ?? []), "className"],
      code: [...(((defaultSchema.attributes ?? {}).code as any[]) ?? []), "className"],
      div: [
        ...(((defaultSchema.attributes ?? {}).div as any[]) ?? []),
        "className",
        ["data-wiki-task-block", /^(true)$/],
        ["data-mdwiki-diagram", /^.+$/],
        ["data-mdwiki-kind", /^(excalidraw|drawio)$/],
        ["data-mdwiki-name", /^.+$/],
      ],
      input: [
        ...(((defaultSchema.attributes ?? {}).input as any[]) ?? []),
        "checked",
        "disabled",
        "type",
      ],
      mark: [
        ...(((defaultSchema.attributes ?? {}).mark as any[]) ?? []),
        "className",
        ["data-wiki-comment", /^[a-zA-Z0-9_-]+$/],
      ],
    },
  })
  .use(rehypeStringify);

export async function renderGFM(markdown: string): Promise<string> {
  const file = await processor.process(expandCommentMarkers(markdown));
  return String(file);
}
