# 本番配置と運用

**本番用IAMは適用済み。費用承認後、本番基盤・DB初期化・スキーマ移行は完了。3コンテナの1タスクとDNS/HTTPSも配置済み。本人の初回ログイン・TOTP登録と優先方式設定、通知購読とテストメール到達は確認済み。本番バックアップを別RDSと別画像先へ復元し、全行と画像の一致を確認済み。** 進捗は[公開台帳](../docs/blog-publication-roadmap-2026-10-18.md)、仕様と理由は[現行設計](../docs/current-design.md)で管理する。

MySQL移行後のローカル結合試験・TLS・権限分離・通し試験と、本番での配置確認は分けて扱う。以下にある以前の性能測定値はPostgreSQL時点のもので、MySQLの同一性能を保証しない。初回は提供可否を確認したMySQL 8.4.11、db.t4g.micro/gp3 20GiBを配置した。

## 使用する定義

[scripts/aws/production.py](../scripts/aws/production.py)が、旧検証のネットワーク・RDS・監視の[共通定義](aws/validation-stack.template.json)を読み、本番用の認証・保存・通知・独自ホスト名を追加し、[常駐部分を1タスクへ変換](../scripts/aws/single_task.py)する。旧検証の2サービスを、そのまま本番へ使う構成ではない。巨大なテンプレートを2本手修正する案より、共通構成の修正を両方へ反映できる。生成処理はAWS SDK・資格情報を使わず、配置しない。生成物をGitへ重複保存しない。

```sh
mkdir -p config/aws-production
python3 scripts/aws/production.py > config/aws-production/stack.template.json
python3 -m unittest discover -s scripts/aws -p 'test_*.py' -v
cfn-lint -r us-west-2 -t config/aws-production/stack.template.json
```

CIはcfn-lint 1.57.2で生成物を検査する。スキーマ検査と契約試験はIAMの実効権限、リージョン内の空き、実配備の成功を保証しない。従来の`validation.py`は検証環境だけを操作する制限を維持し、本番に転用しない。元の検証テンプレートだけでは現在のHTTPSアプリに必要なCognito/S3設定が揃わない。

本番の拡張点：

- 共有アプリタスクのIAMロールとその権限境界に、画像・バックアップの特定バケット内のGet/Put/DeleteObjectと両バケットのListBucketを追加。DeleteObjectVersion、公開ACL、IAM変更権限は与えない。同じタスク内のNode/NginxからもこのS3権限を利用可能になる制約を受け入れる。DB用SSMはタスク実行ロールが読み、接続値はGoコンテナだけへ渡す。[S3 GetObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html)ではListBucketなしの不存在も403になるため、バックアップで未保存と権限拒否を区別するために必要。
- Cognito Lite、提供ログイン画面classic、TOTP必須、自己登録禁止、verified_emailによる回復。Goは登録した本人のsubだけを受け入れる。クライアントシークレットと公開用パスワードハッシュは使わない。
- API Gatewayの地域証明書と独自ホスト名。DNSレコードは自動変更せず、既存DNSを使用する。ブログ専用サブドメインを前提とし、`/blog`等のパス配下へ置く場合はルーター・リンク・Cookieを別途対応する。
- 運用失敗のJSONログからメトリクスを作り、共有サービス等の6アラームと合わせた7件を同じSNSトピックへ通知。メール購読は本人による確認が必要。API入口は10要求/秒・burst20の開始値。
- アーカイブ4世代。S3版管理で残る旧アーカイブ版は7日後に消す。参照される画像コピーは期間だけで消さない。RDSは非公開Single-AZ、7日バックアップ、削除保護、保存資源はRetain。

定義の根拠：[Cognito UserPool](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/aws-resource-cognito-userpool.html)、[UserPoolClient](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/aws-resource-cognito-userpoolclient.html)、[機能プラン](https://docs.aws.amazon.com/cognito/latest/developerguide/cognito-sign-in-feature-plans.html)、[APIドメイン](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/aws-resource-apigatewayv2-domainname.html)、[S3旧版の期限](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/aws-properties-s3-bucket-noncurrentversionexpiration.html)。

## 共有タスクのイメージと入口

Node/Goは従来のDockerfile、Nginxは`docker build --platform linux/arm64 -t course-k-router:production infra/router`でビルドする。専用ECRへ登録後、NodeImage / GoImage / RouterImageの3つをdigestで固定する。Nginxはタスク内localhostへ転送するだけで、TLS終端はAPI Gatewayに委ねる。タスク実行ロールにはこの3リポジトリとロググループ、DB用SSMのみの読み取り・ログ送信を許可する。

通常のアプリはApplicationService / ApplicationTask / ApplicationSg。Cloud MapはNginxの8080を登録し、VPC LinkのSGからそのポートだけへ受信を許可する。Node/Goへ直接の受信を開けない。DB初期化・移行・復元の単発タスクにはApplicationSgを指定する。旧検証用のGoSgやNodeServiceを本番へ指定しない。Nginxの一時ボリュームはDockerfileのVOLUMEと同じ`/tmp/nginx`を指定し、非rootの書き込み先を確保する。

## 本番の構成と運用条件

### 採用した構成

公開先は`https://blog.course-k.com`、リージョンはus-west-2、スタック名は`course-k-blog-production`を使う。本人メール・通知先とオレゴンの発行済み証明書ARNはGit対象外の`config/aws-production/domain.json`に記録済み。証明書の発行とDNS/HTTPS接続は確認済み。後日の静的配信・Lambda化は現行構成に含めない。

通常はNode/Go/Nginxの1タスク（ARM64、0.5vCPU/2GiB）、公開IPv4 1個、非公開RDS MySQL db.t4g.micro・gp3 20GiB。ネットワークは新規VPC・4サブネット・3セキュリティグループ、入口はHTTP API・VPC Link・Cloud Map・Nginx、保存は非公開S3 2バケット。ECR 4リポジトリのうち1件は初期化用。ECSタスク定義は通常用・移行用・初期化用の3件だが、常駐サービスは1件だけで、移行用・初期化用を常駐させない。ほかにCognito、SNSメール購読、7アラーム、ログ5グループ、15分間隔の疎通確認Lambdaと定期ルールを含む。詳細な資材の正本は生成定義とし、ここではアプリ本体の全68論理資源を68台のサーバーと解釈しない。

費用は本書の開始例で税前約39.03 USD/月。税前約39.03 USD/月と初回の復元・負荷試験の追加5 USD以内は本人承認済みで、後日案の約18.13 USDを今回の料金として扱わない。通常1タスクでも更新時は一時的に2タスクになる。DB作成時点からDB課金が始まるため、EnableRuntime=falseを無課金の待機と扱わない。

### 本番権限の分離

#### GitHubからのアプリ更新と専用アクセス

`python3 scripts/aws/delivery_access.py`で専用のアクセス定義を生成する。これはAWSを呼び出さず、本番更新も開始しない。管理者が別スタック`course-k-blog-delivery-access`として確認・登録した。2026-10-30に既存IDプロバイダ0件を確認したうえでGitHub用OIDCプロバイダと専用ロールを作成し、両資源とスタックのCREATE_COMPLETEを確認済み。後日の変更は同じスタックの更新として扱う。他のスタックを新設してこのプロバイダを重複作成しない。

接続元は`course-k/course-k-blog`の`main`だけで、GitHubの通常のsubject形式と所有者・リポジトリID付き形式を完全一致で許可する。PR、別ブランチ、Environmentを使うsubjectは許可しない。GitHub Actionsで`id-token: write`を宣言し、`course-k-blog-github-deploy`を利用する。長期アクセスキーや管理者権限をGitHubへ渡さない。

権限はオレゴンの本番ブログ用ECR（node/go/router）へのイメージ登録、既存本番スタックの変更セット、CloudFormation経由のアプリ・移行タスク定義更新と既存サービス更新、関連する監視の更新、更新前バックアップ用のアプリタスク起動に絞る。CloudFormationの公式リソース仕様が要求するタスク定義・実行ロールの照会と、同じサービス/監視のタグ・配備状態操作も、CloudFormationを経由する場合だけ許可する。DescribeTaskDefinitionとDeregisterTaskDefinitionはARNで限定できないため、この経路に限ってResource=*を使い、GitHubから直接の全定義照会・無効化は許可しない。サービスのRegistryArn解決に必要なCloud Map GetServiceは、既存の本番サービス1件だけをCloudFormation経由で照会できる。スタック出力の再評価では、既存DB1件の構成照会、API/ドメイン各1件のGET、bootstrap用ECRのURI照会もCloudFormation経由だけ許可する。DBレコードや管理者秘密値は取得できない。渡せる実行ロールは既存のアプリ・移行用4種で、自分の権限変更、IAMユーザー作成、別ロールへの切替、RDS/DNS/ネットワークの変更、S3や秘密値の直接読み取りは許可しない。ただし、配備されるアプリは実行時の権限で記事・画像・バックアップへアクセスできる。配備権限が記事データから完全に隔離される意味ではない。

[検証ワークフロー](../.github/workflows/checks.yml)の`Deploy production`が、mainへのpushまたはmainを指定した手動実行で、4つの検査ジョブがすべて成功した後に実行される。PRでは配備しない。新しいmainに置き換わった版と、文書だけのpushは配備しない。実行中の本番更新を新しいpushでキャンセルせず、配備を直列化する。GitHub環境変数のアカウント番号は接続先識別子であり、秘密鍵ではない。公式AWS認証アクションv6.3.0をコミットで固定し、OIDCで取得する一時権限を配備ジョブだけに与える。

[`delivery.py`](../scripts/aws/delivery.py)は以下の手順を実行する。`--apply`がない場合は読み取りと前提確認までで、ビルド・バックアップ・更新は開始しない。

1. AWSアカウントと専用ロール、テスト対象とcheckoutのコミット、既存本番の安定状態・ARM64・固定イメージを照合する。CloudFormationサービスロールが付いた場合は停止する。DB移行ディレクトリと移行処理が適用済み基準コミット672e66fから変わっている場合も停止する。DB移行は別途レビュー・実施後に基準を更新する。
2. GitHubのARM64ランナーでNode/Go/Nginxをビルドし、専用ECRへ登録してdigestで固定する。Docker認証情報は一時ディレクトリに置いて終了時に削除し、保存・表示しない。
3. 既存アプリ定義のGoコマンドを`backup`にした単発タスクでバックアップを実行する。成功した場合だけ更新を続け、その単発タスクだけを停止する。
4. 現行CloudFormationテンプレートと他のパラメータを維持し、NodeImage/GoImage/RouterImageだけの変更セットを作る。アプリ・移行タスク、置き換えないアプリサービス、サービス名に追随する監視以外の変更は拒否してから実行する。
5. 更新したパラメータでスタックが完了したこと、稼働タスクのdigest・全コンテナのHEALTHY、公開HTTPSの一覧・readiness・healthを確認する。起動失敗はECS/CloudFormationのロールバックに任せ、配備を失敗にする。更新完了後の確認で失敗した場合は、元の3イメージのdigestへ戻して確認し、候補の配備自体は失敗として扱う。

アクセス定義の単体試験・構文検査は、OIDCの実接続や本番配備の成功を保証しない。実際の実行結果は公開台帳とGitHubジョブで区別する。作成済みOIDCプロバイダと専用ロールは維持し、基盤・認証・DB構造の変更を通常のアプリ自動更新へ混ぜない。公式仕様は[GitHub OIDC](https://docs.github.com/en/actions/how-tos/secure-your-work/security-harden-deployments/oidc-in-aws)、[ARM64ランナー](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)、[CloudFormationサービスロール](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/using-iam-servicerole.html)を参照する。

検証用の`course-k-validation-operator`と本番用の`course-k-production-operator`を分離する。本番用アクセス設定は管理者が適用済みで、次の範囲を対象とする。

| 対象 | 本番計画で必要な範囲 |
|---|---|
| スタック・資源名 | 検証用の`course-k-blog-validation-*`ではなく、本番スタックと対応リソースに限定したCloudFormation操作・参照 |
| 実行ロール・権限境界 | 管理者が境界1件を作成し、操作者は本番の実行/タスク用ロール6件にその境界を必須で付ける。参照・インラインポリシー・必要なPassRoleを照合。操作者が自分の権限を自由に変更できる設計にしない |
| RDS | MySQL 8.4のパラメーターグループ、非公開DB、サブネットグループ、管理者secret、削除前スナップショット。作成・復旧・保護の変更範囲を限定 |
| Cognito | UserPool・Client・Domainの作成/更新/参照に加え、本人1人の登録と回復。管理者用の操作とアプリが行うOIDC通信を区別 |
| API・証明書 | HTTP APIの独自ホスト名とマッピング、既存ACM証明書の参照、API Gatewayのログ配信 |
| S3・ECR・SSM | 本番専用バケット/リポジトリ/SSMパスへの配置と運用。通常DB用とDDL用の秘密値を分離し、不要な版削除や他サービスへの操作を与えない |
| 監視・通知 | ロググループだけでなくMetricFilter、アラーム、SNSトピック/購読、疎通Lambdaと定期ルールを照合 |

本番用ロールで、基盤作成・DB初期化・移行・アプリ配置・別RDSへの復元と一時資源の撤去を確認済み。RDSスナップショット/PITR復旧、本人の資格情報紛失時の回復は未試験であり、これらの実効権限まで確認済みとは扱わない。

### 管理者が適用する本番用アクセス設定

[scripts/aws/production_access.py](../scripts/aws/production_access.py)で管理者用のアクセス定義を生成する。これはAWS APIを呼ばず、既存の検証用定義も上書きしない。

```sh
python3 scripts/aws/production_access.py > config/aws-production/access.template.json
```

管理者はオレゴンの別スタック`course-k-blog-production-access`として内容と名前付きIAM資源を確認して適用する。作成するのはIAMの操作者ロール1件・管理ポリシー5件（うち1件は実行ロールの上限）・既存course-k-deployへのAssumeRoleポリシー1件で、DBや常駐アプリの課金は開始しない。既存の検証用ロール・ポリシーを置き換えない。検証環境用の保存済みオブジェクト版・スナップショット等の直接削除権限は引き継がず、本番データの撤去は管理者の別操作とする。操作者には自分のロールやこの上限ポリシーを変更する権限を与えない。出力のRuntimeBoundaryArnは操作者から読み取れるため、本人へARNのコピーを繰り返し依頼する運用にはしない。

適用後は専用の`course-k-production-operator`へAssumeRoleし、アカウント・ロールを照合して、既存証明書と本番スタックを再確認する。MySQLは読み取りで提供を確認した8.4.11を初回配置に指定する。Cognitoの本人管理はProject=course-k-blog-productionタグのプールに限定し、本人1人だけを登録する。CloudFormation経由の作成/更新を広い資源指定で許可する箇所は残るため、万能な最小権限やアカウント全体の隔離を保証する設定とは説明しない。

ポリシー検査・構文検査の成功は実効権限や配置成功を保証しない。生成物と現行ソースを照合し、管理者による権限変更の承認後に適用する。

### 回復目標の扱い

アプリの変更時バックアップ4世代とRDS自動バックアップ7日を維持する。公開反映後は速やかに取得し、下書き等はUTCの日付境界でまとめる。障害通知を受けて手順に沿い手動で復元する方針を本人が受入済みで、復旧時間の保証や数値目標は設けていない。許容損失は取得周期だけで決めず、最後に成功したアーカイブ世代とRDSの復元可能時点を示して判断する。

スタック名は小文字の`course-k-blog-production`、SSMパスは`/course-k/blog-production/`を使用する。RuntimeBoundaryは管理者用の別スタック`course-k-blog-production-access`で作り、その出力ARNを本番定義へ渡す。操作者へ境界の作成・更新権限を与えず、検証用境界も流用しない。DB用パスワード・DSNは権限0600のGit対象外ファイルからSSM SecureStringへ登録し、コマンド引数・ログ・PRへ値を出さない。通常DML用と移行DDL用は分ける。

## 配置・更新の順序

| 段階 | 作業と終了条件 |
|---|---|
| 1. 承認する計画 | 確定した公開URL、全資源の月額見込み、IAM差分、保持方針を確認。継続課金とDNS変更の対象を示して承認を得る |
| 2. 証明書・基盤 | オレゴンの対応MySQL版とアカウントを確認。ACMの証明書を発行・DNS検証し、EnableRuntime=falseで基盤を配置。まだアプリは稼働しないが、DB等の課金は始まる |
| 3. 本人・データ準備 | 自己登録を使わずCognitoに本人だけを作る。本人所有メールを確認し、subをAdminSubjectへ設定。初期認証情報は本人が安全に保持し、パスワード変更・TOTP登録は接続後の段階6で行う。本人の秘密値をチャットへ送らない |
| 4. ビルド・DB移行 | 同じDockerfileからLinux ARM64のNode/Go/Nginx/DB初期化イメージを作り、ECRへ登録してdigestを固定。新規DBのロールを初期化後、MySQLの001_initial.sqlの移行を単発タスクで実行。実データがあるDBを再初期化しない |
| 5. 配置 | 公開URL、DB用SSM ARN、Cognito本人sub、digestを設定し、変更セットを確認してEnableRuntime=true。1サービス・通常1タスク（0.5vCPU/2GiB）の常時稼働・ヘルスチェック・DB接続を確認 |
| 6. 接続確認 | 専用ホスト名のDNSをRegionalDomainNameへ接続。Cognitoログイン・TOTP・サインアウト・回復、非公開S3の拒否、記事/画像の公開制御、同期、バックアップ、メール通知到達を確認 |
| 7. 運用受入 | 別RDS・別画像バケットへの復元、候補版の負荷、失敗イメージからの戻し、請求/予算通知を確認。許容損失と復旧時間に合うことを確認して公開 |

初期化・移行・復元用のタスクは必要時だけ動かす。ECSの完了コードと移行結果を確認し、アプリが起動したことだけで移行成功にしない。DNSの実URL、承認済み額、適用済みdigest等はGit対象外の環境設定へ保存する。

更新は変更前バックアップを確認し、後方互換の移行を先に行い、変更したコンテナのイメージを新digestへ指定し、Node/Go/Nginxを含むタスク全体を更新する。変更しないイメージは同じdigestを維持する。ローリング更新中は新旧2タスク・公開IPv4 2個が一時的に並行する。通常の1タスク料金は更新中の追加分を含まず、その分は予備枠へ計上する。ECS circuit breakerは起動に失敗した配置を戻すが、起動できる論理不具合までは判定しない。直前に稼働したdigestを保持し、公開疎通・投稿・画像・監視を検査する。不具合時は先のdigestへ戻し、DDLが非互換なら自動で巻き戻さず復元案を扱う。ECRの整理では現行・直前・復元に必要なイメージを消さない。

## 現在の配置結果

2026-10-19、mainのdd049adと一致する基盤変更セットを費用承認後に実行し、CREATE_COMPLETEを確認した。RDSは暗号化・非公開・7日バックアップ・削除保護、両S3は公開アクセス全面ブロック・SSE-S3・版管理が有効。DB初期化とMySQLの001_initial.sqlの移行は別々の単発タスクで終了コード0を確認。通常用と移行用の接続値はSSM SecureStringに分離し、Gitやコマンド引数へ秘密値を保存していない。

同じmainからARM64の4イメージを作成し、専用ECRへ登録。アプリはNode/Go/Nginxのdigest固定・通常1タスクでUPDATE_COMPLETE、全コンテナHEALTHYを確認した。XServer DomainにblogのCNAMEを追加し、通常HTTPSで疎通できる。具体的ARN・イメージdigest・接続先・操作状態はGit対象外のconfig/aws-productionへ保持する。

未ログイン管理APIの401、両S3の匿名403、JavaScriptなしのSSR、公開一覧の外部107件、疎通監視の実行を確認した。Cognitoの本人初期設定、TOTPの有効・優先設定、SNS購読とテストメール到達を確認した。専用の検証記事で保存・公開・更新・画像差し替え・非公開化を行い、公開前/差し替え前/非公開後の匿名画像404、公開画像200、公開版の維持と初回公開日/固定URLを照合した。検証記事は非公開で保持する。別RDSへの5テーブル/画像2点の復元、一覧600取得の失敗0とp95約0.661秒、失敗版から元の定義への復帰も確認した。手動復帰の要求とAWS自動ロールバック開始イベントが重なるため、自動回復単独の完走は未確認。具体的な条件と限界は公開台帳へまとめる。

## 別RDSへの復元リハーサル

[scripts/aws/recovery.py](../scripts/aws/recovery.py)は本番定義を拡張し、既存資源を変更せず、明示的に有効にした間だけ一時RDSと画像バケット、単発タスクを追加する。通常の定義を別に手修正しない。本番のDBと画像保存先は切り替えない。復元先は新規・非公開・暗号化のblog_restore、画像は専用の非公開バケット。パスワードとDSNも復元用のSSMへ分離する。

最初はEnableRestoreExercise=true / EnableRestoreTasks=falseで一時DBとバケットだけを作る。完了後、復元専用SSM、RestoreCheckImageの固定digest、管理画面で確認したRestoreBackupIdを指定して単発タスクを有効にする。既存の移行用ロールを使うためEnableDatabaseTools=trueも必要。新規DBの権限初期化、移行、復元照合の順に各タスクの終了コードを確認する。

[restorecheck](../backend/cmd/restorecheck/main.go)は通常の復元処理を実行し、復元先から再取得した全表を、行順と変更世代だけを除いてアーカイブと比較する。本文・公開状態・画像参照・作成要求の対応・外部記事を含み、画像本体もサイズとSHA-256を照合する。SQLの行順やJSON数値精度によって差を見逃さない試験を付ける。本番ホスト・通常のblog DB・本番画像バケットは対象として拒否する。

復元専用のIAMロールは元バックアップを読むだけで、削除は復元用画像バケット内だけに許可する。成功後の--cleanup-mediaはコピーした画像だけを削除する。検証用バケットは版管理なしで、本番2バケットの版管理を弱めない。最後に両フラグをfalseにした変更セットがRestoreで始まる一時資源の削除だけであることを照合し、空バケットと一時DBを撤去する。一時DBには自動バックアップ・削除保護・最終スナップショットを付けず、復元元は本番側に保持する。失敗した場合は再初期化せず対象を調査する。

### 起動失敗版からの戻し方の確認

一時資源の有効期間だけRollbackTestTaskの定義を追加する。この定義を作るだけではサービスは切り替わらない。通常の固定イメージを使い、Goの未対応コマンドでDBへ接続する前に終了させる。現在の正常なタスク定義・通常1台・全コンテナHEALTHY・直前の完了したデプロイ・minimumHealthyPercent=100 / maximumPercent=200・circuit breakerのrollback=trueを照合してから、対象サービスに限って失敗定義への更新を試す。

既存の正常タスクを保持し、起動失敗と自動ロールバックをECSイベントで確認する。公開HTTPSの応答と、サービスが元のタスク定義へ戻ったことも照合する。自動回復が進まない場合は同じ正常な定義を明示して戻す。DB移行は行わない。終了後は元の定義が稼働中であることを確認してから、未使用の失敗用定義を一時資源と一緒に撤去する。これは起動失敗への回復確認で、起動後の論理不具合や非互換DDLの回復を保証する試験ではない。

このリハーサルはアプリのバックアップから別RDSへ戻す確認で、RDSスナップショット/PITRの復旧や全スキーマ版への互換性を確認した意味ではない。通常費用に加算せず、承認済みの初回追加5 USD以内の作業予算で一時資源の稼働時間を管理する。

## 保存と回復の操作

ローカルは`mysql_data`・`media_data`・`backup_data`の3ボリュームを保持する。`docker compose down -v`は通常停止に使わない。同一端末の別ボリュームは端末故障への独立バックアップではない。本番は別の非公開S3バケットへ保存する。

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

アプリはNode/Go/Nginxを合計0.5vCPU/2GiBの1タスクとし、公開IPv4は通常1個。RDS db.t4g.micro/20GiBとともに本番受入の開始値とする。共有候補のローカル性能確認と限界は[現行設計](../docs/current-design.md#タスク統合候補の性能確認)を参照。追加機能の試験では200記事×50KiBの作業版と公開版（計19.5MiB）を別DBへ復元し、最大1200万画素のPNGを再エンコードできた。macOS上の独立試験プロセス・GOMEMLIMIT=512MiBで最大常駐メモリはそれぞれ約175MiB・149MiB。画像は透明の規則的パターン、記事は反復文字であり、圧縮率・CPU時間・並行実行・Fargate ARM64の性能を保証しない。DB JSON 64MiB/展開アーカイブ128MiBの上限や、複数作業の同時実行には別の負荷確認が必要。

2026-10-19に[AWS公式オレゴン価格表](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonECS/current/us-west-2/index.json)と各サービスの公開表を確認。月730時間、オンデマンド、ARM64、アプリ1サービス・通常1タスク、総API要求10万、S3保存6GiB・PUT1万/GET10万、ECR2.1GiB（Nginx追加分を含む仮定）、ログ取込/保存各1GiBを例にする。アクセス量・保存量は想定であり、実測の確定請求ではない。

| 項目 | 計算の前提 | 月額USD（税前） |
|---|---|---:|
| アプリ共有タスク | (0.5×0.03238＋2×0.00356)×730。Node/Go/Nginxで共有 | 17.02 |
| 公開IPv4 1本 | 1×0.005×730。ALB・NATは作らない | 3.65 |
| RDS | db.t4g.micro 0.016×730 | 11.68 |
| RDS保存 | gp3 20×0.115 | 2.30 |
| API Gateway | HTTP API 10万×0.000001 | 0.10 |
| S3 | 6×0.023＋PUT1万＋GET10万 | 0.23 |
| ECR | 2.1×0.10 | 0.21 |
| CloudWatch | 取込0.50＋保存0.03＋7アラーム0.70＋独自指標0.30 | 1.53 |
| Cloud Map・内部DNS | 1登録0.10＋検出10万0.10＋登録等千回0.001＋private zone0.50 | 0.70 |
| Secrets Manager | RDS管理者secret1件0.40＋API千回0.005 | 0.41 |
| Cognito | 本人1MAU、無償枠を使わない場合0.0055 | 0.01 |
| 小額処理・通信の予備 | 監視Lambda・通知・更新/単発タスク・AZ間通信等の予算枠。固定単価ではない | 1.20 |
| **例の合計** | インターネット転送30GiBをアカウントの月100GB無償枠内と仮定 | **約39.03** |

[Fargate](https://aws.amazon.com/fargate/pricing/)、[RDS](https://aws.amazon.com/rds/mysql/pricing/)、[S3](https://aws.amazon.com/s3/pricing/)、[CloudWatch](https://aws.amazon.com/cloudwatch/pricing/)、[Cloud Map](https://aws.amazon.com/cloud-map/pricing/)、[Route 53](https://aws.amazon.com/route53/pricing/)、[Cognito](https://aws.amazon.com/cognito/pricing/)を参照。Cognitoの直接ログイン1MAUは通常10,000MAUの共通枠に収まるが、組織/アカウント全体の残枠は未確認。private hosted zoneの問い合わせはRoute 53で追加料金なし。公開DNSは既存サービスを使う前提で、新しいRoute 53公開ゾーンを必須にしない。

税・為替・ドメイン更新・有償CI、転送枠を超える通信、増えた画像/ログ/旧版/スナップショット、RDSの追加CPUクレジット等は別。通常のSSM Standardパラメーターと本番VPCを使い、Spot・予約割引・期限付きクレジットで安く見せない。概算を月額上限の保証にはしない。通常月の見込み額は本人承認済みで、予算通知を設定した。運用中もアカウントの料金条件・無償枠・実際の利用量を確認し、構成変更時には再見積もりする。

## 予算通知

AWS Budgetsに月45 USDの予算通知を設定した。対象はブログだけの費用ではなく、AWSアカウント全体。実績85％（38.25 USD）・実績100％・予測100％で本人の登録メールへ通知する。受信先はActive (legacy)を確認済み。予算のしきい値到達による通知発火は未試験。障害通知用SNSのテストメール到達とは区別する。自動停止などのアクションは設定せず、料金の強制上限ではない。費用監視と通知の利用は[公式料金](https://aws.amazon.com/aws-cost-management/aws-budgets/pricing/)上無料で、既存の他の予算設定は変更していない。

### 最新範囲から漏れる過去の外部記事の初回補完

Wantedlyの公開プロフィールで過去記事を追加表示し、各記事の公開構造化メタデータで本人の著者URL・タイトル・公開日を照合する。本文は転載しない。自動同期の最新範囲と、初回の過去記事補完を区別する。

補完には`cmd/externalimport`の一度限りの管理コマンドを使う。`scripts/aws/backfill.py`は既存の本番・復元定義を維持し、既定では無効なタスク定義だけを追加する。実行時は`EnableExternalBackfill=true`、Go用ECR内の固定digest画像、確認した公開メタデータの配列を指定する。JSONの項目は`id`・`title`・`url`・`publishedAt`、最大20件/4096バイト。DB接続先は既存のSSM秘密値から取得し、タスク上書きの平文環境変数に置かない。

タスクは`--confirm-owner-metadata`と本番DBホストの一致・TLS検証を要求し、既存記事を更新せず未登録分だけを一つのトランザクションで追加する。既存の本人補正・非表示・版番号や同期の取得範囲は変更しない。新しいIAM権限や公開APIは追加せず、実行プロセスのAWSロールは権限なし。終了コード0・挿入数・公開一覧のリンクと日時を確認し、バックアップ後にタスク定義を無効化する。後日の自動取得が全履歴を保証する意味にはしない。

2026-10-19の初回補完では、公開プロフィールで本人の14件を照合し、未登録4件だけを追加した。公開一覧はQiita92・Wantedly14・note1、計107件。4件のタイトルと元公開時刻を公開APIと照合した。通常のWantedly同期は最新範囲のままで、将来の全履歴取得の保証は追加していない。

復元用DB・画像バケット・権限・タスク定義と初回補完定義は試験後に無効化して撤去した。復元専用SSMの4値も削除した。本番は通常1タスク/3コンテナHEALTHY、元のRDSは非公開・暗号化・7日バックアップ・削除保護を維持する。補完後のバックアップコマンドは終了コード0を確認し、単発タスクは停止した。通常の継続課金は残るが、一時DBの稼働は継続しない。

本人の最終画面確認は「問題なし」。現行バックアップ・通知後の手動復元・時間保証なしで開始する方針の確認を得て、公開の受入を完了した。自動回復単独の完走や資格情報紛失・PITRの未試験を成功扱いにはしない。
