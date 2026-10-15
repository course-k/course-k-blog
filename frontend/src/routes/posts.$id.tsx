import { createFileRoute, Link } from '@tanstack/react-router'
import { getPost } from '../lib/posts'
import { dateLabel } from '../lib/types'
import { Markdown } from '../components/Markdown'

export const Route = createFileRoute('/posts/$id')({
  loader: ({ params }) => getPost({ data: params.id }),
  head: ({ loaderData }) => ({ meta: [{ title: loaderData ? `${loaderData.title} — course_k` : '記事 — course_k' }] }),
  component: Post,
})
function Post() {
  const post = Route.useLoaderData()
  return <article className="reading"><Link to="/" search={{ page: 1 }} className="back">← 記事一覧</Link><header><p className="post-meta"><time dateTime={post.publishedAt}>{dateLabel(post.publishedAt)}</time></p><h1>{post.title}</h1>{post.updatedAt !== post.publishedAt && <p className="muted">更新：{dateLabel(post.updatedAt)}</p>}</header><Markdown text={post.markdown} /></article>
}
