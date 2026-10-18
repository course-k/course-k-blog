# AWSオレゴン実検証の記録

## 現在地

**最新の訂正：API Gatewayのタグ権限は解消済み。** 修正版はCloudFormationとIAMの両画面で反映を確認。最新の最小構成試験はStageのアクセスログ設定で`logs:CreateLogDelivery`不足となった。先の応答で同じタグエラーと説明したのは、失敗イベント本文を最後まで確認せずロールバック状態から判断した誤りであり、本人の更新ミスとは扱わない。

追加のログ配信権限を公式資料から補ったアップロード用修正版を用意し、本人による既存アクセス用スタックの更新待ち。今回の確認用スタックも撤去済み。DB・アプリタスクは未作成。

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
