import { createFileRoute } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { Markdown } from '../components/Markdown'
import type { Draft } from '../lib/types'

export const Route = createFileRoute('/admin')({
  head: () => ({ meta: [{ title: '記事管理 — course_k' }, { name: 'robots', content: 'noindex, nofollow' }] }),
  component: Admin,
})
class ApiError extends Error { constructor(public status: number, message: string) { super(message) } }
async function api<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
  const response = await fetch(`/api${path}`, { method, credentials: 'same-origin', cache: 'no-store', headers: { 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) })
  const data = await response.json()
  if (!response.ok) throw new ApiError(response.status, data.error || '処理できませんでした。')
  return data
}
function Admin() {
  const [signedIn, setSignedIn] = useState<boolean | null>(null)
  const [password, setPassword] = useState('')
  const [posts, setPosts] = useState<Draft[]>([])
  const [selected, setSelected] = useState<Draft | null>(null)
  const [title, setTitle] = useState('')
  const [markdown, setMarkdown] = useState('')
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [editing, setEditing] = useState(false)
  const dirty = editing && (selected ? title !== selected.title || markdown !== selected.markdown : title !== '' || markdown !== '')
  useEffect(() => { let active = true; api('/session').then(() => { if (active) setSignedIn(true) }).catch(e => { if (active) { setSignedIn(false); if (!(e instanceof ApiError && e.status === 401)) setError('接続を確認できませんでした。再読み込みしてください。') } }); return () => { active = false } }, [])
  useEffect(() => { if (signedIn) api<Draft[]>('/admin/posts').then(setPosts).catch(e => setError(e.message)) }, [signedIn])
  useEffect(() => { if (!dirty) return; const warn = (e: BeforeUnloadEvent) => { e.preventDefault(); e.returnValue = '' }; window.addEventListener('beforeunload', warn); return () => window.removeEventListener('beforeunload', warn) }, [dirty])
  async function act(fn: () => Promise<void>) {
    setBusy(true); setError(''); setMessage('')
    try { await fn() } catch (e) { setError(e instanceof Error ? e.message : '処理できませんでした。'); if (e instanceof ApiError && e.status === 401) setSignedIn(false) } finally { setBusy(false) }
  }
  function choose(post: Draft | null) {
    if (dirty && !window.confirm('保存していない変更を破棄しますか？')) return
    setSelected(post); setTitle(post?.title || ''); setMarkdown(post?.markdown || ''); setEditing(true); setMessage(''); setError('')
  }
  async function refresh(post: Draft) { setSelected(post); setPosts(await api<Draft[]>('/admin/posts')) }
  async function save(): Promise<Draft> {
    const result = selected
      ? await api<Draft>(`/admin/posts/${selected.id}`, 'PUT', { title, markdown, version: selected.version })
      : await api<Draft>('/admin/posts', 'POST', { title, markdown })
    await refresh(result); return result
  }
  if (signedIn === null) return <p className="notice" role="status">ログイン状態を確認しています…</p>
  if (!signedIn) return <section className="login"><p className="eyebrow">PRIVATE</p><h1>管理者ログイン</h1><p className="muted">記事を書いたり、公開状態を管理できます。</p><form onSubmit={e => { e.preventDefault(); void act(async () => { await api('/session', 'POST', { password }); setPassword(''); setSignedIn(true) }) }}><label htmlFor="password">パスワード</label><input id="password" type="password" autoComplete="current-password" required maxLength={1024} value={password} onChange={e => setPassword(e.target.value)} /><button disabled={busy}>{busy ? '確認中…' : 'ログイン'}</button></form>{error && <p role="alert" className="error">{error}</p>}</section>
  return <section className="admin"><div className="admin-heading"><div><p className="eyebrow">WORKSPACE</p><h1>記事を書く</h1></div><button className="secondary" disabled={busy} onClick={() => { if (dirty && !window.confirm('未保存の変更を破棄してログアウトしますか？')) return; void act(async () => { await api('/session', 'DELETE', {}); setSignedIn(false); setPosts([]); setSelected(null); setEditing(false); setTitle(''); setMarkdown('') }) }}>ログアウト</button></div>
    <div className="editor-layout"><aside><button disabled={busy} onClick={() => choose(null)}>＋ 新しい記事</button><ul className="draft-list">{posts.map(p => <li key={p.id}><button className={selected?.id === p.id ? 'draft active' : 'draft'} disabled={busy} onClick={() => choose(p)}><span>{p.title}</span><small>{p.published ? '公開中' : '下書き'}{p.published && p.hasUnpublishedChanges ? '・未公開の変更あり' : ''}</small></button></li>)}</ul></aside>
      <div>{editing ? <><label htmlFor="title">タイトル</label><input id="title" maxLength={200} value={title} onChange={e => setTitle(e.target.value)} disabled={busy} /><div className="editor-columns"><div><label htmlFor="markdown">Markdown</label><textarea id="markdown" value={markdown} onChange={e => setMarkdown(e.target.value)} disabled={busy} spellCheck={false} /></div><section className="preview" aria-label="プレビュー"><h2>プレビュー</h2><Markdown text={markdown || 'ここにプレビューが表示されます。'} /></section></div>
      <div className="actions"><button disabled={busy || !title.trim()} onClick={() => void act(async () => { await save(); setMessage('下書きを保存しました。公開中の記事は変わりません。') })}>下書きを保存</button><button className="secondary" disabled={busy || !title.trim()} onClick={() => void act(async () => { const p = await save(); const published = await api<Draft>(`/admin/posts/${p.id}/publish`, 'POST', { version: p.version }); await refresh(published); setMessage('公開しました。') })}>保存して公開</button>{selected?.published && <button className="quiet" disabled={busy} onClick={() => { if (!window.confirm('この記事を非公開に戻しますか？')) return; void act(async () => { await refresh(await api<Draft>(`/admin/posts/${selected.id}/unpublish`, 'POST', { version: selected.version })); setMessage('非公開にしました。') }) }}>非公開に戻す</button>}</div><p className="muted" role="status">{busy ? '処理しています…' : message || (dirty ? '保存していない変更があります。' : '編集内容は自動保存されません。')}</p></> : <div className="empty"><h2>書きたいことを、ここから。</h2><p>記事を選ぶか、新しい記事を作成してください。</p></div>}{error && <p className="error" role="alert">{error}</p>}</div>
    </div>
  </section>
}
