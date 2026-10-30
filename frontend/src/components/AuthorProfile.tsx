export function AuthorProfile() {
  return (
    <section className="author-profile" aria-label="プロフィール">
      <span className="author-avatar" aria-hidden="true">k</span>
      <div className="author-copy">
        <h1><img className="blog-wordmark" src="/brand/wordmark.png" width="2027" height="254" alt="course_kのブログ" /></h1>
        <p>技術のこと、日々のこと。このブログと、Qiita・note・Wantedlyに書いたものをまとめています。</p>
        <nav className="author-links" aria-label="course_kの公開プロフィール">
          <a href="https://github.com/course-k" target="_blank" rel="noopener noreferrer" aria-label="GitHubのプロフィール、新しいタブで開く">GitHub ↗</a>
          <a href="https://qiita.com/course_k" target="_blank" rel="noopener noreferrer" aria-label="Qiitaのプロフィール、新しいタブで開く">Qiita ↗</a>
          <a href="https://note.com/course__k" target="_blank" rel="noopener noreferrer" aria-label="noteのプロフィール、新しいタブで開く">note ↗</a>
        </nav>
      </div>
    </section>
  );
}
