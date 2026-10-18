# 後日の構想：公開ページの静的配信とGo APIのLambda化

整理日：2026-10-19。

**状態：後日検討する構想。今回の公開に向けた実装・AWS設定の変更は行わない。** 現行のFargate構成を置き換える決定や、移行実施の承認ではない。現在の設計・配置手順は[現行設計](current-design.md)と[本番配置と運用](../infra/production.md)を参照する。この構想を初回公開の必須作業に加えない。

## 目的と判断の軸

記事の閲覧は速く保ち、コメント投稿や記事の保存・更新では多少の待ち時間を許容する、という本人の意向を前提にする。サイトを常に閲覧できることと、自分のNode.js・Goを常に動かすことを分けて考える。

公開HTMLを更新時に生成して配信すれば、閲覧時にGoやDBの起動を待つ必要がない。React／TypeScript・TanStack StartとGo、AWS、MySQLを継続し、機能を削るための構成変更にはしない。コメントは将来の拡張であり、実装済み・初回必須ではない。

| 比較案 | 評価 |
|---|---|
| 現行のFargateによる常時SSR | 現在の実装・配置定義を使いやすく、閲覧ごとに最新データを取得できる。アイドル時もアプリと公開IPv4の料金が発生する |
| 静的配信＋Lambda＋稼働継続するRDS MySQL | 本構想の推奨候補。閲覧の速さを保ち、低頻度の操作を必要時だけ実行できる。公開ファイルの生成・反映と処理分割の設計が必要 |
| 静的配信＋Lambda＋自動停止するAurora | DBの稼働費も減らせる可能性があるが、DB再開待ち・接続再試行への対応が増える。今回の構想では優先しない |

LambdaのコールドスタートとDBの再開は別である。AWSはLambdaの起動時間を100ms未満から1秒超まで変動すると説明しているが、通信・DB接続・保存を含む本アプリの応答時間ではない。低頻度の利用では起動待ちに遭遇しやすく、1秒以内の完了を保証しない。Auroraの自動停止からの再開は通常約15秒、24時間超の停止後には30秒以上かかる場合がある。RDS MySQLの稼働を続ければ、このDB再開待ちを避けられる。

出典：[Lambda実行環境](https://docs.aws.amazon.com/lambda/latest/dg/lambda-runtime-environment.html)、[Aurora自動停止・再開](https://docs.aws.amazon.com/AmazonRDS/latest/AuroraUserGuide/aurora-serverless-v2-auto-pause.html)。

## 構成候補

| 役割 | 候補となる配置 | 動かすタイミング |
|---|---|---|
| 公開HTML・画像・画面遷移用データ | 非公開S3＋CloudFrontのアクセス制御付き配信 | 作成済みファイルを常時配信 |
| 管理画面のJavaScript・CSS | S3＋CloudFront | 画面を開いたとき。本人認証はAPI側でも必須 |
| ログイン・外部通信・API入口 | API Gateway HTTP API＋VPCに接続しないGo Lambda | 管理操作・取得処理が必要なとき |
| DB操作・S3保存 | VPC内のGo Lambda | 上記Lambda等から、IAMで限定した呼び出しを受けたとき |
| 本文・下書き・公開版・セッション等 | 非公開のRDS MySQL、Single-AZ、db.t4g.micro、gp3 20GB | 稼働継続。現在の開始容量を引き継ぐ候補 |
| 外部記事の同期 | 定期実行＋Go Lambda | 設定した間隔。取得結果をDB操作側へ渡す |
| 公開HTML・データの生成と配置 | CodeBuildを候補とする | 公開・更新・同期で公開内容が変化したとき |
| バックアップ | Go Lambda＋非公開S3 | 変更に応じて取得。毎日の自前フルダンプへ戻さない |

TanStack Startの公式プリレンダリングと静的Server Functionsを調査した。記事本文のHTMLだけでなく、一覧・ページ分割・Reactによる画面遷移で必要なデータも生成する。閲覧ごとのNode.js常駐は不要にできるが、現在のバージョン・コードでの静的配信は未検証。CodeBuildは更新時にNode.jsで生成する作業環境の候補であり、採用確定ではない。毎回の生成範囲と所要時間を確認し、必要なら生成専用Lambdaとも比較する。

### ネットワーク費用を増やさないための分割

現在のGoはDB接続、CognitoのOIDC通信、外部媒体の取得を同じプロセスで行う。そのままVPC内のLambdaへ移すと、外部通信にNAT等が必要になる可能性がある。公開サブネットへ接続するだけではLambdaに公開IPv4やインターネット接続は付かない。

この候補では、外部通信をVPC外のLambdaに置き、DB操作はAWSのLambda呼び出しAPIを介してVPC内のLambdaへ依頼する。DBを公開しない。VPC内からS3へは追加料金のないゲートウェイエンドポイントを使う。公開用データをS3へ出力し、VPC外の生成処理が読む形も候補にする。NAT Gateway・ALB・自前の公開IPv4・Cloud Map・VPC Linkはこの候補の必須資材にしない。

認証済みの操作だけをDB側が受け入れる契約、IAM、資格情報の取得方法、通知・監視を含む全通信経路は未設計。VPC内でSecrets Manager・SSM・CloudFront等のAPIを直接呼ぶ設計にすると、別の出口が必要になる場合がある。生成の起動や配信キャッシュの更新はVPC外へ置くなど、経路全体を確認してから費用を確定する。Lambda標準のログ出力と、アプリがAWS APIを直接呼ぶ通信も区別する。

DB接続は同時実行数と接続プールを制限し、開始時点ではRDS Proxyを必須にしない。AWSは本番でProxyを推奨しているため、直接接続を選ぶなら、低い同時実行上限と接続数の検証を理由として明記する。

## 月額の比較用試算

2026-10-19時点、オレゴン、730時間、オンデマンド、税別。使用量・平均時間・メモリは試算の仮定であり、必要容量の検証結果ではない。RDSの単価は公式リージョン価格表で再確認した。

| 項目 | 計算の前提 | 月額USD |
|---|---|---:|
| RDS MySQL | db.t4g.micro：0.016×730時間 | 11.68 |
| DB保存領域 | gp3：20GB×0.115 | 2.30 |
| Lambda | ARM、全関数合計20万回、512MiB、平均課金時間1秒。0.5×200,000×0.0000133334＋要求料0.04。Lambda間の呼び出しも実行数に含める | 約1.37 |
| API Gateway | 512KB以下の要求10万回、100万回あたり1ドル | 約0.10 |
| S3 | 合計6GB×0.023＋PUT1万回×0.005/千回＋GET10万回×0.0004/千回 | 約0.23 |
| HTML生成 | CodeBuild general1.small、月20回×5分×0.005ドル/分 | 約0.50 |
| CloudWatch | ログ取込1GB 0.50＋保存1GB 0.03＋通常アラーム7件0.70＋独自指標1件0.30。具体的な監視対象は再設計 | 約1.53 |
| Secrets Manager | secret 1件0.40＋取得千回0.005 | 約0.41 |
| Cognito | 本人1MAU、無償枠を差し引かず0.0055を丸める | 約0.01 |
| CloudFront | 月30GB・10万要求。従量課金の継続提供される無償枠内と仮定 | 0 |
| **小計** | **通知・定期実行・その他の少量通信等は含まない** | **約18.13** |

Lambda・CodeBuildの無償枠、期限付きクレジット、予約割引は差し引いていない。CloudFrontは月1TB・1,000万要求の無償枠を利用する仮定で、アカウント全体の残枠は未確認。キャッシュ無効化も月1,000パス以内を仮定し、超過分は追加する。HTML生成の回数には外部記事の変化に伴う生成も含め、毎回生成する設計ならその回数に再計算する。

現行の本番手順の例は約39.03ドル/月で、単純差額は約20.90ドル。ただし、同等の機能を実環境で受け入れた比較ではない。Lambdaの平均課金時間が同じメモリで5秒なら約5.33ドル増える。税・為替・追加CPUクレジット・DBの追加バックアップやスナップショット・保存世代増加・転送量増加・ビルド時間増加は別。小計を最終請求額や予算上限の保証にしない。

## 現行コードから必要になる変更

1. **公開画面の事前生成**：現在の`frontend/src/lib/posts.ts`は閲覧時にGoから記事を取得し、`frontend/runtime.mjs`はレスポンスにno-storeを付ける。HTML・一覧・遷移用データの生成と、静的URLの解決へ変更する。既存の`/?page=2`等をS3の同じファイルへ誤って解決しないよう、ページ分割URLと互換性を設計する。
2. **保存と公開反映の分離**：保存完了とサイト反映完了を区別する。DBの公開版から生成し、下書きを生成元へ混ぜない。生成失敗時の再実行、複数更新の順序、古い生成結果による上書き防止、記事・一覧・画像の整合性が必要。
3. **非公開化と画像の制御**：現在は画像取得時にもGoが公開可否を照合する。公開画像の直接配信と下書き画像の認可を分ける。非公開化時は公開HTML・画像の削除とキャッシュ失効を扱い、反映時間を明示する。ブラウザへ既に配信したコピーを回収できるとは説明しない。
4. **画像アップロード**：現在の最大5MiB画像はJSONでbase64化するとLambda同期要求の6MB上限を超え得る。非公開S3へ署名付きで直接アップロードし、Goが内容を検査・再エンコードして登録する方式等を設計する。未検査ファイルを公開しない。
5. **Goの実行入口と責務**：HTTP常駐を前提とする起動処理をLambda用に整え、外部通信とDB操作を分離する。既存の認証・セッション・競合防止・保存契約を維持する。
6. **同期・バックアップの起動**：現在の常駐ループを、定期実行または変更に応じた実行へ変更する。Lambdaの応答後にバックグラウンド処理が継続する前提にしない。再試行しても重複・欠落しない契約を維持する。
7. **基盤・運用の更新**：IaC、IAM、秘密値の受け渡し、DNS・CloudFront用証明書、監視、配備・復元手順を候補に合わせて変更する。現行の権限や証明書をそのまま流用できるとは扱わない。

コメントを将来実装する場合は、記事本文を先に表示し、コメントだけをAPIから取得する案を検討する。コメントの取得・投稿を記事本文の表示待ちに直結させない。送信中・失敗・成功を区別し、保存確認前に成功と表示しない。

## 再開時の到達点

| 順序 | 到達点 | 判断できる状態 |
|---|---|---|
| 1 | 通信と責務の設計 | ログイン・画像・外部同期・秘密値・監視を含め、全経路と必要権限、NAT不要の条件を説明できる |
| 2 | 静的配信の小さな検証 | 直接アクセス・Reactの画面遷移・ページ分割・404・本文HTML・画像・下書き非公開を確認できる |
| 3 | 保存から公開反映の検証 | 保存・更新・非公開化、生成失敗と再試行、並行更新を確認できる |
| 4 | 容量と費用の見積もり直し | Lambdaのメモリ・コールド/ウォーム応答、生成時間、DB接続数、通常月の全費用を示せる |
| 5 | 採用と移行計画の判断 | 費用削減と運用の複雑さを比較し、移行・ロールバック・復元を具体化してから現行設計へ反映する |

この到達点は後日の検討順序であり、今回実施済みの作業や初回公開の残件数を意味しない。

## 主な一次情報

- [TanStack Startのプリレンダリング](https://tanstack.com/start/latest/docs/framework/react/guide/static-prerendering)、[静的Server Functions](https://tanstack.com/start/latest/docs/framework/react/guide/static-server-functions)
- [CloudFrontとS3による配信](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/GettingStarted.SimpleDistribution.html)、[CloudFront従量課金](https://aws.amazon.com/cloudfront/pricing/pay-as-you-go/)
- [VPC接続したLambdaの通信](https://docs.aws.amazon.com/lambda/latest/dg/configuration-vpc.html)、[S3ゲートウェイエンドポイント](https://docs.aws.amazon.com/vpc/latest/privatelink/vpc-endpoints-s3.html)、[LambdaからのRDS接続](https://docs.aws.amazon.com/lambda/latest/dg/services-rds.html)
- [Lambdaの制限](https://docs.aws.amazon.com/lambda/latest/dg/gettingstarted-limits.html)、[同期呼び出し](https://docs.aws.amazon.com/lambda/latest/dg/invocation-sync.html)、[S3からのイベント起動](https://docs.aws.amazon.com/lambda/latest/dg/with-s3.html)
- [RDSオレゴン公式価格表](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonRDS/current/us-west-2/index.json)、[Lambda料金](https://aws.amazon.com/lambda/pricing/)、[API Gateway料金](https://aws.amazon.com/api-gateway/pricing/)、[CodeBuild料金](https://aws.amazon.com/codebuild/pricing/)
- [S3料金](https://aws.amazon.com/s3/pricing/)、[CloudWatch料金](https://aws.amazon.com/cloudwatch/pricing/)、[Secrets Manager料金](https://aws.amazon.com/secrets-manager/pricing/)、[Cognito料金](https://aws.amazon.com/cognito/pricing/)
