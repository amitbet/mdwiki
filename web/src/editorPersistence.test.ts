import StarterKit from "@tiptap/starter-kit";
import { Editor } from "@tiptap/core";
import { afterEach, describe, expect, it } from "vitest";
import { htmlToMarkdown } from "./md/htmlToMarkdown";
import { renderGFM } from "./md/render";

describe("editor tab persistence", () => {
  let editor: Editor | null = null;

  afterEach(() => {
    editor?.destroy();
    editor = null;
  });

  it("preserves a literal tab through editor.getHTML and htmlToMarkdown", () => {
    editor = new Editor({
      extensions: [StarterKit],
      content: "<p></p>",
    });

    editor.commands.insertContent("\talpha");

    const html = editor.getHTML();
    const markdown = htmlToMarkdown(html);

    expect(html).toContain("\talpha");
    expect(markdown).toBe("\talpha\n");
  });

  it("preserves tabs through markdown render, editor load, and save", async () => {
    const rendered = await renderGFM("\talpha\n\nbeta\n");

    editor = new Editor({
      extensions: [StarterKit],
      content: rendered,
    });

    const html = editor.getHTML();
    const markdown = htmlToMarkdown(html);

    expect(markdown).toBe("\talpha\n\nbeta\n");
  });
});
