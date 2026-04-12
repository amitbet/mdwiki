export function normalizeMarkdownForComparison(input: string): string {
  const normalized = input.replace(/\r\n?/g, "\n");
  if (normalized.length === 0) {
    return "";
  }
  return normalized.endsWith("\n") ? normalized : `${normalized}\n`;
}
