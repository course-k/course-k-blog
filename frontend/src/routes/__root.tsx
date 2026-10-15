import { createRootRoute, HeadContent, Link, Outlet, Scripts } from '@tanstack/react-router'
import '../styles.css'

export const Route = createRootRoute({
  head: () => ({ meta: [
    { charSet: 'utf-8' }, { name: 'viewport', content: 'width=device-width, initial-scale=1' },
    { title: 'course_k — 記録と考えごと' },
    { name: 'description', content: '技術とものづくりについての記録。' },
  ] }),
  component: Root,
  notFoundComponent: () => <section className="notice"><h1>記事が見つかりません</h1><p>URLをご確認ください。</p><Link to="/" search={{ page: 1 }}>記事一覧へ</Link></section>,
  errorComponent: () => <section className="notice" role="alert"><h1>読み込めませんでした</h1><p>時間をおいて再度お試しください。</p><a href="/">記事一覧へ</a></section>,
})
function Root() {
  return <html lang="ja"><head><HeadContent /></head><body>
    <a className="skip" href="#main">本文へ移動</a>
    <header className="site-header"><Link to="/" search={{ page: 1 }} className="brand">course_k<span>記録と考えごと</span></Link><nav aria-label="メイン"><Link to="/" search={{ page: 1 }}>記事</Link><Link to="/admin">管理</Link></nav></header>
    <main id="main"><Outlet /></main>
    <footer>course_k <span>書いて、考えて、つくる。</span></footer><Scripts />
  </body></html>
}
