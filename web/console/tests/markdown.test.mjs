/**
 * Markdown rendering smoke tests for the Anvil Agents console run surfaces.
 *
 * Run detail prose (decision summary, report summary/detail, humanFollowUp,
 * run.output) and standing-chat archives render as sanitized GFM Markdown via
 * react-markdown + remark-gfm + rehype-sanitize (see
 * src/components/MarkdownBody.tsx). No custom Markdown parser.
 *
 * Run: cd web/console && node --test tests/markdown.test.mjs
 */
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import ReactMarkdown from "react-markdown";
import rehypeSanitize from "rehype-sanitize";
import remarkGfm from "remark-gfm";

const source = readFileSync(
  new URL("../src/components/MarkdownBody.tsx", import.meta.url),
  "utf8",
);

const runDetailSource = readFileSync(
  new URL("../src/components/RunDetail.tsx", import.meta.url),
  "utf8",
);

// Same link policy as MarkdownBody: http(s) opens in a new tab with
// noreferrer+noopener; unsafe schemes render as text.
function Link({ href, children }) {
  const url = typeof href === "string" ? href : "";
  if (/^https?:\/\//i.test(url)) {
    return React.createElement(
      "a",
      { href: url, target: "_blank", rel: "noreferrer noopener" },
      children,
    );
  }
  if (
    url.startsWith("#") ||
    url.startsWith("/") ||
    /^mailto:/i.test(url)
  ) {
    return React.createElement("a", { href: url }, children);
  }
  return React.createElement("span", null, children);
}

function render(content) {
  return renderToStaticMarkup(
    React.createElement(
      ReactMarkdown,
      {
        remarkPlugins: [remarkGfm],
        rehypePlugins: [rehypeSanitize],
        components: { a: Link },
      },
      content,
    ),
  );
}

test("MarkdownBody uses maintained libraries, not a hand-rolled parser", () => {
  for (const lib of ["react-markdown", "remark-gfm", "rehype-sanitize"]) {
    assert.ok(
      source.includes(lib),
      `MarkdownBody.tsx must import ${lib}`,
    );
  }
  assert.ok(
    !/dangerouslySetInnerHTML\s*=\s*\{/.test(source),
    "model output must never use dangerouslySetInnerHTML",
  );
});

test("RunDetail renders run prose through MarkdownBody", () => {
  for (const surface of [
    "decision.summary",
    "report.summary",
    "report.detail",
    "humanFollowUp",
    "run.output",
  ]) {
    assert.ok(
      runDetailSource.includes("MarkdownBody"),
      `RunDetail must render ${surface} via MarkdownBody`,
    );
  }
  assert.ok(
    !/dangerouslySetInnerHTML\s*=\s*\{/.test(runDetailSource),
    "run prose must never use dangerouslySetInnerHTML",
  );
});

test("headings, bold/italic, and lists render", () => {
  const html = render("# Decision\n\nSome **bold** and *italic* text.\n\n- one\n- two\n");
  assert.ok(html.includes("<h1>Decision</h1>"), `missing h1: ${html}`);
  assert.ok(html.includes("<strong>bold</strong>"), `missing bold: ${html}`);
  assert.ok(html.includes("<ul>"), `missing list: ${html}`);
});

test("fenced code blocks render as pre/code", () => {
  const html = render("```ts\nconst x = 1;\n```\n");
  assert.ok(html.includes("<pre>"), `missing pre: ${html}`);
  assert.ok(html.includes("<code"), `missing code: ${html}`);
  assert.ok(html.includes("const x = 1;"), `missing code body: ${html}`);
});

test("GFM tables render", () => {
  const html = render("| a | b |\n|---|---|\n| 1 | 2 |\n");
  assert.ok(html.includes("<table>"), `missing table: ${html}`);
  assert.ok(html.includes("<th>a</th>"), `missing th: ${html}`);
  assert.ok(html.includes("<td>1</td>"), `missing td: ${html}`);
});

test("script tags and unsafe URLs are stripped", () => {
  const html = render(
    'Hello<script>alert("xss")</script>\n\n[evil](javascript:alert(1))\n\n[fine](https://example.com)\n',
  );
  assert.ok(!html.includes("<script"), `script not stripped: ${html}`);
  assert.ok(!html.includes("javascript:"), `unsafe URL kept: ${html}`);
  assert.ok(
    html.includes('target="_blank"') && html.includes("noreferrer"),
    `http link missing safe target/rel: ${html}`,
  );
});

test("plain non-Markdown text stays readable", () => {
  const html = render("Just a plain status line.\nSecond line.");
  assert.ok(
    html.includes("Just a plain status line."),
    `plain text lost: ${html}`,
  );
});
