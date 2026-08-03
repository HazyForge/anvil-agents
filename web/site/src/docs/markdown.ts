/** Rewrite markdown links/images so they work on the product site. */
export function rewriteDocMarkdown(source: string): string {
  let out = source;

  // Images under docs/images → public /docs/*
  out = out.replace(
    /!\[([^\]]*)\]\((?:\.\/)?images\/([^)]+)\)/g,
    "![$1](/docs/$2)",
  );

  // Relative markdown links to other docs → /docs/<slug>
  out = out.replace(
    /\[([^\]]+)\]\((?:\.\.\/)*docs\/([^)#]+?)(?:\.md)?(#[^)]+)?\)/g,
    (_m, label: string, path: string, hash: string = "") => {
      const slug = path.replace(/\.md$/, "").replace(/^\/+/, "");
      return `[${label}](/docs/${slug}${hash})`;
    },
  );

  // Same-directory .md links (e.g. architecture.md)
  out = out.replace(
    /\[([^\]]+)\]\((?!https?:|\/|#|mailto:)([^)#]+)\.md(#[^)]+)?\)/g,
    (_m, label: string, path: string, hash: string = "") => {
      const slug = path
        .replace(/^\.\//, "")
        .replace(/^\.\.\//, "")
        .replace(/\.md$/, "");
      return `[${label}](/docs/${slug}${hash})`;
    },
  );

  return out;
}

export function extractTitle(markdown: string, fallback: string): string {
  const match = markdown.match(/^#\s+(.+)$/m);
  return match?.[1]?.trim() || fallback;
}
