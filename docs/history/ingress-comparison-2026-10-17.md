> 位置づけ：本書は対象時点の判断・調査・実装・検証の記録です。本文中の「現在」「未着手」「次」は当時の状態を含みます。進捗と残作業の正本は[公開台帳](../blog-publication-roadmap-2026-10-18.md)、現行仕様は[設計書](../current-design.md)です。

# 入口の振り分け：類似例と比較

**現行提案は末尾「タスク分離を前提とした再評価」。それ以前の専用プロキシ優先案は、1タスク同居を前提とした検討履歴。採用未決。**

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

## 初回調査時の製品比較（後述の再評価を優先）

[Nginx](https://nginx.org/en/docs/beginners_guide.html)・[Caddy](https://caddyserver.com/docs/quick-starts/reverse-proxy)とも公式にHTTP転送を扱い、今回のパス振り分けを実現できる。性能・メモリーの比較測定はしていない。

- Caddy：短い設定で表せる点が利点。現在の試作で基本転送を確認済み。ただし既存採用は選定優位の根拠にしない。自動HTTPSは現在無効で、AWS案はAPI GatewayでTLSを終端するため、今回は主要な採用理由にならない。
- Nginx：今回に近いGo＋React/Next.jsの転送例と、SSRのセルフホスト公式資料に明示された製品例がある。実務へ結び付けた学習という本人の目的に対し、これらの具体例を参照して設計・説明できる点を評価する。設定の多寡や性能・普及率の優位は今回断定しない。

**推奨は、同一Fargateタスクに専用プロキシを置き、製品はNginxを第一候補としてレビューする。** 技術的にCaddyが不適切だからではなく、業務ロジックと転送を分け、既存の公式構成例と近い説明可能な形を優先する判断。Caddyの短い設定を優先する判断も妥当。公開例があるだけで最適・安全とせず、今回の条件で検証する。

推奨の採用・置換は未実施。本人との設計確認後、実装する場合にOrigin/Cookie、SSR本文、静的ファイル、404・5xx、パスとクエリ、送信サイズ、タイムアウト、転送ヘッダー、コンテナ資源・AWS経路を確認する。入口の費用は新たに再計算していない。

LLMは一次情報の検索・読取・比較整理に使用。外部リポジトリのコード取り込みや実行、AWS作成、アプリ変更はしていない。


## 本番の負担を優先した再評価

2026年10月17日。本人の「ローカルとの一致は重視しない」「基準を適用した技術評価を任せたい」という意向を反映。以下を現在の評価とする。実装・課金操作は行っていない。

### 評価の前提と順序

React SSR＋Go APIとECS/Fargateは維持。1タスク同居、HTTP API＋Cloud Mapは比較用の設計候補で、本人の必須要件には昇格させない。別配置・ALBも検討対象に含める。要件・保護を満たせない案は除外し、その上で運用全体の負担と費用、保守・習熟、変更容易性を評価。ローカルとの一致、既存コードの存在は主たる加点にしない。実測していない性能を数値点数で評価しない。

### 同じ目的を満たす経路の比較

| 案 | 本番の経路 | 増える管理対象・結合 | 費用の違い | 判断 |
|---|---|---|---|---|
| ALBで直接振り分け | ALB→Node/Goを別ターゲットグループへ。1タスクでも複数登録できる | リスナールール・ターゲットグループ・正常性確認。専用プロキシ不要、マネージドの登録機能を利用可能 | ALB時間料金＋LCU、公開方式ならIPv4。HTTP API案の料金を残したまま二重加算せず、入口全体を置換して比較する必要 | 正攻法の有力案。費用よりマネージド入口と標準的な更新経路を優先するなら選択。ただし低頻度の個人運用では入口固定費を受け入れる価値が現状小さいと評価 |
| HTTP API/Cloud Mapで直接二分 | APIのルートごとにNode/Goの登録先へ | ECS標準サービス発見は1サービス1レジストリ。サービス分割または別の登録ライフサイクル設計が必要 | 分割なら各タスク割当・場合によりIPv4などが変わる。同じ合計資源にできる場合もあるので必ず倍とはしない | 実現不可ではないが、プロキシ1個を省く目的だけで独自登録やサービス分割を導入しない |
| NodeでGoへ転送 | HTTP API/Cloud Map→Node→Go | 転送ハンドラー/ライブラリ選定、Cookie・ヘッダー・タイムアウト・切断検証。画面サーバーがAPIの通過点にもなる | 同じタスク枠なら独立の追加計算資源課金なし。Nodeの転送負荷を測る | コンテナ最小化を優先するなら妥当。TanStackのHTTPハンドラーがあることと、検証済み汎用転送機能があることを混同しない。現状では追加の転送実装・ライブラリを選ぶ利得が小さい |
| GoでNodeへ転送 | HTTP API/Cloud Map→Go。APIは自分で処理、それ以外をNodeへ | Go標準httputil.ReverseProxyを使用可能。業務APIがSSR・静的配信の通過点にもなり、現APIのOrigin/JSON制限を転送へ誤適用しない設計が必要 | 同じタスク枠なら独立の追加計算資源課金なし。Goの転送負荷を測る | 技術的に十分成立する。プロキシをゼロから書く必要はない。サーバー数最小化の次点案。ただし転送と業務APIを同じプロセスへ結合する |
| 専用プロキシ | HTTP API/Cloud Map→プロキシ→Node/Go | コンテナの更新・ログ・正常性確認が1つ増える。転送は製品設定で扱い、業務/画面から独立 | 同じタスク割当で収まればコンテナ追加による別のFargate最低枠・公開IPは不要。CPU/RAM、ログ・保管・転送はゼロでなく、増量すれば費用増 | 本番でも推奨。理由はローカル一致ではなく、AWSの登録先を1つに保ち、自作の経路処理を増やさず、画面と業務APIへの結合を避けるため |

専用プロキシは同居タスクの入口になるが、同居コンテナ同士が独立障害ドメインになるわけではない。Node入口案・Go入口案でも業務ルールの所有権はGoのまま保てるため、「責務分離が不可能」とは評価していない。

### NginxとCaddyの再評価

両者とも今回必要なパス転送、Cookieを含むHTTP応答、転送ヘッダー、タイムアウト等を設定できる。どちらも採用可能で、今回の機能適合に決定的な差は確認していない。

- Nginx：proxy_pass、proxy_set_header、proxy_read_timeout等で明示的に設定する。既定の応答バッファリングがあり、逐次応答を要件とする経路では設定を検討する。公式SSR配置例と近い構成例を参照でき、本人の実務学習の目的では選好の根拠になる。
- Caddy：reverse_proxyへ設定を集約でき、短い設定にできる。既定で設定される転送ヘッダー等の挙動も確認が必要。自動HTTPSは今回のAPI Gateway終端では利点に数えない。未知の製品だから安全性・保守性が低いとはしない。
- 両者とも、名前や導入だけで入力保護・レート制限・適切な転送ヘッダー信頼が完成するわけではない。今回の設定と試験で確認する。メモリー・性能の勝者、利用率、セキュリティ優位は未確認。

**最終提案：専用プロキシ方式を採り、Nginxを第一候補とする。** 方式は本番の登録・転送・保守の分担から選ぶ。製品は両方が適合した上で、本人の実務学習と具体的な構成例を参照できることを補助基準にNginxを選ぶ。Nginxが技術的に唯一の正解、Caddyより高性能・安全という主張ではない。製品差より、入口方式を決める方が今回の影響が大きい。

受け入れる負担：コンテナ1つの依存更新・ログ・ヘルスチェック、入口障害の調査、資源測定。省く負担：独自のCloud Map登録更新処理、Node/Go内への汎用転送経路の組込み。ALBの新規固定費は初期案では避ける。

### 未検証事項と判断を変える条件

1. AWS HTTP API経由でSSR本文・静的アセット・Cookie・404/5xx・正しいHost/Originを確認。30秒/10MBの制約は入口プロキシを変えても残る。要件に合わない場合はALB入口へ再評価し、差額を提示する。
2. 最終的な信頼済み転送元・ヘッダー処理、管理API非公開境界ではなく認証保護、リクエストサイズ・タイムアウト、ログの秘密値非出力を検証する。
3. タスク切替・Cloud Mapの登録反映、DB一時障害、コンテナ停止、ロールバック、VPC Linkの長期無通信対策を確認する。
4. 選定タスク枠で同時閲覧・編集・同期等を測る。プロキシを加えて枠を超える場合は、増量費用とGo入口等の代案を比較し直す。
5. 正確な円/月の差・必要メモリーは未算出。今回は公式課金方式と既存割当枠での増減を評価したもの。最新の東京単価でALBを含めた月額全体を再試算したとは扱わない。

### 今回再確認した一次情報

- [ECS CreateService](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_CreateService.html)：serviceRegistries制約。
- [ECS複数ターゲットグループ](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/register-multiple-targetgroups.html)：ALBから同じサービスの複数コンテナ/ポートへ接続する公式案。
- [HTTP API private integration](https://docs.aws.amazon.com/apigateway/latest/developerguide/http-api-develop-integrations-private.html)、[HTTP API quotas](https://docs.aws.amazon.com/apigateway/latest/developerguide/http-api-quotas.html)。
- [ALB料金](https://aws.amazon.com/elasticloadbalancing/pricing/)、[Fargate料金](https://aws.amazon.com/fargate/pricing/)：時間・LCUとタスク資源の課金構造。
- [Go ReverseProxy](https://pkg.go.dev/net/http/httputil#ReverseProxy)、[TanStack Server Routes](https://tanstack.com/start/latest/docs/framework/react/guide/server-routes)。
- [Nginx HTTP proxy](https://nginx.org/en/docs/http/ngx_http_proxy_module.html)、[Caddy reverse_proxy](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy)。

本人への確認対象は、この提案の「コンテナ1つの保守負担を受け入れ、AWSの独自登録やアプリ内転送の組込みを避ける」方針。Nginx設定の細部を本人へ逐一選ばせない。現在の試作は変更せず、採用確認後に必要な実装と検証へ進む。


## タスク分離を前提とした再評価（現行提案）

2026年10月17日。本人の疑問を受け、AWSが示す同居/分離の基準を確認。前の推奨は1タスク同居を前提にCloud Mapの登録制約を重く評価したもの。NodeとGoを別ECSサービスにすれば、それぞれ標準のCloud Mapサービスへ登録できるため、独自登録処理が必要という懸念はこの構成には当てはまらない。

### 目的に戻す

必要なのは、Reactの記事SSR・管理画面、Goによる認証/保存/同期、常時運用、無理のない費用と保守。Nginxを動かすこと、1コンテナ1タスク、サービス数を増やすこと自体は目的ではない。本人の「役割で分けたい」は、まず役割・配置の理解を確認する発言で、3タスク採用の明示決定ではない。

AWS公式は、同じライフサイクル・同居・共有資源が必要なら同一タスクを、不要なら分離を推奨。画面とAPIの異なるスケールを具体例にしている。ただし常に1タスク1コンテナという規則ではない。

### 比較する経路

A：3タスク（各1個常時）

```text
API Gateway HTTP API→VPC Link→Cloud MapのNginx登録先
 → Nginxタスク → Nodeタスク / Goタスク（各Cloud Mapで内部発見）
```

B：2タスク（各1個常時、現在の推奨候補）

```text
API Gateway HTTP API
 ├─ ANY /api と ANY /api/{proxy+} → VPC Link → GoのCloud Map登録先 → Goタスク
 └─ $default → VPC Link → NodeのCloud Map登録先 → Nodeタスク
Nodeタスク → 内部DNS → Goタスク（SSR用のデータ取得）
```

BはALBを新たに置く案とは別。Cloud Mapは接続先を管理・発見し、HTTPの取り次ぎはAPI Gatewayが担当。ECSサービスはNode用とGo用の2つで、各々にタスク定義・Cloud Map登録先を持つ。VPC Linkは同じものを共有する案。SRV＋必要なAレコード、コンテナヘルスチェック、パス転送、DNS更新時の再接続を設計する。ブラウザーから見えるURL/オリジンは1つに保つ。

| 観点 | A：独立Nginx＋Node＋Go | B：AWS入口＋Node＋Go |
|---|---|---|
| 独立更新・資源・権限 | 3サービスごとに設定 | 2サービスと入口設定。NodeとGoを独立にできる |
| 入口の仕事 | API GatewayとNginxの2段で管理 | パス分岐をAPI Gatewayへ集約 |
| 保守 | Nginxイメージ・設定・ログ・DNS再解決・正常性を追加管理 | Nginxの保守なし。代わりに2つのAWS統合先とルートを管理 |
| 障害 | Nginx停止で両経路に影響。単一Nginxは高可用ではない | 中継用の独自単一タスクを省ける。各アプリ1個なのでアプリ側の高可用性はない |
| 適する追加条件 | Nginx固有の変換・キャッシュ・通信制御が必要 | 今回のパス転送で足り、HTTP APIの制約内で動く |

### 費用の比較例（必要容量の判定ではない）

公式東京料金表を2026年10月17日に再取得。publicationDateは2026-09-11T12:44:25Z。Linux ARMオンデマンド、730時間/月、CPU 0.04045 USD/vCPU時・RAM 0.00442 USD/GiB時。税・為替・割引なし。

NodeとGoの割当を両案で同一にして差分比較。計算例として各0.25vCPU/1GiB、Nginxは独立タスク最小の0.25vCPU/0.5GiBと置く。これでブログ運用に十分と証明した値ではない。実測で必要枠を決める。

| 項目 | A：3タスク | B：2タスク |
|---|---|---|
| Node＋Goの計算資源 | 約21.22ドル/月 | 約21.22ドル/月 |
| Nginxタスクの計算資源 | 約9.00ドル/月追加 | なし |
| 各タスクに公開IPv4を1つ付ける外向き通信案 | 3個＝10.95ドル/月 | 2個＝7.30ドル/月 |
| 上記小計（サービス全体の料金ではない） | 約41.16ドル/月 | 約28.52ドル/月 |

差は約12.65ドル/月に、追加の登録・DNS・ログ等の差が加わる。Nginxをプライベートにする場合はIPv4分が変わるが、イメージ取得等のためのNAT/エンドポイント等を代わりに計上する必要があり、無料の外向き経路とはしない。公開IPv4があってもSGで直接受信を禁止し、内部通信には私有IPを使う。
両案共通のAPI Gateway/VPC Link/Cloud Map、RDS、S3、DNS、ログ、通信、更新時一時増加等は小計に含まない。過去の全体49〜53ドル試算は1タスク前提であり、そのまま新構成の合計に使わない。ALB案は時間/LCU/IPv4課金を含む別の入口比較で、この2案比較に混ぜない。

### 推奨と理由

**NodeとGoを別ECSサービス/タスクとし、HTTP API＋VPC Link＋各Cloud Map登録先で直接振り分けるBを、次に検証する構成として推奨する。独立Nginxタスクは初期には置かない。**
理由：NodeとGoの運用単位を分ける目的を満たしつつ、Nginxを独立させた場合の常駐費・依存更新・障害点を省ける。現在必要なのは/apiとその他の振り分けであり、Nginx固有の機能要求は未確認。AWSの標準機能の組合せで実現する設計で、公開例があるだけで普及率や本番適合を保証しない。

専用プロキシ推奨からの変更理由は「本人が3タスクを好んだから」ではなく、タスクを分離する前提では、1サービス1レジストリの制約が回避され、AWS側の直接転送が比較上有利になるため。今までの1タスク同居は低コスト集約案として履歴に残すが、標準の必須配置とは扱わない。

未検証：Node/Goの必要CPU/RAM、外部Host/Origin、Cookie、静的配信、本文HTML、30秒/10MB、Cloud Map更新・障害・ロールバック、DB接続/バックアップ同時負荷。GoはNodeとVPC Linkからの必要通信を許可するが、認証APIを無認証で操作可能にしない。Nginxに置いていたセキュリティレスポンスヘッダーはAWSまたはアプリへ移す設計が必要。VPC Link長期無通信対策も残る。
HTTP APIの制約が要件に合わない場合は、まずALB直接転送へ再評価する。Nginxを追加してもHTTP APIの制約は消えない。コード・AWS設定・課金は変更していない。判断は設計案として提示し、今回の調査だけで配備に進まない。

一次情報：

- [AWS同居/分離の基準](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/container-considerations.html)、[画面とAPIの分割・権限](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/application_architecture.html)
- [HTTP APIのルート](https://docs.aws.amazon.com/apigateway/latest/developerguide/http-api-develop-routes.html)、[Cloud Map私有統合](https://docs.aws.amazon.com/apigateway/latest/developerguide/http-api-develop-integrations-private.html)、[ECSサービス発見](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service-discovery.html)
- [東京Fargate公式料金JSON](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonECS/current/ap-northeast-1/index.json)、[Fargate割当枠・料金](https://aws.amazon.com/fargate/pricing/)、[公開IPv4料金](https://aws.amazon.com/vpc/pricing/)
