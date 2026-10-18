# course-k-blog

自作記事とnote・Qiita・Wantedlyの本人記事を、自分のドメインで案内するブログ。React／TypeScript・TanStack StartのSSR、Go API、PostgreSQLを一つのリポジトリで管理する。

## 最初に読むもの

| 知りたいこと | 正本 |
|---|---|
| 今どこまで終わり、何が残るか | [公開までの進捗台帳](docs/blog-publication-roadmap-2026-10-18.md) |
| 何を作り、なぜこの設計にするか | [現行設計](docs/current-design.md) |
| 何をどこに保存し、どう管理するか | [リポジトリ構成と保存方針](docs/repository-organization.md) |
| 文書の役割と読む順序 | [文書索引](docs/README.md) |

進捗は台帳だけで更新する。このREADMEに試験件数・未完了一覧・料金を複製しない。採用UI・AWS対応・設計記録はmainへ統合済み。以後はmainを共通の基準とし、変更は作業ブランチとPRで管理する。

## モノレポの責務

```text
frontend/       Reactの画面、SSR、Markdown、フロントのテスト
backend/        Go API、業務処理、認証、DBアクセス、SQL移行とテスト
infra/          AWS設定、DB初期化、監視コードとそのテスト
scripts/        開発・検証の補助処理
docs/          現行設計と採用理由、進捗台帳、保存方針
.github/        PR運用設定
config/         ローカル秘密値・キャッシュ・作業ファイル（Git対象外）
```

フロントとGoは別々にビルド・配備する。ソースが同じリポジトリにあることは、同じコンテナや同時配備を意味しない。現時点では共通パッケージがなく、ルートのnpm workspaceや追加のビルド統括ツールは導入していない。

ローカルはCaddyでNodeとGoへ振り分ける。AWSの採用構成はHTTP API → VPC Link → Cloud Map → Node／Goの別ECSサービスで、DBは非公開RDS。ComposeのDBはローカル開発用。

## ローカル起動

必要環境：Docker Compose 2.24.4以上、Node.js 24以上、Go 1.27以上、依存パッケージ取得先への接続。

初回だけ、ルートで実行する。設定が存在する場合は生成処理を再実行せず、そのままComposeを起動する。

```sh
node scripts/setup-local.mjs
docker compose up --build
```

一覧は `http://localhost:8080`、管理は `http://localhost:8080/admin`。Originを照合するため、`127.0.0.1`へ読み替えない。生成したローカル認証情報は `config/local-login.txt` で本人が確認し、共有しない。既存設定は自動上書きしない。3ファイルの一部だけがある場合も生成を止める。既存DBを使う場合は対応する設定一式をバックアップから復元し、DBのパスワードだけを生成し直さない。新しい使い捨て環境なら、既存DBと切り離した上で設定一式を作る。プロセス中断で部分生成が残った場合も同様に扱う。

停止は `docker compose down`。データボリュームを消す `down -v` は通常の停止に使わない。ローカルDBの起動成功や再起動後の保持を、本番のバックアップ復元成功と扱わない。

## 検証の種類と実行順

以下はリポジトリのルートから実行する。Node.js 24以上・Go 1.27以上・Python 3.10以上が必要。初回の依存取得にはネットワークを使う。

```sh
npm --prefix frontend ci
go -C backend mod download
```

| 種類 | コマンド | 必要な環境・確認範囲 |
|---|---|---|
| 文書・構文 | `python3 scripts/check-repository.py` | Git管理対象と新規ファイルの相対リンク、JSON・Python構文、差分の空白。外部URLやMarkdownの見出しアンカー、AWSの意味的な妥当性は検査しない |
| フロント基本 | `npm --prefix frontend run check` | 本番ビルド→本体と試験の型検査→runtime→管理API通信→固定APIのSSR。ビルドでルート型を生成するため、この順序で実行 |
| 固定APIの画面 | `npm --prefix frontend run test:ui` | Chrome、一時プロファイル、架空の固定API。画面幅・表示・操作・失敗時の復帰をオフラインで確認。実Go・DBへ接続しない |
| Goの基本 | `TEST_DATABASE_URL= TEST_TLS_DATABASE_URL= go -C backend test -race -v ./...` と `go -C backend vet ./...` | 認証・HTTP・保存契約等。実DBとTLS専用試験はSKIPするため、実PostgreSQLやTLSの成功と扱わない |
| 実PostgreSQL | 下記の専用DB手順 | 移行・公開版分離・競合・セッション等を実DBで確認。テストデータの作成・削除を伴う |
| 設定生成補助 | `node --test scripts/local-config.test.mjs` | 一時ディレクトリで設定の部分生成・失敗時の復帰を確認。実秘密値や既存DBは使わない |
| AWS操作補助 | `PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/aws -p 'test_*.py' -v` | 偽のAWS応答を使い、対象・実行順・秘密値の扱いを確認。AWS CLIを起動しない |
| 監視 | `PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s infra/monitoring -p 'test_*.py' -v` | 応答の判定と配備コピーの一致を確認。実際の通知到達を意味しない |
| ローカル通し確認 | 下記の明示実行手順 | 実ブラウザ→SSR/API→実DB。仮記事を作成・公開・更新・非公開にする |
| AWS実経路 | [AWS手順](infra/README.md) | 対象・課金・公開範囲を確認して別途実施。通常のPR検証から実行しない |

### 実PostgreSQLの専用環境

既存の開発DBや本番DBへ自動接続しない。DB名が `_test` で終わる専用接続先を `TEST_DATABASE_URL` で明示する。次は独立した一時コンテナの例で、Dockerが必要。

```sh
docker run --rm -d --name course-k-blog-postgres-check \
  -e POSTGRES_DB=blog_test -e POSTGRES_USER=blog \
  -e POSTGRES_HOST_AUTH_METHOD=trust \
  -p 127.0.0.1:15432:5432 postgres:17-alpine
docker exec course-k-blog-postgres-check pg_isready -U blog -d blog_test
TEST_DATABASE_URL='postgres://blog@127.0.0.1:15432/blog_test?sslmode=disable' \
  node scripts/test-postgres.mjs
docker stop course-k-blog-postgres-check
```

`pg_isready`の成功後に試験する。15432が使用中なら別の空きポートを選び、接続先も合わせる。trust認証はこの使い捨て・loopback限定DBの設定で、本番や共有環境には使わない。試験成功は再起動・障害時のバックアップ復元成功とは別。

### TLS専用試験

実DBの通常接続とTLS検証は別に実行する。通常のPRジョブはTLS専用環境を持たないため `TestTLSVerification` はSKIPする。
TLSを変更した場合は、localhostだけに有効なサーバー証明書と信頼CAを持つ使い捨てPostgreSQLを別途用意し、`TEST_TLS_DATABASE_URL` に `verify-full` と信頼CAを含む接続先、`TEST_TLS_WRONG_CA_FILE` に別CAのファイルを指定する。DB名は `_test`、ホスト名は `localhost` とする。

```sh
go -C backend test -count=1 -race -v ./internal/postgres -run '^TestTLSVerification$'
```

接続先の指定がなければSKIPする。証明書とDBの準備自動化は未整備なので、通常DB試験の成功をTLS試験の成功として報告しない。

### ブラウザの選択と通し確認

ローカルではChromeを使用する。別環境やCIでPlaywright管理のChromiumを使う場合は、先にfrontendで `npx playwright install --with-deps chromium` を実行し、`PLAYWRIGHT_CHANNEL=chromium` を明示する。ブラウザ起動やアクセスが拒否された場合は未実行として報告し、別手段で同じアクセス制限を回避しない。

[通し試験の入口](scripts/test-local-e2e.mjs)が、ランダムなComposeプロジェクト・専用DBボリューム・一時認証情報・空きポートを用意する。同じDockerfileとCaddy経路を使い、通常利用中のCompose環境を停止・置換しない。

```sh
node scripts/test-local-e2e.mjs
```

ビルドと起動、実ブラウザでの仮記事の保存・公開・更新・非公開・ログアウトを確認し、成功・失敗ともそのプロジェクトのコンテナ・ボリューム・ローカルイメージを撤去する。アプリの既存データは使わない。一時認証情報の親ディレクトリは0700で隔離し、Linuxの非rootコンテナにも読めるよう、コンテナへ渡す2ファイルだけを0444にする。通常設定の生成は0600のまま。ビルド等の子処理は10分で打ち切る。
強制終了やDocker停止では後始末できない場合があるため、その実行で表示された `blog-e2e-…` のプロジェクトだけを確認・撤去する。清掃失敗時は対象名と一時設定の場所を案内する。
出力はGit対象外の `frontend/test-results/` 等へ保存する。

自分で専用環境を管理する場合は `RUN_LOCAL_E2E=1 BLOG_E2E_ORIGIN=http://localhost:対象ポート BLOG_E2E_LOGIN_FILE=専用ログイン情報ファイル npm --prefix frontend run test:e2e` で明示する。この直接実行ではデータの撤去を呼出側が担う。宛先はloopback HTTPに限定し、実行フラグがなければ記事作成前に失敗する。

`node scripts/check-restart.mjs` は対象ComposeのDB・API・Webを再起動する別の明示操作であり、通常の検査には含めない。

## PRで確認する範囲

[検証ワークフロー](.github/workflows/checks.yml)はPR・main更新・手動実行を対象とし、次の4ジョブを分けて実行する。小規模なモノレポのためパス条件で省略せず、対応版の取り違えを防ぐ。

- **Repository and infrastructure**：設定生成、文書・構文、AWS操作補助、監視の試験、AWSへ接続しない計画表示。
- **Frontend and offline UI**：lockfileから依存取得、基本検査、Playwright管理のChromiumによる固定API画面試験。
- **Go and real PostgreSQL**：Goの基本検査と、ジョブ専用の一時PostgreSQLサービスへの結合試験。実DBの接続値を渡してキャッシュを使わず実行し、SKIPを結合成功と扱わない。

- **Browser, SSR, Go and PostgreSQL**：専用Compose環境を構築し、仮記事で実ブラウザ・本番SSR・実API・DBの通し確認と撤去を行う。

ワークフローは読み取り権限で動き、AWS認証情報・デプロイ処理・実記事の公開処理を含めない。DockerのARM64ビルド、実AWSの配備・復元・通知・容量は別の確認範囲。GitHub上の実行結果を確認するまでは、CI成功・必須チェック設定済みとは表現しない。mainへの自動デプロイはない。

Actionsの利用方法は[checkout](https://github.com/actions/checkout)、[setup-node](https://github.com/actions/setup-node)、[setup-go](https://github.com/actions/setup-go)の公式資料を参照する。CIでの実行時間はGitHubの利用枠を使うため、反復実行は変更に必要な範囲とする。

## 変更を進める手順

1. mainを基準に作業ブランチを作り、変更する仕様と完了条件を確認する。無関係なローカル変更を混ぜない。
2. コードと採用理由・仕様を同じ変更で揃える。文書は現行の結果を更新し、作業経緯のための文書を増やさない。
3. 変更に対応する検証を実施し、成功・失敗・SKIP・未実行を分けてPRへ記載する。画面/API境界を変えたら、固定API試験に加えて実API/DBの通し確認を対象にする。
4. PRの差分・上記4ジョブ・未確認事項を確認してmainへ取り込む。GitHub側のマージ制限は別途設定が必要で、手順を文書化しただけでは強制されない。
5. 配備・費用・DNS・権限変更・実記事の公開は、具体的な対象と影響を確認して別の操作として実施する。
