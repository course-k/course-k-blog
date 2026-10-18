# course-k-blog

自作記事とnote・Qiita・Wantedlyの本人記事を、自分のドメインで案内するブログ。React／TypeScript・TanStack StartのSSR、Go API、PostgreSQLを一つのリポジトリで管理する。

## 最初に読むもの

| 知りたいこと | 正本 |
|---|---|
| 今どこまで終わり、何が残るか | [公開までの進捗台帳](docs/blog-publication-roadmap-2026-10-18.md) |
| 何を作り、なぜこの設計にするか | [現行設計](docs/current-design.md) |
| 何をどこに保存し、どのブランチを見るか | [リポジトリ構成・情報の棚卸し](docs/repository-organization.md) |
| 過去の比較・検証結果を探す | [文書索引](docs/README.md) |

進捗は台帳だけで更新する。このREADMEに試験件数・未完了一覧・料金を複製しない。現在のブランチとmain、新UIのブランチは同じ内容ではないため、対象は台帳で確認する。

## モノレポの責務

```text
frontend/       Reactの画面、SSR、Markdown、フロントのテスト
backend/        Go API、業務処理、認証、DBアクセス、SQL移行とテスト
infra/          AWS設定、DB初期化、監視コードとそのテスト
scripts/        開発・検証の補助処理
docs/          現行設計、進捗台帳、判断・検証の記録
.github/        PR運用設定
config/         ローカル秘密値・キャッシュ・作業ファイル（Git対象外）
```

フロントとGoは別々にビルド・配備する。ソースが同じリポジトリにあることは、同じコンテナや同時配備を意味しない。現時点では共通パッケージがなく、ルートのnpm workspaceや追加のビルド統括ツールは導入していない。

ローカルはCaddyでNodeとGoへ振り分ける。AWSの採用構成はHTTP API → VPC Link → Cloud Map → Node／Goの別ECSサービスで、DBは非公開RDS。ComposeのDBはローカル開発用。

## ローカル起動

必要環境：Docker Compose、Node.js 24以上、Go 1.27以上、依存パッケージ取得先への接続。

初回だけ、ルートで実行する。設定が存在する場合は生成処理を再実行せず、そのままComposeを起動する。

```sh
node scripts/setup-local.mjs
docker compose up --build
```

一覧は `http://localhost:8080`、管理は `http://localhost:8080/admin`。Originを照合するため、`127.0.0.1`へ読み替えない。生成したローカル認証情報は `config/local-login.txt` で本人が確認し、共有しない。既存設定は自動上書きしない。

停止は `docker compose down`。データボリュームを消す `down -v` は通常の停止に使わない。ローカルDBの起動成功や再起動後の保持を、本番のバックアップ復元成功と扱わない。

## 検証コマンド

Goの単体検証はbackendで実行する。

```sh
go mod download
go test -race -v ./...
go vet ./...
```

PostgreSQL結合試験は `TEST_DATABASE_URL` がなければSKIP。ローカルComposeの専用テストDB起動後、ルートで `node scripts/test-postgres.mjs` を実行する。これは専用試験用データを作成・削除する検証である。

フロントはfrontendで実行する。

```sh
npm ci
npm run build
npm run typecheck
npm run test:runtime
```

ルート型はビルドで生成される。`npm run test:e2e` は実Go・DBとブラウザを必要とし、UIブランチの固定APIによる画面試験と区別する。環境のブラウザアクセス拒否を回避する目的で実行しない。新UI固有のコマンドはUIブランチの実装記録を参照する。

AWS用設定は `infra/` にあるが、テンプレートの存在だけで本番配備済みとは判断しない。課金資源の作成、独自ドメイン変更、実記事の公開は、このREADMEのローカル起動手順に含まれない。
