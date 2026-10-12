import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { describe, expect, it } from "vitest"
import { MarkdownContent } from "./markdown-content"

function render(content: string) {
  return renderToStaticMarkup(createElement(MarkdownContent, { content }))
}

describe("MarkdownContent", () => {
  it("renders headings, emphasis, lists, quotes and code", () => {
    const html = render('# 标题\n\n**加粗**、*斜体* 和 `inline`\n\n- 第一项\n  - 嵌套项\n\n1. 步骤\n\n> 引用\n\n```go\nfmt.Println("<hello>")\n```')
    for (const expected of ["<h1>标题</h1>", "<strong>加粗</strong>", "<em>斜体</em>", "<code>inline</code>", "<ul>", "<ol>", "<blockquote>", '<pre tabindex="0"><code class="language-go">', "&lt;hello&gt;"]) {
      expect(html).toContain(expected)
    }
  })

  it("supports GFM tables, strikethrough and read-only task lists", () => {
    const html = render("| 模型 | 状态 |\n| --- | ---: |\n| mock | 可用 |\n\n~~旧内容~~\n\n- [x] 完成\n- [ ] 待办")
    expect(html).toContain('class="markdown-table" role="region" aria-label="表格" tabindex="0"')
    expect(html).toContain("<table>")
    expect(html).toContain('<th style="text-align:right">状态</th>')
    expect(html).toContain("<del>旧内容</del>")
    expect(html).toContain('type="checkbox" disabled="" checked=""')
    expect(html).toContain('type="checkbox" disabled=""')
  })

  it("opens safe links separately without automatically loading images", () => {
    const html = render('[文档](https://example.com/docs "说明")\n\n![示意图](https://example.com/image.png)')
    expect(html).toContain('href="https://example.com/docs" title="说明" target="_blank" rel="noopener noreferrer"')
    expect(html).toContain('href="https://example.com/image.png" target="_blank" rel="noopener noreferrer">示意图</a>')
    expect(html).not.toContain("<img")
    expect(html).not.toContain("<link")
  })

  it("does not execute raw HTML or unsafe links from model output", () => {
    const html = render('<script>alert(1)</script>\n\n<img src="x" onerror="alert(1)">\n\n<iframe src="https://example.com"></iframe>\n\n[危险](javascript:alert%281%29)\n\n![危险图片](data:image/svg+xml;base64,PHN2Zz4=)\n\n[正常](https://example.com)')
    for (const unsafe of ["<script", "<img", "<iframe", "onerror", "javascript:", "data:image"]) {
      expect(html).not.toContain(unsafe)
    }
    expect(html).toContain('href="https://example.com"')
  })

  it("can render every streaming prefix, including unclosed code fences", () => {
    const content = "## 实时回复\n\n**加粗**\n\n```go\nfmt.Println(1)\n```\n\n| A | B |\n| - | - |\n| 1 | 2 |"
    for (let end = 0; end <= content.length; end++) {
      expect(() => render(content.slice(0, end))).not.toThrow()
    }
    expect(render("```go\nfmt.Print")).toContain('<code class="language-go">fmt.Print\n</code>')
    expect(render(content)).toContain("<table>")
  })
})
