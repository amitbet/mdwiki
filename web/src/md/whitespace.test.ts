import { describe, expect, it } from "vitest";
import { htmlToMarkdown } from "./htmlToMarkdown";
import { renderGFM } from "./render";
import { normalizeMarkdownForComparison } from "./whitespace";

describe("normalizeMarkdownForComparison", () => {
  it("treats whitespace-only edits as meaningful", () => {
    expect(normalizeMarkdownForComparison("alpha beta\n")).not.toBe(normalizeMarkdownForComparison("alpha  beta\n"));
    expect(normalizeMarkdownForComparison("alpha\nbeta\n")).not.toBe(normalizeMarkdownForComparison("alpha\n\nbeta\n"));
    expect(normalizeMarkdownForComparison("alpha\n")).not.toBe(normalizeMarkdownForComparison(" alpha\n"));
  });

  it("normalizes only line endings and final newline shape", () => {
    expect(normalizeMarkdownForComparison("alpha\r\nbeta")).toBe("alpha\nbeta\n");
    expect(normalizeMarkdownForComparison("")).toBe("");
  });
});

describe("htmlToMarkdown", () => {
  it("preserves 4-space indentation as a tab and keeps repeated spaces", () => {
    const markdown = htmlToMarkdown("<p>    alpha   beta</p>");
    expect(markdown).toContain("\talpha");
    expect(markdown).toContain("alpha &nbsp;&nbsp;beta");
  });

  it("preserves blank lines", () => {
    const markdown = htmlToMarkdown("<p>first</p><p><br></p><p>second</p>");
    expect(markdown).toContain("<!-- mdwiki:blank-line -->");
  });
});

describe("renderGFM", () => {
  it("restores explicit blank lines", async () => {
    const html = await renderGFM("first\n<!-- mdwiki:blank-line -->\nsecond\n");
    expect(html).toContain("<p>first</p>");
    expect(html).toContain("<p><br></p>");
    expect(html).toContain("<p>second</p>");
  });

  it("renders preserved spaces visibly", async () => {
    const html = await renderGFM("&nbsp;&nbsp;alpha &nbsp;&nbsp;beta\n");
    expect(html).toContain("\u00a0\u00a0alpha");
    expect(html).toContain(`alpha ${"\u00a0".repeat(2)}beta`);
  });

  it("keeps literal tab indentation intact", async () => {
    expect(htmlToMarkdown("<p>\talpha</p>")).toBe("\talpha\n");
    const html = await renderGFM("\talpha\n");
    expect(html).toContain("\u00a0\u00a0\u00a0\u00a0alpha");
  });

  it("persists a tab through html-to-markdown and render round-trip", async () => {
    const markdown = htmlToMarkdown("<p>\talpha</p><p>beta</p>");
    expect(markdown).toBe("\talpha\n\nbeta\n");

    const html = await renderGFM(markdown);
    expect(html).toContain("\u00a0\u00a0\u00a0\u00a0alpha");
    expect(html).toContain("<p>beta</p>");
  });
});
