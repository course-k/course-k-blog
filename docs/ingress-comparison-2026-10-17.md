# 入口の振り分け：類似例と比較

2026年10月17日。調査・提案のみ。採用未承認、実装・AWS設定の変更なし。

## 確認した前例と証拠の範囲

| 一次情報 | 確認できたこと | 証明しないこと |
|---|---|---|
| [Go API＋Next.jsの公開課題](https://github.com/fbg871/NI-coding-assignment/blob/main/README.md) | 作者の構成説明に、Nginxが/apiをGo、それ以外をNext.jsへ転送、Compose・MySQLと明記 | 採用課題用であり大規模本番実績ではない。READMEの説明を確認したが、今回このコードを実行していない。Next.jsを使うことだけで全ページSSRとは断言しない |
| [GoFast CLI](https://github.com/gofast-live/gofast-cli) | Go系バックエンドの生成ツールにTanStackクライアントの追加・起動コマンドがある | テンプレートの存在であり、実サービスの規模・安定性や今回と同じプロキシ構成の証拠ではない |
| [Next.js公式self-hosting](https://nextjs.org/docs/app/guides/self-hosting#reverse-proxy) | Nodeサーバーの手前にNginx等のリバースプロキシを置く推奨と、その保護上の理由がある | TanStack公式推奨ではない。今回の設定がその保護を全て実施する証拠でもない |
| [AWSのRS Components事例・2020年](https://aws.amazon.com/blogs/architecture/field-notes-integrating-http-apis-with-aws-cloud-map-and-amazon-ecs-services/) | API Gateway HTTP API、Cloud Map、ECS/Fargateを接続し、ALB置換で費用最適化を検討した企業事例 | React SSR・Go・Caddyの組合せの実例ではない。当時の料金は現在の見積もりに使わない |

今回の調査範囲では、TanStack Start＋Go＋今回と同じAWS入口の大規模本番構成は確認できていない。公式にサポートされる接続の組合せと近い公開例はあるが、普及率・多数派は断定しない。

## 方式の比較

| 方式 | 利点 | 負担・今回の評価 |
|---|---|---|
| 専用プロキシを同一タスクへ置く | 画面・Goから振り分け責務を独立。ローカルとAWSで同じ経路規則を使える。AWSへの登録先を入口1つにできる | 管理・更新するコンテナが1つ増える。同じタスクの割当枠内ならコンテナ数による独立のFargate最低枠は増えないが、資源は消費する。容量・負荷を要検証 |
| Node.jsでGoへ転送 | 専用プロキシコンテナを省ける。入口は1つ | Cookie、複数Set-Cookie、Host/Origin、パス・クエリ、ストリーム、タイムアウト、切断の扱いを実装・検証する。Goの業務ルールを移す必要はないが、画面側に転送責務が増える |
| AWSでNode/Goへ直接振り分け | アプリ内の専用プロキシを省ける。ALBなら複数ターゲットを扱う設計が可能 | HTTP API＋Cloud Map＋1 ECSサービスという現在案に単純置換できると断言しない。標準ECSサービス発見のレジストリは1サービス1つ。二つのポートへの自動登録・更新は別設計が必要。サービス分割ならタスク最低枠等、ALBなら固定費を再見積もり。独自登録処理を安易に追加しない |

Node経由の根拠：[TanStack Server Routes](https://tanstack.com/start/latest/docs/framework/react/guide/server-routes)はHTTPハンドラーを提供するが、完成された汎用プロキシ機能の確認とは別。[Next.js rewrites](https://nextjs.org/docs/app/api-reference/config/next-config-js/rewrites)には外部転送例があるが、TanStackの同等機能として流用しない。
AWSの制約：[CreateService serviceRegistries](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_CreateService.html)、[HTTP API private integration](https://docs.aws.amazon.com/apigateway/latest/developerguide/http-api-develop-integrations-private.html)。

## 専用プロキシの製品比較と推奨案

[Nginx](https://nginx.org/en/docs/beginners_guide.html)・[Caddy](https://caddyserver.com/docs/quick-starts/reverse-proxy)とも公式にHTTP転送を扱い、今回のパス振り分けを実現できる。性能・メモリーの比較測定はしていない。

- Caddy：短い設定で表せる点が利点。現在の試作で基本転送を確認済み。ただし既存採用は選定優位の根拠にしない。自動HTTPSは現在無効で、AWS案はAPI GatewayでTLSを終端するため、今回は主要な採用理由にならない。
- Nginx：今回に近いGo＋React/Next.jsの転送例と、SSRのセルフホスト公式資料に明示された製品例がある。実務へ結び付けた学習という本人の目的に対し、これらの具体例を参照して設計・説明できる点を評価する。設定の多寡や性能・普及率の優位は今回断定しない。

**推奨は、同一Fargateタスクに専用プロキシを置き、製品はNginxを第一候補としてレビューする。** 技術的にCaddyが不適切だからではなく、業務ロジックと転送を分け、既存の公式構成例と近い説明可能な形を優先する判断。Caddyの短い設定を優先する判断も妥当。公開例があるだけで最適・安全とせず、今回の条件で検証する。

推奨の採用・置換は未実施。本人との設計確認後、実装する場合にOrigin/Cookie、SSR本文、静的ファイル、404・5xx、パスとクエリ、送信サイズ、タイムアウト、転送ヘッダー、コンテナ資源・AWS経路を確認する。入口の費用は新たに再計算していない。

LLMは一次情報の検索・読取・比較整理に使用。外部リポジトリのコード取り込みや実行、AWS作成、アプリ変更はしていない。
