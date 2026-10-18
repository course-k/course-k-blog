> 位置づけ：本書は対象時点の判断・調査・実装・検証の記録です。本文中の「現在」「未着手」「次」は当時の状態を含みます。進捗と残作業の正本は[公開台帳](blog-publication-roadmap-2026-10-18.md)、現行仕様は[設計書](current-design.md)です。

# AWSオレゴン実検証の記録

## 現在地

**最新：AWSの一時配備検証と撤去を完了。** 新UI＋Go＋RDSのHTTP確認31項目、個別再配置、保存データ保持、アクセスログ到達を確認した。管理者から削除を再試行し、2026-10-18 20:19 JSTまでにスタックと残存6資源のDELETE_COMPLETEを画面で確認。保持物9件・SSM5件・本人承認後の最終スナップショット1件も削除。正式公開・実ブラウザ管理操作・全機能の受入完了ではない。以下の権限修正・撤去失敗は経緯として残す。

**最新の訂正：API Gatewayのタグ権限は解消済み。** 修正版はCloudFormationとIAMの両画面で反映を確認。最新の最小構成試験はStageのアクセスログ設定で`logs:CreateLogDelivery`不足となった。先の応答で同じタグエラーと説明したのは、失敗イベント本文を最後まで確認せずロールバック状態から判断した誤りであり、本人の更新ミスとは扱わない。

追加のログ配信権限を補った修正版は、本人の実行承認後、LLMが管理者ブラウザで変更セットを実行。2026-10-18 17:05:10 JSTにUPDATE_COMPLETEを確認した。ログ設定の読み取りも新しいSTSセッションで成功。実ログ設定の作成試験と、その後のDB・アプリタスク作成結果は後段に記録する。

## 範囲と開始履歴

本人が最大72時間・費用目安10 USD（税別、上限保証ではない）の仮データ検証を承認。独自ドメイン/DNS、実記事、既存VPC・DBは変更しない。正式公開の完成や継続課金の承認とは区別する。

本人がアクセス用スタックを作成し、専用ユーザーから配備用ロールへ接続成功。AWS実APIで事前確認後、`course-k-blog-validation-20261018`の新規基盤を作成開始した。

- 実行要求：2026-10-18 16:18:28 JST（07:18:28 UTC）。
- 最大撤去期限：2026-10-21 16:18:28 JST。検証が終われば期限を待たず撤去する。
- 配置地域：us-west-2。アプリ未起動の基盤作成段階でもDB等の費用が発生し得る。
- 変更セット：`course-k-blog-validation-initial`、全41件がAdd。既存資源へのModify/Removeなし。
- `EnableRuntime=false`、`EnableDatabaseTools=false`で開始。DB初期化・移行を終えるまでサービスを起動しない。

## 事前確認の証拠

- 接続：想定アカウントのcourse-k-validation-operatorをSTSで照合。
- AZ：usw2-az1/az2がavailable。
- DB：PostgreSQL 17.11がavailable、db.t4g.micro/gp3/20GiBが注文可能。
- Fargate：On-Demand枠64 vCPU。既存利用量を引いた空き枠や実配置成功を保証しない。
- CloudFormation ValidateTemplate：成功、必要capabilityはCAPABILITY_IAM。
- IAM Access Analyzer：4ポリシーを検査しERROR/SECURITY_WARNING/WARNINGなし。S3のバケット/オブジェクトARN重複に関するSUGGESTIONが二つ。追加の権限を付ける変更ではなく、配備を妨げる構文エラーとして扱わない。
- ARM64のローカルAPI/Web/初期化イメージの存在を再確認。

元の機械可読応答、変更セット、生成パラメーターはGit無視対象config/aws-validationへ保存。認証情報・秘密値は記録資料へ出さない。操作者の短期資格情報はプロセス内で扱い、グローバルAWS設定を上書きしない。

## 続く確認

基盤の完了と資源一覧→専用ECRへのdigest固定イメージ登録→新規DB用の秘密値設定→初期化・移行の単発タスク→Node/Goサービスと入口の起動→TLS/SSR/認証/静的ファイル/更新・復帰を実経路で確認→撤去・保持物・費用停止の確認。

実施した結果だけを追記し、未実施を成功扱いにしない。画像・外部記事・変更時バックアップの機能は、この基盤検証だけで完了とはしない。

## 初回作成の失敗と撤去

初回はStageの作成で`apigateway:TagResource`のAccessDeniedとなり、自動ロールバック。DBは作成に到達していない。イベントを保存し、残った空のS3バケット/ECR/ログだけを資源一覧に照合して撤去する。

AWSの[API Gateway V2権限表](https://docs.aws.amazon.com/service-authorization/latest/reference/list_apigatewayv2.html)はTagResource操作をPOST/PATCH/PUTに対応付ける。一方、エラー文字列そのままのapigateway:TagResource/UntagResourceをAccess Analyzerへ渡すとINVALID_ACTIONになることを確認した。存在しないアクションを追加して本人に権限更新を繰り返させない。

利用者タグを伴うステージ作成の権限判定が失敗した事実まで確認し、サービス内部の判定原因は未特定。まず任意のスタック利用者タグを付けない変更セットで成立性を確認する。IAMの権限・リージョン制限は広げず、資源の追跡はCloudFormationのスタック資源一覧と名前で継続する。この変更の成功は再実行結果を確認して追記する。

ECRへのDocker loginも端末の資格情報ストアへの書込み制限で停止。AWSトークン取得自体の失敗と区別する。検証専用の一時Docker設定だけで短期レジストリ認証を扱い、終了時に削除する。既存キーチェーン設定を変更しない。

## 二回目の結果と配備権限の修正

スタック名を`course-k-blog-validation-20261018b`として、任意の利用者タグなしでもStageの同じTagResourceエラーが再現した。タグを外す案では解消せず、成功扱いにしない。二回ともDB・アプリタスクの作成に到達していない。初回と同様、二回目のロールバックで残った空の資源も一覧に照合して撤去し、スタック削除を要求した。

修正はアクセス用テンプレートのGatewayThroughStackのActionだけ。従来のGET/POST/PATCH/PUT/DELETE列挙を`apigateway:*`とし、Resourceの`/apis*`・`/vpclinks*`・`/tags/*`、us-west-2条件、CloudFormation経由の条件は維持する。API Gateway操作の範囲は広がるが、アカウント全体の管理者権限・IAM権限・任意リージョンの権限は追加しない。API Gateway内の作成時タグ権限が列挙したHTTP動詞だけでは通らなかった実挙動を踏まえた修正候補で、反映後の実配備成功はまだ未確認。

この修正版はcfn-lintおよびAWS Access Analyzer（findingsなし）を通過。存在しないTagResourceという文字列をActionへ直接追加しない。操作者は自分の権限を変更できないため、本人へ既存アクセス用スタックを修正版で更新する操作を依頼した。RuntimeBoundaryは変更していないため、出力値を再提出する必要はない。

費用停止の判断はスタック削除完了と保持物の個別削除まで確認してから行う。請求の確定額は現時点で未確認であり、DB/タスク未作成だから実請求ゼロとは断定しない。

LLMは権限・配置案、読み取り専用事前確認、変更セット照合、実APIの実行・失敗分析、撤去と記録に使用。本人はアクセス用スタックの管理者操作を担当。静的検査・事前確認の成功と、実配備の失敗を区別して記録する。

## 更新連絡後の最小構成確認

本人の更新連絡を受け、新しいSTSセッションで確認。手元の元テンプレートはGatewayThroughStackがHTTP動詞列挙へ戻る未コミット変更となっていたため、そのファイルを自動上書きしなかった。AWSに実際に反映されたポリシー本文を操作者は参照できず、原因を断定しない。

DB/VPC/ECSを含む全体の再作成はせず、HttpApi/Stage/GatewayLogsの3資源のみの検証スタック`course-k-blog-validation-permission-check`で確認した。同じTagResourceのAccessDeniedが再現し、ロールバック。DB・アプリタスクは作成していない。

取り違え防止のため、[日付付きのアップロード用修正版](../infra/access/validation-access-gateway-fix-20261018.template.json)を別名で用意。内容はコミット済みの修正版と説明以外が同一で、IAM資源は同じ6件、RuntimeBoundaryや出力に変更なし。元ファイルに残った変更は保持した。日付付きファイルはこの管理者更新用スナップショットであり、将来の編集対象を二つへ増やす意図ではない。

修正版はcfn-lintとAWS Access Analyzer（findingsなし）を確認。本人へ確認画面の説明に`gateway permission revision 2026-10-18`が表示されることを確かめて、既存アクセス用スタックを更新するよう依頼した。反映・実動作の成功は未確認。

アップロード用修正版のSHA-256：`e03ae95dcc6bec9c9eea6f0c47d3d4d2bae626d74bf36dfeb44e19593ea8ac82`。

最小構成試験の3資源は全てDELETE_COMPLETEを確認し、スタック削除後のDescribeStacksでも不存在を確認。保持資源なし。

## 最新の最小構成試験：タグ権限解消後のログ権限不足

本人の更新連絡後、新しいSTSセッションでHttpApi/Stage/GatewayLogsだけを作成。全体のDB/VPC/ECSを再作成しなかった。Stageの失敗理由は`Insufficient permissions to enable logging`、不足アクションは`logs:CreateLogDelivery`。タグエラーの再発ではない。

ネイティブのブラウザ操作で、本人の管理コンソールを読み取り確認した。CloudFormationはUPDATE_COMPLETE、テンプレート説明はgateway permission revision 2026-10-18、IAMのOperatorPolicy1にもapigateway:*と元のResource/Conditionが反映されていた。これらの画面では権限の編集・保存操作を行っていない。API Gateway V2のコンソール上の許可要約だけから不許可と断定せず、実際のJSONと失敗イベントを優先した。

[HTTP APIログ設定の公式資料](https://docs.aws.amazon.com/apigateway/latest/developerguide/http-api-logging.html)と照合し、[ログ配信用のアップロード修正版](../infra/access/validation-access-logging-fix-20261018.template.json)を準備した。元のGateway修正版を維持し、OperatorPolicy2へCreate/Get/Update/Delete/ListLogDelivery、PutResourcePolicy、DescribeResourcePoliciesの7操作を追加。変更操作はus-west-2かつCloudFormation/API Gatewayからの代理呼出しに限定。API Gatewayが内部でログ配信を設定するため、CalledVia条件はCloudFormationまたはAPI Gatewayとした。ReadPreflightには配信・ポリシー状態の読み取り3操作を追加し、オレゴンのまま制限。Resource=*が必要な配信APIを含むので、今回のロググループだけにIAMで完全限定したとは言わない。

RuntimeBoundary、信頼先、PassRole、DB/S3の権限は変更しない。全4ポリシーをAccess Analyzerで確認しERROR/SECURITY_WARNING/WARNINGなし（従来のS3重複SUGGESTIONのみ）。cfn-lint成功、管理ポリシー文字数も上限内。静的検査・公式資料との照合は成功したが、この追加権限による実ログ配信は反映後に検証する。

以前のGateway修正版や元ファイルは上書きせず残した。日付付きファイルは各管理者更新の再現用スナップショットで、採用済みの最新ファイルは本段落で明示する。

今後は、ロールバックという状態だけで前と同じ原因と判断しない。失敗イベントのアクション・対象・理由を最初に確認し、管理者の更新を再依頼する前にAWSに反映済みの内容を照合する。

## 検証記録のGitGuardian検出への対応

GitGuardian Security Checksが、コミットd457a4cの検証エビデンスJSONからGeneric High Entropy Secretを1件検出。該当箇所はStageの失敗応答に含まれるCloudFormation RequestTokenで、AWS操作の識別子だった。AWSのAccessKeyId/SecretAccessKey/SessionTokenとは区別する。検出された値自体は本記録へ転載しない。

根拠：[AWS ProgressEventのRequestToken定義](https://docs.aws.amazon.com/cloudcontrolapi/latest/APIReference/API_ProgressEvent.html)、[GitGuardianの汎用検出器](https://docs.gitguardian.com/secrets-detection/secrets-detection-engine/detectors/generics/generic_high_entropy_secret)。汎用検出器がtokenという名前とランダムな値に反応することと、実際の認証情報漏えいは同一ではない。今回は資格情報の失効・再発行を要する対象と判断しない。

共有エビデンスから生のResourceStatusReasonを除き、必要な失敗要約、missingAction、service、handlerErrorCodeだけを構造化して残した。検証の原因・時刻・結果は保持し、RequestToken/Request IDを転載しない。未加工の応答はGit無視対象のローカル検証フォルダーで管理する。追跡対象のテキストを確認し、長いRequestTokenの代入値が残っていないこと、対象JSONの構文が正しいこと、代表的なAWS/GitHub認証情報の形式が存在しないことを確認した。正規表現の確認だけでリポジトリ全体のあらゆる秘密値がないと保証しない。

今後はAWS応答全体をそのまま共有記録へコピーせず、証拠として必要なフィールドだけを選ぶ。GitGuardianのスキャンを無効化したり、ファイル全体を除外したりしない。過去コミットの検出は残りうるため、最新チェックの結果と区別する。共有ブランチの履歴の強制書き換えは行わない。

修正コミット76facbcをpush後、GitGuardianの最新チェック（117620773378）を確認。failureは継続していたが、検出一覧は修正前d457a4cの同ファイル・同じGeneric High Entropy Secret 1件のみを参照していた。現在のファイルに識別値が残っていると判断せず、過去コミットの検出継続と区別した。認証情報ではないと確認したこのインシデントは、GitGuardian側でFalse positive（not a secret）として扱うのが適切。ダッシュボードでの状態変更はこの作業では未実施。

## 管理者ブラウザからのログ権限更新

本人が変更セットの実行を承認した後、LLMが既存の管理コンソールで`course-k-validation-logging-20261018`を実行。事前の変更セットはOperatorPolicy1/2のModify計2件、置換なしで、RuntimeBoundaryやロールの信頼関係は変更していない。2026-10-18 17:04:47 JSTに開始、17:05:10 JSTにスタックのUPDATE_COMPLETEと両ポリシーの更新完了を画面で確認した。

新しいSTSセッションの`logs:DescribeResourcePolicies`は成功し、試験前のポリシー数は0件。操作者によるアクセス用スタック自体のDescribeStacksは引き続き許可されていないため、更新状態は管理者ブラウザで確認した。追加の権限拡大は行わない。

同じ3資源（HttpApi/Stage/GatewayLogs）の最小構成を、新規変更セットの全3件Addを確認して再試験。CREATE_COMPLETEとGetStageのAccessLogSettingsを確認でき、従来のStage作成時の権限不足は解消した。DB・アプリタスクは作成していない。これはログ設定の作成成功であり、実リクエストのログ到達やアプリ全体の配備成功までは検証していない。確認用スタックの撤去を要求した。

撤去後、3資源すべてのDELETE_COMPLETEとDescribeStacksでのスタック不存在を確認。確認用のAPI・ロググループは保持していない。

## UI引き継ぎ後の基盤再作成

PR #3の採用UIを受領し、未コミット変更のないcb649c7のfrontendをARM64の独立イメージとしてビルド。本番ビルド・型検査成功。元の作業ツリーへUI変更をマージせず、試験用のNodeイメージだけを新UIに差し替える。GoとDBの業務コードは元ブランチを用いる。

`course-k-blog-validation-20261018c`の新規変更セットは全41件Addで、既存資源へのModify/Removeなしを確認して実行。今回、基盤はCREATE_COMPLETEへ到達。RDSは非公開・暗号化、VPC Linkも作成完了。初回DB作成には待ち時間があり、途中のcreating/backing-upをエラーや配備完了と扱わなかった。既存ドメイン・DNS・既存VPC/DBは変更していない。

専用ECRにGo・新UIのNode・DB初期化の3イメージを登録し、digestを保存。レジストリの資格情報は一時Docker設定だけで扱い、設定ディレクトリを削除。端末の既存キーチェーンを変更しない。

DB初期化・移行の両タスクが終了コード0。Runtime有効化の変更セットは追加24件で既存資源の置換なし。Node/Go各1タスクの安定状態とCloudFormationのUPDATE_COMPLETEを確認した。

新UIと実Go/RDSをHTTPS経由で接続し、HTTPの確認31項目が成功。未認証管理拒否、異Origin拒否、Secure/HttpOnly/SameSite Cookie、下書き非公開、初回公開、公開中の下書き保存による旧公開版維持、競合409、更新公開で初回公開日とID維持、非公開時の本文/API404、ログアウト後の拒否、JS実行なしの本文と目次、CSS/JSのMIMEと保護ヘッダーを確認。実ブラウザの管理操作・ハイドレーションは未検証。

起動処理中には入口が500を返す時間帯があった。安定稼働後の操作試験とは分け、初回配備時の公開タイミングに注意する。ログは設定成功だけでなく、Gatewayログストリームで200/201/401/403/404/409等の実記録到達を確認。Node/Go/DBの実SGにインターネット全体からの受信許可なし。

5並列・30要求の空一覧SSRスモーク試験は30/30成功。このクライアントからの中央値約0.51秒、最大約0.80秒。起動直後のCloudWatch標本はNodeメモリー約3.81%、Go約0.29%だが標本各1点で、最大記事・同期・画像・バックアップを含む本番必要容量を証明しない。

Goだけのforce-new-deploymentでタスクIDが変わり、NodeのタスクIDが変わらないことを確認。再配置後のreadyz/SSRとDBの下書き本文・非公開状態が保持されていた。続いてNodeだけを再配置し、GoのタスクIDが変わらず、Nodeの新タスクでhealthz/readyz/SSRが200になることを確認。これは同じイメージでのタスク置換と内部名前解決の追随であり、不良リリースからのロールバック試験や完全無停止の証明とはしない。

検証終了に伴い、Node/GoのdesiredCountを0へ変更し、RDS削除保護解除だけのCloudFormation変更セットを準備。仮記事しか保存していない検証環境として、承認済みの撤去範囲に従う。独自ドメインは公開していない。

撤去更新の直接変更はDatabase.DeletionProtectionのみ。変更セットにはDB属性参照を通じたBootstrapExecutionRole/BootstrapTaskのDynamic変更も表示されたため、内容を確認してから実行。DB置換はなく、参照先や権限の追加を直接変更していない。更新完了後、スタック削除を要求した。

## 撤去時の権限不足と残存

スタック削除はDatabaseでDELETE_FAILED。不足はrds:CreateDBSnapshotであり、ログ権限の再発ではない。テンプレートのDeletionPolicy=Snapshotに必要な操作を、こちらのアクセス定義で漏らしていた。DBは削除保護解除済みだが、削除自体は完了しておらず課金が続き得る。

[スナップショット用修正版](../infra/access/validation-access-snapshot-fix-20261018.template.json)は、反映済みログ修正版にrds:CreateDBSnapshotだけを追加。検証用プレフィックス、オレゴン、CloudFormation経由の条件は維持。ValidateTemplate成功、変更したOperatorPolicy1はAccess Analyzer findingsなし、4924文字。今回cfn-lintは実行環境に見つからず未実行。既存の元テンプレートの未コミット変更は上書きしない。

管理者ブラウザで修正を準備しようとしたが、ツールはMacロックを報告。ロックを迂回せず本人へ解除を依頼した。権限修正は未反映。直接サービス操作の権限を広げて回避しない。

独立して進められる撤去として、スタックでDELETE_SKIPPEDになった9保持資源（空のS3バケット2、ECR3、ログ4）を検証名・状態に照合して個別削除。専用SSM5件も削除した。DBと依存ネットワークの残存があり、撤去完了・費用停止とは扱わない。

## 撤去再開時の認証期限切れと管理者による再試行方針

本人のMacロック解除後、Brave操作競合による誤操作防止を確認し、操作を止めてもらった。その後、管理コンソールがサインアウト済みであることを確認。専用CLIのログインセッションも期限切れで、AssumeRole前に再認証を要求された。資格情報を推測・取得せず、本人へ既存管理者の再ログインを依頼。

[DeleteStack公式仕様](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DeleteStack.html)を確認し、サービスロール未指定なら削除要求者の一時資格情報を使用できることを確認した。本スタックはサービスロールを指定せず作成したため、管理者コンソールから削除を再試行する方針へ変更。今回の撤去のために操作者へ権限を追加する必要はなく、スナップショット修正版は未適用の修正候補として残す。管理者での実際の再試行結果は未確認。

本人の管理者再ログイン後、CloudFormation画面で対象がcourse-k-blog-validation-20261018c、IAMロールが未指定であることを再確認。通常の「削除を再試行」を実行しDELETE_IN_PROGRESSを確認。RDS詳細でも削除中、手動スナップショット一覧で本検証DB由来の最終スナップショット1件が作成中であることを確認した。操作者の権限追加は実行していない。専用CLIは再ログインしていないため、この再試行の証拠は管理者ブラウザの画面による。

## 撤去完了

管理者ブラウザで、Database/DbSg/DbSubnets/Private1/Private2/Vpcの6資源がすべてDELETE_COMPLETE、対象スタックもDELETE_COMPLETEと確認した。CloudFormationの「削除時間」2026-10-18 20:08:44 JSTは削除要求時刻であり、完了時刻として扱わない。完了確認は20:19 JSTまでに実施。

最終スナップショットは本検証DB由来の1件だけで、利用可能を確認してから削除確認画面を準備。本人の実行直前の明示承認後に削除し、成功通知と手動スナップショット0件を確認。自動バックアップの現在リージョン0件・保持0件も確認した。先に削除したS3/ECR/ログ9件・SSM5件と合わせ、今回の検証スタック・保持物の撤去を完了。不要になったローカルの検証用パスワードファイルも削除。専用IAMアクセス設定は維持し、操作者への追加権限変更は行っていない。

確定請求額とアカウント全体の課金ゼロは未確認。この撤去確認を正式公開や全機能の運用準備完了と混同しない。
