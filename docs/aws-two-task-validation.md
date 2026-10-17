# 2タスク配置：成立性検証の準備

2026年10月17日。本人の「はい、進めましょう」により、NodeとGoを分離し、AWS入口から直接振り分ける案を次の検証方針として採用。容量・料金・公開認証・課金リソース作成の一括承認にはしない。

## 検証対象

```text
HTTP API → 共通VPC Link
  ├─ /api と /api/* → Go用Cloud Map → Go ECSサービス（通常1タスク）
  └─ その他 → Node用Cloud Map → Node ECSサービス（通常1タスク）
Node → api.blog.internal:8081 → Go → RDS
```

内部名は例。両ECSサービスのA/SRV登録・ヘルスチェックを設定し、Nodeは内部名でGoを取得する。API GatewayはSRV登録のIP/portをDiscoverInstancesで取得する。S3とバックアップ・外部同期は今後の機能で、今回配備済みにしない。

## 用意した具体的な設定

[HTTP APIルートのCloudFormation草案](../infra/review/http-api-routing.template.json)：API、2統合、3ルート、default stageの7リソース。既存のVPC Link ID、Node/Go用Cloud Map ARNをパラメーターとして受ける。秘密値・実アカウントIDは含まない。
これは入口部分だけのレビュー用草案。VPC・ECS・IAM・DB・ログ・独自ドメイン・疎通プローブを含まないため、単独でブログを配備できる完成テンプレートではない。AWSへ送信・適用していない。API Gatewayのルートでは認証を掛けず、管理APIはGoの認証で保護する設計。ネットワーク非公開と未認証で管理操作できないことを区別する。

パスは$request.pathで転送し、stage名が混ざることを避ける。/apiも明示して/api/*だけのマッチ漏れを避ける。新規API草案なので既存公開ルートは変更しない。AutoDeployは検証環境用で、本番の承認付きリリース方式は別に決める。

## タスク・接続設定案

| 設定 | Node | Go |
|---|---|---|
| Dockerイメージ | 現frontend/DockerfileのARM64ビルド | 現backend/DockerfileのARM64ビルド |
| 待受 | HOST=0.0.0.0, PORT=3000 | LISTEN_ADDR=0.0.0.0:8081（現在の既定loopbackから変更必須） |
| 上流 | API_BASE_URL=http://api.blog.internal:8081 | RDSのverify-full接続 |
| 秘密値 | DB秘密値・管理者ハッシュを渡さない | 起動時SSM注入。値はソースへ記入しない |
| SG受信 | VPC Link SGから3000 | VPC Link SGとNode SGから8081 |
| DB受信 | 直接接続しない | RDS SGがGo SGから5432を許可 |
| 生存確認 | NodeプロセスのHTTP応答。DB停止で再起動連鎖にしない | /api/healthz。DB疎通は/api/readyzで別監視 |

候補割当は各0.25vCPU/1GiB。容量を満たすという判定ではなく、制限を付けた試験開始値。通常各1タスク、更新時の一時2タスクとDB接続プール総数も確認する。IAMタスクロールを分離し、NodeへDB/S3バックアップ権限を渡さない。
外向き通信は各タスク公開IPv4＋SG受信制限案。インターネットから直接受信させる意味ではない。NAT/エンドポイントを採る場合は費用を置換する。

## 既存コードとの差と実AWSで必要な確認

| 確認 | 受入の証拠 | 現在地 |
|---|---|---|
| Node→Go | 内部DNSで取得・再配置後も復帰 | 現在は同居/Compose接続のみ。別タスク未実証 |
| 記事SSR | JS無効で本文・タイトル、404が取得できる | ローカル確認履歴あり。AWS未実証 |
| セッション | 正しいOriginでログイン/保存/ログアウト、Secure Cookie維持。異Origin拒否 | ローカル確認履歴あり。Gateway経由のHost/Origin/Set-Cookie未実証 |
| 公開先の正規URL | SSR生成URL・転送先・必要な転送ヘッダーが外部HTTPSと一致 | Hostは予約ヘッダーで単純overwriteを想定しない。独自ドメイン前の検証URLの扱いも確認する |
| 静的・バイナリ | JS/CSS/MIME、サイズ・文字化け、キャッシュ | Gateway経由未実証 |
| レスポンス保護 | Caddyにあったnosniff等を失わない | 現在はCaddy設定。AWS側/アプリ側の適用箇所を決めて実装する必要あり |
| DB TLS | verify-fullで接続し、誤CA/ホスト名を拒否 | 起動コードでモードを検査するが、RDS CAバンドルの供給・接続は未実証 |
| 障害・更新 | Node/Goそれぞれの切替、Cloud Map追随、旧版への復帰 | 未実証。Cloud Map登録は更新時完全無停止を保証しない |
| 制約 | HTML/APIは30秒・10MB以内。大画像は将来S3直接送信 | 実測前。ストリーミングを保証しない |
| 継続運用 | 外部疎通・失敗通知・VPC Link無通信対策・ログの秘密値非出力 | 仕組みは未実装 |

## 費用と配備前の残作業

比較記録の例では2タスク計算資源約21.22ドル/月、各IPv4計7.30ドル/月。RDS/S3/Gateway/Cloud Map/DNS/ログ/通信等は別。旧49〜53ドルは1タスク前提なので、そのまま本構成の総額として承認対象にしない。
課金前に、VPC/SG/Cloud Map/ECS/IAM/RDS/SSM/ログ等を含む設定草案、最新単価による全体見積もり、検証用の作成・削除範囲、DB保全と復旧手順を用意する。現時点ではこれら全てが完成したとはしない。必要な利用者情報・承認は具体的な配備案を示してまとめて確認する。認証キーやAWSアカウント情報の提出を今要求しない。

## 今回確認したこと

JSONの構文、CloudFormation内のRef/GetAtt参照、ルート・統合先・パス上書きを静的に照合。AWSのvalidate-template、cfn-lint、CloudFormation作成、実経路試験は未実施。アプリの機能・ローカルデータ・DNSは変更していない。

公式根拠：[Integration](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/aws-resource-apigatewayv2-integration.html)、[Route](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/aws-resource-apigatewayv2-route.html)、[パラメーター変換](https://docs.aws.amazon.com/apigateway/latest/developerguide/http-api-parameter-mapping.html)。LLMは既存コードとの照合、設定草案と検証手順の作成に使用。
