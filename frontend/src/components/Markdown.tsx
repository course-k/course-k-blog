import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

// Preview and published pages share this renderer. Raw HTML is not enabled.
export function Markdown({ text }: { text: string }) {
  return <div className="prose"><ReactMarkdown remarkPlugins={[remarkGfm]} skipHtml>{text}</ReactMarkdown></div>
}
