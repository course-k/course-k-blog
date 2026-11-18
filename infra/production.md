# 本番配置と運用

**本番はCloudFront・Lambda・自動停止するAurora MySQLで稼働している（2026-11-16に切替、旧Fargate構成は同日撤去）。** 構成と選定理由は[現行設計](../docs/current-design.md#5-採用構成と選定理由)、進捗は[公開台帳](../docs/blog-publication-roadmap-2026-10-18.md)、mainからの反映は[本番への反映](../docs/production-release.md)を参照する。

## 使用する定義

本番スタック`course-k-blog-production`へ当てる定義は[`scripts/aws/retire.py`](../scripts/aws/retire.py)の`current_template()`。旧検証の共通定義を読む[`production.py`](../scripts/aws/production.py)・[`recovery.py`](../scripts/aws/recovery.py)・[`backfill.py`](../scripts/aws/backfill.py)の出力に[新構成](../scripts/aws/ondemand.py)を足し、Fargateのサービスだけが使っていた資材を外す（[旧構成の撤去](#旧構成の撤去)）。

```sh
python3 scripts/aws/retire.py > config/aws-production/stack.template.json
python3 -m unittest discover -s scripts/aws -p 'test_*.py' -v
cfn-lint config/aws-production/stack.template.json --regions us-west-2
```

CIはcfn-lint 1.57.2で生成物を検査する。スキーマ検査と契約試験はIAMの実効権限や実配備の成功を保証しない。資材の追加・削除・権限の変更は管理者ログインで行い、変更セットの中身（追加・変更・削除・置き換え）を確かめてから実行する。

## 本番の構成と運用条件

### 採用した構成

公開先は`https://blog.course-k.com`、リージョンはus-west-2。XServer DomainのCNAME`blog`を`d3t8lafmacar3v.cloudfront.net`へ向け、CloudFrontにus-east-1のACM証明書を付ける。

- 入口：CloudFront（PriceClass_200、HTTP/2・3、IPv6）。公開ページは1日キャッシュ、`/api/*`と`/data/*`はキャッシュしない、`/api/images/*`は公開用S3を先に見て無ければGoのLambdaへ。
- 処理：NodeのLambda（`course-k-blog-production-web`、VPC外）、GoのLambda（`course-k-blog-production-api`、VPC内）、取得の中継Lambda（`-relay`、VPC外・権限なし）。関数URLは署名つきで、このCloudFrontからだけ呼べる。
- DB：Aurora MySQL Serverless v2、最小0・最大1 ACU、無操作30分で停止、削除保護、7日のバックアップ保持。非公開で、GoのLambdaと単発タスクのセキュリティグループからだけ届く。
- 保存：非公開S3 3バケット（画像、バックアップ、読者向けファイル）。読者向けファイルのバケットはこのCloudFrontだけが読める。
- 単発タスク：ECSクラスタ（常駐サービスなし）で、Aurora向けの初期化・移行・復元を必要時だけ動かす。
- ネットワーク：既存VPCにデュアルスタックのサブネット2つ、Egress-Only Internet Gateway、S3のゲートウェイエンドポイント。NAT Gateway・公開IPv4の常駐は無い。
- 認証：Cognito Lite、提供ログイン画面classic、TOTP必須、自己登録禁止、verified_emailによる回復。Goは登録した本人のsubだけを受け入れる。
- 定期処理：1日1回（00:10 UTC）にGoへ未処理の作業を実行させる。15分ごとの疎通監視は公開ページと`/healthz`だけを見る。
- 通知：運用失敗のJSONログと疎通監視のアラームを同じSNSトピックからメールで通知する。

### 本番権限の分離

#### GitHubからのアプリ更新と専用アクセス

`python3 scripts/aws/delivery_access.py`で専用のアクセス定義を生成する。これはAWSを呼び出さず、本番更新も開始しない。管理者が別スタック`course-k-blog-delivery-access`として確認・登録した。2026-10-30に既存IDプロバイダ0件を確認したうえでGitHub用OIDCプロバイダと専用ロールを作成し、両資源とスタックのCREATE_COMPLETEを確認済み。後日の変更は同じスタックの更新として扱う。他のスタックを新設してこのプロバイダを重複作成しない。

接続元は`course-k/course-k-blog`の`main`だけで、GitHubの通常のsubject形式と所有者・リポジトリID付き形式を完全一致で許可する。PR、別ブランチ、Environmentを使うsubjectは許可しない。GitHub Actionsで`id-token: write`を宣言し、`course-k-blog-github-deploy`を利用する。長期アクセスキーや管理者権限をGitHubへ渡さない。

権限はオレゴンの本番ブログ用ECR（node/go）へのイメージ登録、既存本番スタックの変更セット、CloudFormation経由の2つの関数（`course-k-blog-production-api`・`-web`）のイメージ更新とAuroraへの一時タスク（移行・復元）の定義更新、更新前の未処理作業の呼び出し（APIの関数だけ）、CloudFrontのキャッシュ無効化に絞る。イメージ更新で再評価される関数URL・定期ルール・配信の照会、タスク定義・ロールの照会は、CloudFormationを経由する場合だけ許可する。DescribeTaskDefinitionとDeregisterTaskDefinitionはARNで限定できないため、この経路に限ってResource=*を使う。渡せる実行ロールはAurora向け一時タスクの4種で、関数の設定・権限・配信設定の変更、自分の権限変更、IAMユーザー作成、RDS/DNS/ネットワークの変更、S3や秘密値の直接読み取りは許可しない。ただし、配備されるアプリは実行時の権限で記事・画像・バックアップへアクセスできる。配備権限が記事データから完全に隔離される意味ではない。

[検証ワークフロー](../.github/workflows/checks.yml)の`Deploy production`が、mainへのpushまたはmainを指定した手動実行で、4つの検査ジョブがすべて成功した後に実行される。PRでは配備しない。新しいmainに置き換わった版と、文書だけのpushは配備しない。実行中の本番更新を新しいpushでキャンセルせず、配備を直列化する。GitHub環境変数のアカウント番号は接続先識別子であり、秘密鍵ではない。公式AWS認証アクションv6.3.0をコミットで固定し、OIDCで取得する一時権限を配備ジョブだけに与える。

ローカルAWS CLIは、業務用の`~/.aws`と分離した設定ファイル・credentialsファイル・ログインキャッシュを明示して使う。親シェルのAWS環境変数を専用プロセスへ引き継がず、コンテナ認証やメタデータへのフォールバックも無効にする。操作前にSTSのアカウント番号を照合して、個人ブログのアカウント以外では停止する。管理者ログインは初回アクセス補正に限定し、作業後に専用CLIセッションをログアウトする。通常の更新はmainのGitHub Actionsで行い、ブラウザやCloudShellの画面操作を運用の前提にしない。

[`delivery.py`](../scripts/aws/delivery.py)は以下の手順を実行する。`--apply`がない場合は読み取りと前提確認までで、ビルド・バックアップ・更新は開始しない。

1. AWSアカウントと専用ロール、テスト対象とcheckoutのコミット、ECSサービスが無いこと、2つの関数が停止中でなく直前の更新を終え、スタックのイメージで動いていることを照合する。CloudFormationサービスロールが付いた場合は停止する。DB移行SQLと移行処理のハッシュをSSMの`/course-k/blog-production/applied-schema-digest`と照合し、未登録・不一致なら停止する。通常配備からDDLは実行しない。
2. GitHubのARM64ランナーでNode/Goをビルドし、専用ECRへ登録してdigestで固定する。Docker認証情報は一時ディレクトリに置いて終了時に削除し、保存・表示しない。
3. APIの関数を`{"work":"pending"}`で呼び出し、未処理の作業（期限の来たバックアップを含む）を実行する。成功した場合だけ更新を続ける。変更が無ければ新しいバックアップは作らない。
4. 現行CloudFormationテンプレートと他のパラメータを維持し、OnDemandGoImage/OnDemandNodeImageだけの変更セットを作る。2つの関数のイメージ、一時タスク2つの定義の置き換え、関数や配信を参照する資材の再評価（値は変わらない）以外の変更は拒否してから実行する。
5. 更新したパラメータでスタックが完了したこと、2つの関数が新しいイメージで更新を終えたことを待って確かめ、CloudFrontのキャッシュを全体で無効化する（読者のページは1日キャッシュされ、古いページは新しいビルドに無い資材を参照するため）。その後、公開HTTPSのhealth・readiness・一覧を確認する。DBが停止中なら再開を待つ。確認で失敗した場合は、元の2イメージのdigestへ戻して確認し、候補の配備自体は失敗として扱う。

Fargate時代の単発タスク`release-check`（本番S3への直接送信の確認）は、ECSのアプリ定義が無くなったため配備から外した。スキーマを変える配備は[`special-release.yml`](../.github/workflows/special-release.yml)で行う（[本番への反映](../docs/production-release.md#dbのスキーマを変える更新)）。

#### DBのスキーマを変える配備

手順は[本番への反映](../docs/production-release.md#dbのスキーマを変える更新)を参照。基盤の定義の変更はこのワークフローでは行わない。

### 管理者が適用する本番用アクセス設定

[scripts/aws/production_access.py](../scripts/aws/production_access.py)で管理者用のアクセス定義を生成する。これはAWS APIを呼ばず、既存の検証用定義も上書きしない。

```sh
python3 scripts/aws/production_access.py > config/aws-production/access.template.json
```

管理者はオレゴンの別スタック`course-k-blog-production-access`として内容と名前付きIAM資源を確認して適用する。作成するのはIAMの操作者ロール1件・管理ポリシー5件（うち1件は実行ロールの上限）・既存course-k-deployへのAssumeRoleポリシー1件で、DBや常駐アプリの課金は開始しない。既存の検証用ロール・ポリシーを置き換えない。検証環境用の保存済みオブジェクト版・スナップショット等の直接削除権限は引き継がず、本番データの撤去は管理者の別操作とする。操作者には自分のロールやこの上限ポリシーを変更する権限を与えない。出力のRuntimeBoundaryArnは操作者から読み取れるため、本人へARNのコピーを繰り返し依頼する運用にはしない。

適用後は専用の`course-k-production-operator`へAssumeRoleし、アカウント・ロールを照合して、本番スタックを再確認する。Cognitoの本人管理はProject=course-k-blog-productionタグのプールに限定し、本人1人だけを登録する。CloudFormation経由の作成/更新を広い資源指定で許可する箇所は残るため、万能な最小権限やアカウント全体の隔離を保証する設定とは説明しない。

ポリシー検査・構文検査の成功は実効権限や配置成功を保証しない。生成物と現行ソースを照合し、管理者による権限変更の承認後に適用する。

### 回復目標の扱い

アプリの変更時バックアップ4世代とAuroraの自動バックアップ7日を維持する。公開反映後は速やかに取得し、下書き等はUTCの日付境界でまとめる。障害通知を受けて手順に沿い手動で復元する方針を本人が受入済みで、復旧時間の保証や数値目標は設けていない。許容損失は取得周期だけで決めず、最後に成功したアーカイブ世代とAuroraの復元可能時点を示して判断する。旧RDSの最終スナップショット（`course-k-blog-production-snapshot-database-pts5zduc7iua`、2026-11-16）は残している。

スタック名は小文字の`course-k-blog-production`、SSMパスは`/course-k/blog-production/`を使用する。RuntimeBoundaryは管理者用の別スタック`course-k-blog-production-access`で作り、その出力ARNを本番定義へ渡す。操作者へ境界の作成・更新権限を与えず、検証用境界も流用しない。DB用の接続文字列は権限0600のGit対象外ファイルからSSM SecureStringへ登録し、コマンド引数・ログ・PRへ値を出さない。通常DML用と移行DDL用は分ける。

## 新構成の定義とパラメータ

[現行設計の構成](../docs/current-design.md#awsの入口と配置)の資材は、本番定義（`scripts/aws/production.py`）へ[`scripts/aws/ondemand.py`](../scripts/aws/ondemand.py)が足す。2026-11-16に適用し、同日にブログのホスト名を割り当てた。各パラメータで作られるもの：

| パラメータ | 設定すると作られるもの |
|---|---|
| `EnableOnDemand=true` | 新しいデュアルスタックのサブネットとIPv6の出口、S3のゲートウェイエンドポイント、Auroraのクラスタとインスタンス、読者向けファイルのバケット、Aurora向けの初期化タスク |
| `OnDemandGoImage`・`OnDemandNodeImage` | 新構成が使うイメージ（ダイジェスト指定）。下の3行の資材は、対応するイメージも設定されているときだけ作られる |
| `OnDemandMigrationUrlParameterArn` | Aurora向けの移行タスク |
| `OnDemandDatabaseUrlParameterArn` | GoとNodeのLambda、取得の中継、CloudFront、1日1回の定期呼び出し、CloudFront向けの疎通監視（公開ページと`/healthz`だけを見る。DBへ接続する`/api/readyz`は停止中のDBを15分ごとに起こすため対象にしない） |
| 上の2つの両方 | Auroraへバックアップを復元する一時タスク |
| `OnDemandOrigin`・`OnDemandDistributionId` | 新構成が自分のホスト名で応答するための設定（GoのOrigin照合、キャッシュ無効化の権限、Cognitoのコールバック、画像バケットのCORS）。CloudFrontの作成後に、その出力を渡して更新する |
| `OnDemandDataOrigin` | Nodeが読者向けデータを読む先。CloudFront自身のホスト名（`https://….cloudfront.net`）を設定する。ブログのホスト名はDNSに従うため、切替の前後は旧構成を指し得る |
| `OnDemandCertificateArn` | 切替時に設定する。ブログのホスト名をCloudFrontへ割り当てる |

適用の順序は、権限上限の更新（[`scripts/aws/production_access.py`](../scripts/aws/production_access.py)。Lambdaのネットワークインターフェース、自分自身と中継の呼び出し、キャッシュ無効化、Auroraの管理者シークレットの読み取りを足した）→ `EnableOnDemand=true` → Auroraの初期化と2つの接続文字列の登録（`/course-k/blog-production/`の下）→ 残りのパラメータ、の順。新しい資材の種類を作る権限は操作者ロールにないため、スタックの更新は管理者ログインで行う。

Auroraへの復元は`<Goイメージ> restore --backup-id <ID> --confirm-empty-target --new-database-host <Auroraのホスト名>`で行う。復元先は本来`_restore`か`_test`で終わる専用DBに限るが、新しいDBサーバーへ移すときは実際のDB（`blog`）へ入れる必要があるため、接続先のホスト名を一致させたときだけ許可する。復元先が空でなければ、復元の処理自体が拒否する。復元はアプリ用の利用者で行い、タスクのロールはバックアップの読み取りと画像バケットへの追加だけを持つ。受入の試験記事が残ったAuroraへ切替日に復元し直すときは、同じタスクで先に`<Goイメージ> clear-restore-target --confirm-delete-content --new-database-host <Auroraのホスト名>`を実行する。記事・画像の記録・外部記事・作成要求・未完了の画像送信・セッション・ログインの途中状態を一つのトランザクションで消す。許可の条件は復元と同じで、画像の実体とバックアップの記録・実体は消さない。

新構成はイメージ用パラメータ（`OnDemandGoImage`・`OnDemandNodeImage`）を持ち、出力（Outputs）を足さない。切替後の通常の配備はこの2つを変える（[手順](#githubからのアプリ更新と専用アクセス)）。資材の名前やアドレスは、スタックの資材一覧から読む。

権限上限は2026-11-16に適用した。適用済みのアクセス用スタックには、リポジトリの生成処理にない資材（MacからのRoles Anywhere接続用のTrustAnchorとProfile、それに合わせたOperatorRole）がある。生成した定義をそのまま適用するとこれらが削除されるため、適用済みの定義へ権限上限の文だけを足して更新した。アクセス用スタックを更新するときは、生成した定義と適用済みの定義の差を先に確かめる。

生成した定義は51,200バイトを超えるので、S3経由（TemplateURL）で渡す。定義の不変条件は`scripts/aws/test_ondemand.py`で確認する：既定では何も作らない、既存の資材への変更は受入用ホスト名の2か所だけ、関数URLは署名つきでこのCloudFrontからだけ呼べる、読者向けバケットはこのCloudFrontだけが読める、DBは非公開でAPIと一時タスクだけが届く、実行ロールの権限は権限上限に収まる。

## 旧構成の撤去

切替（2026-11-16）の後、本番スタックへ当てる定義は[`scripts/aws/retire.py`](../scripts/aws/retire.py)の`current_template()`とする。`backfill_template()`の出力から、Fargateのサービスだけが使っていた資材を外す：ECSサービスとタスク定義、API Gateway・VPC Link・Cloud Map、MySQLのRDSインスタンスとそのサブネット・セキュリティグループ・パラメータグループ、旧構成の疎通監視、それらの上に作った一時操作（別RDSへの復元、起動失敗版からの戻し、外部記事の補完）。残す資材の定義は適用済みのものと同じで、変更は削除だけになる。不変条件は`scripts/aws/test_retire.py`で確認する。

2026-11-16に、`EnableRuntime=false`でサービスを止める → RDSインスタンスの削除保護を外す → `current_template()`の変更セット（削除28件だけ）を確かめて実行、の順で適用した。RDSは削除方針（Snapshot）により最終スナップショットを残す。画像・バックアップのバケット、Cognito、ECRは残る。保持方針（Retain）のロググループとリポジトリ（RouterRepository等）は定義に残す。

一時タスク（Auroraの初期化・移行・復元）は、新構成のサブネット（`OnDemandSubnet1`・`OnDemandSubnet2`）で、セキュリティグループ`ApplicationSg`と公開IPv4を付けて実行する。このサブネットはIPv4をインターネットゲートウェイへ向けているため、ECRとSSMへ届く。

## 保存と回復の操作

ローカルは`mysql_data`・`media_data`・`backup_data`の3ボリュームを保持する。`docker compose down -v`は通常停止に使わない。同一端末の別ボリュームは端末故障への独立バックアップではない。本番は別の非公開S3バケットへ保存する。

管理画面に「バックアップ待ち」「前回失敗」「最終成功」「保存済みID」を表示する。変更を今取得する要求もできる。変更なしは新世代を増やさない。保存失敗を公開失敗や成功へ置き換えない。削除/期限超過/形式変更/保存量の増加は通知と状態表示で確認し、保存先の失敗・取得上限を解消して再取得する。

アプリには次の明示コマンドがある。接続値は環境・SSMから与え、引数に秘密値を入れない。

```sh
# 指定環境の変更を今取得する。変更がなければ新規作成しない。
api backup
# 移行済みの空DBと別画像保存先へ復元。IDは管理画面の保存済みID。
api restore --backup-id BACKUP_ID --confirm-empty-target
# 新しいDBサーバーへ復元し直す前に、そのDBの記事と付随する状態を消す。
api clear-restore-target --confirm-delete-content --new-database-host HOST
# ログインフローを含め、ローカルDBセッションを全失効。
api revoke-sessions --confirm
```

復元先DB名は`_restore`または`_test`で終わるか、`--new-database-host`で接続先のホスト名を明示し、対象表が空、移行版がアーカイブと同じことを要求する。Auroraへは`OnDemandRestoreTask`（アプリ用の利用者、バックアップの読み取りと画像バケットへの追加だけを持つロール）で、新構成のサブネット・`ApplicationSg`・公開IPv4を指定して実行する。バックアップ用S3は復元元、MEDIA_BUCKETは新しい復元先とする。記事数・本文・公開状態・画像ハッシュを照合し、匿名で下書き画像を取得できないことまで確認してから切替を判断する。

パスワード回復はCognitoの検証済み本人メールを使用する。TOTP端末を失った場合や侵害が疑われる場合は、管理者が本人を確認してCognito側を停止・回復し、CognitoセッションとDBセッションを失効させる。Cognito側だけのサインアウトでは8時間のDBセッションは消えない。別の管理ユーザーを無条件に追加して回避しない。AWSアカウント自体のMFA・回復手段も本人が保持する。

## 費用の見込み

オレゴン、オンデマンド、税別。Auroraの単価は2026-10-29版の[RDSオレゴン公式価格表](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonRDS/current/us-west-2/index.csv)で確認した。使用量と無料枠の適用は仮定であり、実測の請求ではない。

| 項目 | 計算の前提 | 月額USD |
|---|---|---:|
| Auroraの計算資源 | 0.12/ACU時。執筆20回×（作業1時間＋停止まで30分）と定期呼び出し30回×30分で月45時間、平均0.75 ACUと仮定 | 約4.05 |
| Auroraの保存領域 | 0.10/GB月×1GB、I/Oは0.20/100万回で少量 | 約0.15 |
| Lambda | 月数万回・512MiB。毎月の無料枠内と仮定 | 0 |
| CloudFront | 毎月の無料枠内と仮定 | 0 |
| S3 | 数GBと少量の要求 | 約0.25 |
| CloudWatch | ログ1GB未満、アラーム10件以内 | 約0.50 |
| 秘密値 | 接続文字列はSSMのSecureString。Auroraの管理者パスワードはSecrets Managerの1件 | 約0.40 |
| **小計** | | **約5.35** |

DBは定期呼び出しのほか、APIへの要求（存在しない画像への要求を含む）でも起きる（2026-11-17〜18の実測で1日数回）。Auroraの稼働時間が月90時間なら約4.05 USD増える。旧RDSの最終スナップショットの保管料、メンテナンスによる再開、無料枠の残り、キャッシュ無効化の超過、税、為替は含まない。切替後1か月の請求で、この範囲に収まるかを確かめる。

## 予算通知

AWS Budgetsに月45 USDの予算通知を設定した。対象はブログだけの費用ではなく、AWSアカウント全体。実績85％（38.25 USD）・実績100％・予測100％で本人の登録メールへ通知する。受信先はActive (legacy)を確認済み。予算のしきい値到達による通知発火は未試験。障害通知用SNSのテストメール到達とは区別する。自動停止などのアクションは設定せず、料金の強制上限ではない。費用監視と通知の利用は[公式料金](https://aws.amazon.com/aws-cost-management/aws-budgets/pricing/)上無料で、既存の他の予算設定は変更していない。
