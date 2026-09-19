import { Component, memo, type ReactNode } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeSanitize from "rehype-sanitize";

interface Props {
  /** Complete (archived/historical) agent or run prose. Never raw HTML. */
  content: string;
  className?: string;
}

// Plain-text fallback when Markdown rendering throws (e.g. a hostile or
// pathological payload trips the parser). react-markdown almost never
// throws; this keeps archived text readable no matter what.
class MarkdownFallback extends Component<{ content: string; children?: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render(): ReactNode {
    if (this.state.failed) {
      return <pre className="markdown-fallback">{this.props.content}</pre>;
    }
    return this.props.children;
  }
}

// Sanitized GFM rendering for archived / historical agent text.
// Uses react-markdown + remark-gfm + rehype-sanitize (default schema strips
// <script>, <style>, and javascript:/data: URLs); no dangerouslySetInnerHTML
// of model output anywhere in this path.
//
// Streaming note: chat transcripts here are complete persisted messages
// (poll-based refresh), not token streams, so progressive rendering is the
// less janky choice — react-markdown tolerates partial fences/lists while a
// turn is still running. Live log streams (LiveStream) stay plain text.
function MarkdownBodyInner({ content, className }: Props) {
  if (!content.trim()) {
    return null;
  }
  return (
    <MarkdownFallback content={content}>
      <div className={className ? `markdown-body ${className}` : "markdown-body"}>
        <ReactMarkdown
          remarkPlugins={[remarkGfm]}
          rehypePlugins={[rehypeSanitize]}
          components={{
            a: ({ href, children }) => {
              const url = typeof href === "string" ? href : "";
              if (/^https?:\/\//i.test(url)) {
                return (
                  <a href={url} target="_blank" rel="noreferrer noopener">
                    {children}
                  </a>
                );
              }
              if (url.startsWith("#") || url.startsWith("/") || /^mailto:/i.test(url)) {
                return <a href={url}>{children}</a>;
              }
              // Drop javascript:, data:, and other unsafe schemes; keep text.
              return <span>{children}</span>;
            },
          }}
        >
          {content}
        </ReactMarkdown>
      </div>
    </MarkdownFallback>
  );
}

export const MarkdownBody = memo(MarkdownBodyInner);
