import type { DocEntry } from "./catalog";
import { DOCS } from "./catalog";

/** Vite loads repo docs as raw markdown (see vite.config resolve alias). */
const rawModules = import.meta.glob("@repo-docs/**/*.md", {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

function moduleKeyFor(file: string): string | undefined {
  const suffix = `/docs/${file}`;
  return Object.keys(rawModules).find(
    (key) => key.endsWith(suffix) || key.endsWith(file),
  );
}

export function loadDocMarkdown(entry: DocEntry): string | null {
  const key = moduleKeyFor(entry.file);
  if (!key) return null;
  return rawModules[key] ?? null;
}

export function listLoadedDocSlugs(): string[] {
  return DOCS.filter((d) => loadDocMarkdown(d) !== null).map((d) => d.slug);
}
