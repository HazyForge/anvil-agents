import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeSlug from "rehype-slug";
import { Link } from "react-router-dom";
import type { Components } from "react-markdown";

type Props = {
  markdown: string;
};

const components: Components = {
  a({ href, children }) {
    if (!href) return <a>{children}</a>;
    if (href.startsWith("/docs/") || href.startsWith("/")) {
      return <Link to={href}>{children}</Link>;
    }
    if (href.startsWith("#")) {
      return <a href={href}>{children}</a>;
    }
    return (
      <a href={href} target="_blank" rel="noopener noreferrer">
        {children}
      </a>
    );
  },
  img({ src, alt }) {
    if (!src) return null;
    return (
      <figure className="doc-figure">
        <img src={src} alt={alt || ""} loading="lazy" />
        {alt ? <figcaption className="mono">{alt}</figcaption> : null}
      </figure>
    );
  },
  table({ children }) {
    return (
      <div className="doc-table-wrap">
        <table>{children}</table>
      </div>
    );
  },
  pre({ children }) {
    return <pre className="doc-pre">{children}</pre>;
  },
  code({ className, children, ...props }) {
    const isBlock = Boolean(className);
    if (isBlock) {
      return (
        <code className={className} {...props}>
          {children}
        </code>
      );
    }
    return (
      <code className="doc-inline-code" {...props}>
        {children}
      </code>
    );
  },
};

export default function DocMarkdown({ markdown }: Props) {
  return (
    <div className="doc-prose">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeSlug]}
        components={components}
      >
        {markdown}
      </ReactMarkdown>
    </div>
  );
}
