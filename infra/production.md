# 本番配置と運用

**コードとオフライン検査は準備済み。AWSへの適用、通知の受信確認、本番復元は未実施。** 進捗は[公開台帳](../docs/blog-publication-roadmap-2026-10-18.md)、仕様と理由は[現行設計](../docs/current-design.md)で管理する。

## 使用する定義

[scripts/aws/production.py](../scripts/aws/production.py)が、検証済みネットワーク・Fargate/RDS・監視の[共通定義](aws/validation-stack.template.json)を読み、本番用の認証・保存・通知・独自ホスト名を追加する。巨大なテンプレートを2本手修正する案より、共通構成の修正を両方へ反映できる。生成処理はAWS SDK・資格情報を使わず、配置しない。生成物をGitへ重複保存しない。

```sh
mkdir -p config/aws-production
python3 scripts/aws/production.py > config/aws-production/stack.template.json
python3 -m unittest discover -s scripts/aws -p 'test_*.py' -v
cfn-lint -r us-west-2 -t config/aws-production/stack.template.json
```

CIはcfn-lint 1.57.2で生成物を検査する。スキーマ検査と契約試験はIAMの実効権限、リージョン内の空き、実配備の成功を保証しない。従来の`validation.py`は検証環境だけを操作する制限を維持し、本番に転用しない。元の検証テンプレートだけでは現在のHTTPSアプリに必要なCognito/S3設定が揃わない。

本番の拡張点：

- Go実行ロールとその権限境界に、画像・バックアップの特定バケット内のGet/Put/DeleteObjectと両バケットのListBucketを追加。DeleteObjectVersion、公開ACL、IAM変更権限は与えない。NodeにS3権限を与えない。[S3 GetObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html)ではListBucketなしの不存在も403になるため、バックアップで未保存と権限拒否を区別するために必要。
- Cognito Lite、提供ログイン画面classic、TOTP必須、自己登録禁止、verified_emailによる回復。Goは登録した本人のsubだけを受け入れる。クライアントシークレットと公開用パスワードハッシュは使わない。
- API Gatewayの地域証明書と独自ホスト名。DNSレコードは自動変更せず、既存DNSを使用する。ブログ専用サブドメインを前提とし、`/blog`等のパス配下へ置く場合はルーター・リンク・Cookieを別途対応する。
- 運用失敗のJSONログからメトリクスを作り、既存の8アラームと合わせた9件を同じSNSトピックへ通知。メール購読は本人による確認が必要。API入口は10要求/秒・burst20の開始値。
- アーカイブ4世代。S3版管理で残る旧アーカイブ版は7日後に消す。参照される画像コピーは期間だけで消さない。RDSは非公開Single-AZ、7日バックアップ、削除保護、保存資源はRetain。

定義の根拠：[Cognito UserPool](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/aws-resource-cognito-userpool.html)、[UserPoolClient](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/aws-resource-cognito-userpoolclient.html)、[機能プラン](https://docs.aws.amazon.com/cognito/latest/developerguide/cognito-sign-in-feature-plans.html)、[APIドメイン](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/aws-resource-apigatewayv2-domainname.html)、[S3旧版の期限](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/aws-properties-s3-bucket-noncurrentversionexpiration.html)。

## 配置前に確定する値

公開ホスト名、月額上限、通知先、復旧時に許容する損失と時間を本人と確定する。既存AWSアクセス権は検証用の名前・SSMパス・サービスに限定されており、この本番定義をそのまま適用できる権限ではない。実環境の必要権限を一括で照合し、管理者向けの具体的な変更セットを用意する。現在の権限やDNSを勝手に広げない。

スタック名は小文字の`course-k-blog-production`、SSMパスは`/course-k/blog-production/`を使用する。RuntimeBoundaryは本番スタック内で作り、検証用境界を流用しない。DB用パスワード・DSNは権限0600のGit対象外ファイルからSSM SecureStringへ登録し、コマンド引数・ログ・PRへ値を出さない。通常DML用と移行DDL用は分ける。

## 配置・更新の順序

| 段階 | 作業と終了条件 |
|---|---|
| 1. 承認する計画 | 確定した公開URL、全資源の月額見込み、IAM差分、保持方針を確認。継続課金とDNS変更の対象を示して承認を得る |
| 2. 証明書・基盤 | オレゴンの対応PostgreSQL版とアカウントを確認。ACMの証明書を発行・DNS検証し、EnableRuntime=falseで基盤を配置。まだアプリは稼働しないが、DB等の課金は始まる |
| 3. 本人・データ準備 | 自己登録を使わずCognitoに本人だけを作る。本人所有メールを確認し、subをAdminSubjectへ設定。初期認証情報は本人が安全に保持し、パスワード変更・TOTP登録は接続後の段階6で行う。本人の秘密値をチャットへ送らない |
| 4. ビルド・DB移行 | 同じDockerfileからLinux ARM64のNode/Go/DB初期化イメージを作り、ECRへ登録してdigestを固定。新規DBのロールを初期化後、001〜006の移行を単発タスクで実行。実データがあるDBを再初期化しない |
| 5. 配置 | 公開URL、DB用SSM ARN、Cognito本人sub、digestを設定し、変更セットを確認してEnableRuntime=true。各サービス1タスクの常時稼働・ヘルスチェック・DB接続を確認 |
| 6. 接続確認 | 専用ホスト名のDNSをRegionalDomainNameへ接続。Cognitoログイン・TOTP・サインアウト・回復、非公開S3の拒否、記事/画像の公開制御、同期、バックアップ、メール通知到達を確認 |
| 7. 運用受入 | 別RDS・別画像バケットへの復元、候補版の負荷、失敗イメージからの戻し、請求/予算通知を確認。許容損失と復旧時間に合うことを確認して公開 |

初期化・移行・復元用のタスクは必要時だけ動かす。ECSの完了コードと移行結果を確認し、アプリが起動したことだけで移行成功にしない。DNSの実URL、承認済み額、適用済みdigest等はGit対象外の環境設定へ保存する。

更新は変更前バックアップを確認し、後方互換の移行を先に行い、Node/Goの変更した側だけを新digestへ更新する。ECS circuit breakerは起動に失敗した配置を戻すが、起動できる論理不具合までは判定しない。直前に稼働したdigestを保持し、公開疎通・投稿・画像・監視を検査する。不具合時は先のdigestへ戻し、DDLが非互換なら自動で巻き戻さず復元案を扱う。ECRの整理では現行・直前・復元に必要なイメージを消さない。

## 保存と回復の操作

ローカルは`postgres_data`・`media_data`・`backup_data`の3ボリュームを保持する。`docker compose down -v`は通常停止に使わない。同一端末の別ボリュームは端末故障への独立バックアップではない。本番は別の非公開S3バケットへ保存する。

管理画面に「バックアップ待ち」「前回失敗」「最終成功」「保存済みID」を表示する。変更を今取得する要求もできる。変更なしは新世代を増やさない。保存失敗を公開失敗や成功へ置き換えない。削除/期限超過/形式変更/保存量の増加は通知と状態表示で確認し、保存先の失敗・取得上限を解消して再取得する。

アプリには次の明示コマンドがある。接続値は環境・SSMから与え、引数に秘密値を入れない。

```sh
# 指定環境の変更を今取得する。変更がなければ新規作成しない。
api backup
# 移行済みの空DBと別画像保存先へ復元。IDは管理画面の保存済みID。
api restore --backup-id BACKUP_ID --confirm-empty-target
# ログインフローを含め、ローカルDBセッションを全失効。
api revoke-sessions --confirm
```

復元先DB名は`_restore`または`_test`で終わり、対象表が空、移行版がアーカイブと同じことを要求する。対象DB用のDSN・権限・画像保存先を持つ専用の単発タスクを用意し、本番タスクの接続先を書き換えて復元しない。バックアップ用S3は復元元、MEDIA_BUCKETは新しい復元先とする。記事数・本文・公開状態・画像ハッシュを照合し、匿名で下書き画像を取得できないことまで確認してから切替を判断する。

パスワード回復はCognitoの検証済み本人メールを使用する。TOTP端末を失った場合や侵害が疑われる場合は、管理者が本人を確認してCognito側を停止・回復し、CognitoセッションとDBセッションを失効させる。Cognito側だけのサインアウトでは8時間のDBセッションは消えない。別の管理ユーザーを無条件に追加して回避しない。AWSアカウント自体のMFA・回復手段も本人が保持する。

## 容量と費用の開始見積もり

Node 0.5vCPU/1GiB、Go 0.25vCPU/1GiB、RDS db.t4g.micro/20GiBは本番受入の開始値。追加機能の試験では200記事×50KiBの作業版と公開版（計19.5MiB）を別DBへ復元し、最大1200万画素のPNGを再エンコードできた。macOS上の独立試験プロセス・GOMEMLIMIT=512MiBで最大常駐メモリはそれぞれ約175MiB・149MiB。画像は透明の規則的パターン、記事は反復文字であり、圧縮率・CPU時間・並行実行・Fargate ARM64の性能を保証しない。DB JSON 64MiB/展開アーカイブ128MiBの上限や、複数作業の同時実行には別の負荷確認が必要。

2026-10-19に[AWS公式オレゴン価格表](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonECS/current/us-west-2/index.json)と各サービスの公開表を確認。月730時間、オンデマンド、ARM64、各サービス1タスク、総API要求10万、S3保存6GiB・PUT1万/GET10万、ECR2GiB、ログ取込/保存各1GiBを例にする。アクセス量・保存量は想定であり、実測の確定請求ではない。

| 項目 | 計算の前提 | 月額USD（税前） |
|---|---|---:|
| Node | (0.5×0.03238＋1×0.00356)×730 | 14.42 |
| Go | (0.25×0.03238＋1×0.00356)×730 | 8.51 |
| 公開IPv4 2本 | 2×0.005×730。ALB・NATは作らない | 7.30 |
| RDS | db.t4g.micro 0.016×730 | 11.68 |
| RDS保存 | gp3 20×0.115 | 2.30 |
| API Gateway | HTTP API 10万×0.000001 | 0.10 |
| S3 | 6×0.023＋PUT1万＋GET10万 | 0.23 |
| ECR | 2×0.10 | 0.20 |
| CloudWatch | 取込0.50＋保存0.03＋9アラーム0.90＋独自指標0.30 | 1.73 |
| Cloud Map・内部DNS | 2登録0.20＋検出10万0.10＋登録等千回0.001＋private zone0.50 | 0.80 |
| Secrets Manager | RDS管理者secret1件0.40＋API千回0.005 | 0.41 |
| Cognito | 本人1MAU、無償枠を使わない場合0.0055 | 0.01 |
| 小額処理・通信の予備 | 監視Lambda・通知・更新/単発タスク・AZ間通信等の予算枠。固定単価ではない | 1.20 |
| **例の合計** | インターネット転送30GiBをアカウントの月100GB無償枠内と仮定 | **約48.88** |

[Fargate](https://aws.amazon.com/fargate/pricing/)、[RDS](https://aws.amazon.com/rds/postgresql/pricing/)、[S3](https://aws.amazon.com/s3/pricing/)、[CloudWatch](https://aws.amazon.com/cloudwatch/pricing/)、[Cloud Map](https://aws.amazon.com/cloud-map/pricing/)、[Route 53](https://aws.amazon.com/route53/pricing/)、[Cognito](https://aws.amazon.com/cognito/pricing/)を参照。Cognitoの直接ログイン1MAUは通常10,000MAUの共通枠に収まるが、組織/アカウント全体の残枠は未確認。private hosted zoneの問い合わせはRoute 53で追加料金なし。公開DNSは既存サービスを使う前提で、新しいRoute 53公開ゾーンを必須にしない。

税・為替・ドメイン更新・有償CI、転送枠を超える通信、増えた画像/ログ/旧版/スナップショット、RDSの追加CPUクレジット等は別。通常のSSM Standardパラメーターと既存VPCを使い、Spot・予約割引・期限付きクレジットで安く見せない。概算を月額上限の保証にはしない。実公開前にアカウントの料金条件と残枠を照合し、予算通知を設定して、本人が許容した額に収まる計画にする。
