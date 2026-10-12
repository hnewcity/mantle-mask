import { memo } from "react"
import ReactMarkdown, { type Components } from "react-markdown"
import remarkGfm from "remark-gfm"

const components: Components = {
  a: ({ children, href, title }) => <a href={href} title={title} target="_blank" rel="noopener noreferrer">{children}</a>,
  // Model output is untrusted: do not automatically fetch remote images.
  img: ({ src, alt }) => <a href={src} target="_blank" rel="noopener noreferrer">{alt || "查看图片"}</a>,
  pre: ({ children }) => <pre tabIndex={0}>{children}</pre>,
  table: ({ children }) => <div className="markdown-table" role="region" aria-label="表格" tabIndex={0}><table>{children}</table></div>,
}
const remarkPlugins = [remarkGfm]

export const MarkdownContent = memo(function MarkdownContent({ content }: { content: string }) {
  return <div className="markdown-content"><ReactMarkdown remarkPlugins={remarkPlugins} components={components} skipHtml>{content}</ReactMarkdown></div>
})
