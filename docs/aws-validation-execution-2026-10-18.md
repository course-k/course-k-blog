# AWSオレゴン実検証の記録

## 現在地

事前確認は成功したが、API Gateway Stageの作成時権限で二回ともロールバック。両試験スタックの削除完了（DescribeStacksで不存在）を確認し、Retainされた空のS3/ECR/ログを個別削除済み。今回の接頭辞を持つロググループも残っていない。DB/アプリタスクは未作成。現在はアクセス用スタックの修正版反映待ちで、アプリ環境を稼働させたまま待ってはいない。

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
