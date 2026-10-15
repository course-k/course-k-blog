import { createFileRoute, Link } from '@tanstack/react-router'
import { listPosts } from '../lib/posts'
import { dateLabel } from '../lib/types'

export const Route = createFileRoute('/')({
  validateSearch: (search: Record<string, unknown>) => ({ page: Math.min(10000, Math.max(1, Number.isInteger(Number(search.page)) ? Number(search.page) : 1)) }),
  loaderDeps: ({ search: { page } }) => ({ page }),
  loader: ({ deps }) => listPosts({ data: deps.page }),
  component: Home,
})
function Home() {
  const posts = Route.useLoaderData()
  const { page } = Route.useSearch()
  return <><section className="intro"><p className="eyebrow">JOURNAL</p><h1>記録と考えごと</h1><p>技術のこと、ものづくりのこと。<br />日々の学びを、少しずつ残していきます。</p></section>
    <section aria-label="記事一覧"><div className="section-label"><h2>記事</h2><span>新しい順</span></div>
      {posts.length === 0 ? <div className="empty"><h3>{page === 1 ? '最初の記事を準備しています' : 'このページの記事はありません'}</h3><p>公開された記事が、ここに並びます。</p></div> : posts.map(post => <article className="post-row" key={post.id}><div className="post-meta"><time dateTime={post.publishedAt}>{dateLabel(post.publishedAt)}</time><span className="badge">このブログ</span></div><h3><Link to="/posts/$id" params={{ id: post.id }}>{post.title}<span aria-hidden="true">↗</span></Link></h3></article>)}
      <nav className="pagination" aria-label="ページ">{page > 1 && <Link to="/" search={{ page: page - 1 }}>← 前のページ</Link>}{posts.length === 20 && <Link to="/" search={{ page: page + 1 }}>次のページ →</Link>}</nav>
    </section></>
}
