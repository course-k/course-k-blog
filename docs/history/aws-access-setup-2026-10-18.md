> 位置づけ：本書は対象時点の判断・調査・実装・検証の記録です。本文中の「現在」「未着手」「次」は当時の状態を含みます。進捗と残作業の正本は[公開台帳](../blog-publication-roadmap-2026-10-18.md)、現行仕様は[設計書](../current-design.md)です。

# AWS検証用アクセス設定

2026-10-18。本人が最大72時間・予算目安10 USD（税別、上限保証ではない）のAWS検証を承認。その後、既存のCLIプロファイルは全て別用途と確認した。

## 最新のアップロード対象

ログ修正版は反映・実動作確認済み。現在の追加候補は[スナップショット修正版](../../infra/access/validation-access-snapshot-fix-20261018.template.json)。撤去のCreateDBSnapshot不足1件を補い、対象・地域・CloudFormation経由の制限は維持。未反映。今回の撤去は管理者コンソールから通常削除を再試行して完了し、操作者への権限追加は行っていない。将来の操作者によるスナップショット付き撤去には、この不足への対応が必要。以下はログ修正時点の履歴。

現在は[validation-access-logging-fix-20261018.template.json](../../infra/access/validation-access-logging-fix-20261018.template.json)を使用する。説明に`gateway logging revision 2026-10-18`があることを確認する。

Gateway修正版の反映はCloudFormationとIAMで確認済み。タグ権限は解消し、最新試験の不足はログ配信のCreateLogDeliveryだった。「同じタグエラー」とした前の説明は訂正する。ログ配信に必要な公式の権限セットを追加し、cfn-lint・Access Analyzerで検査済み。既存アクセス用スタックへの反映待ち。詳細は実検証記録の最新節を参照。

## 最新：実配備で確認した追加修正

初回のアクセス設定は反映され、AssumeRoleと事前確認は成功した。ただしAPI Gateway Stageの作成時タグ操作がAccessDeniedとなり、実配備はロールバックした。利用者タグなしでも再現。アクセス用テンプレートはGatewayThroughStackの操作をapigateway:*へ修正し、元の対象リソース・オレゴン・CloudFormation経由の条件を維持した。修正版のcfn-lint・Access Analyzerは成功。本人による既存アクセス用スタックの更新待ち。検証記録の二回目の結果を参照。

## 最新：権限反映とAWS事前確認の成功

本人がオレゴンでアクセス用スタックを作成し、出力のRuntimeBoundaryArnを提供。専用ユーザーから操作者ロールへのAssumeRoleに成功した。以下のAccessDenied記録は設定前の履歴。

オレゴンのusw2-az1/az2、PostgreSQL 17.11、db.t4g.micro/gp3/20GiBの注文可否を実APIで確認。Fargate On-Demandのアカウント上限は64 vCPU（空き枠を保証する値ではない）。CloudFormation ValidateTemplateも成功。Access Analyzerによる4つのポリシーの検査はERROR/SECURITY_WARNING/WARNINGなし、S3 ARNの重複に関するSUGGESTIONが二つのみ。権限の実運用が全て成立した証明とはしない。

操作者プロファイルをAWS CLIに直接使うとグローバルの~/.aws/cliへキャッシュを書こうとして端末の制限にかかったため、事前確認ツールはブラウザ認証元から明示的にAssumeRoleし、短期資格情報をプロセス内だけで扱う方式に変更。グローバルキャッシュの権限を広げず、認証情報はファイル・画面に出さない。

続く実配備と撤去の状態は[AWS実検証記録](aws-validation-execution-2026-10-18.md)を参照。

## 再開時の確認（オレゴン採用後）

本人のオレゴン採用に合わせ、配備/権限テンプレートをus-west-2へ変更。SSM ARNの検査、権限のリージョン条件とARN、KMS ViaServiceも揃え、cfn-lintをオレゴン指定で確認済み。

公開/非公開サブネットは、アカウントごとに対応が変わるAZ名の並び順ではなく、VPC Link対応表にあるusw2-az1/usw2-az2を既定のAZ IDとした。同じAZを二つ選べない検査を加え、ECSのFargateプラットフォームを1.4.0と明示。

AWS STSでcourse-k-deployの本人接続を再確認。配備用ロールへのAssumeRoleはAccessDenied。この結果だけでロールの存在/不存在は断定しない。管理者側でアクセス用スタックの作成または更新が必要。アプリのAWS資源はまだ作成していない。

過去に東京用ファイルをダウンロード済みの場合、そのコピーを使わず、更新したファイルを再アップロードする。アクセス用スタックをすでに東京へ作成した場合は、IAMはグローバルのためオレゴンに同名ロールを二重作成せず、既存スタックを更新する。作成場所と許可する実行地域を区別する。

## 再開用の事前確認ツール

[scripts/aws-validation-preflight.py](../../scripts/aws-validation-preflight.py)を追加した。専用のローカル設定に、ブラウザ認証の`course-k-blog`を参照する`course-k-validation-operator`プロファイルを設定済み。既存のグローバルプロファイルは変更していない。

このツールはAWS資源を変更しない。想定アカウントと操作者ロールをSTSで照合した後、オレゴンのAZ、PostgreSQL 17の提供版、db.t4g.micro/gp3/20GiBの注文可否、FargateのOn-Demand vCPU枠、CloudFormationテンプレートの検証を行う。認証情報は出力・保存しない。結果はGit無視対象の`config/aws-validation/preflight-result.json`へ保存する。枠の値は空き容量や配備成功の保証ではない。

```sh
python3 scripts/aws-validation-preflight.py \
  --cli /tmp/course-k-aws-cli/expanded/aws-cli.pkg/Payload/aws-cli/aws \
  --expected-account 757412973334
```

2026-10-18の再実行はAssumeRoleのAccessDeniedで停止。想定外アカウントを渡すローカル検査でも、最初のSTS照合後に後続処理へ進まないことを確認した。この時点ではAWS上のAZ・DB・枠・テンプレート検証は未実施だった。後続の成功は冒頭を参照する。権限変更なしで同じ失敗を繰り返さない。

権限設定後の順序は、事前確認→新規資源だけのChange Set確認→基盤作成→イメージ登録と秘密値設定→DB初期化/移行→Node/Go起動→実経路の操作/SSR/復帰検証→撤去と保持物確認。課金対象を初めて作成した時刻と撤去期限を記録する。現時点で72時間の稼働検証を開始した扱いにはしない。

## 初回の確認履歴

- 本人はルートでコンソールを利用していた。`course-k-deploy` を新設し、本人がパスワードを変更した。
- 同ユーザーには IAMUserChangePassword と SignInLocalDevelopmentAccess を付与。
- AWS公式署名済み macOS CLI 2.38.1 を一時フォルダーに展開。既存2.15.34・既存プロファイルは上書きしていない。
- aws loginで認証し、STS GetCallerIdentityで同ユーザーを確認。追加のAIツール設定提案は辞退。
- 認証キャッシュ・専用プロファイルは無視対象のconfig/aws-validation配下。長期アクセスキーは新規発行していない。認証情報をドキュメントへ記録しない。
- RDS DescribeDBEngineVersions は AccessDenied。認証は成立しているが、配備・事前確認用の権限はまだない。
- アプリ用AWS資源はまだ作成していない。通知先メールアドレスも未指定。

## 用意した権限の構成

[アクセス設定テンプレート](../../infra/access/validation-access.template.json)を、本人が管理者のコンソールからオレゴン（us-west-2）へ配備する。スタック名は `course-k-validation-access`。

作るのはIAMの設定のみで、アプリ・DB・ネットワークは作成しない。

- `course-k-validation-operator` ロール。信頼先は既存の `course-k-deploy` だけ。セッションは1時間。
- 上記ロールの権限ポリシー。対象CloudFormationスタックは `course-k-blog-validation-*`。
- `course-k-deploy` に上記ロールのAssumeRole権限だけを追加するインラインポリシー。
- アプリ/実行/疎通確認ロールに必須となる権限境界。操作者は境界の変更・削除権限を持たない。

通常の配備は操作者ロールを使い、CloudFormationへサービスロールを渡さず、その呼出元権限で実行する。ネットワーク等の変更操作は `aws:CalledVia` でCloudFormation経由に制限した。新規環境のロール名・ECR・SSM・S3等はプレフィックスで制限。リージョンを持つ操作はオレゴンに制限する。

IAMの新規ロールは `course-k-blog-validation-*` だけで、指定の境界が必須。操作者自身、既存ユーザー、境界ポリシーは変更できない。PassRole先はECSタスク/Lambdaのみ。長期アクセスキー発行権限も追加しない。

**これはアカウント分離と同等の完全な隔離ではない。** ネットワーク作成等はResource=*を必要とする操作がある。CloudFormation経由という条件だけでは、テンプレートに記述した既存リソースへの影響まで自動的に防げない。実行前にChange Setと新規資源だけであることを確認する。権限境界のRDS管理秘密値参照は自動生成名rds!db-*に対する上限であり、アプリの実行ポリシーではさらに今回のDBの秘密値ARN一つへ制限する。

RDS管理者権限はBootstrapにのみ、通常DSNと管理者ハッシュはGoにのみ注入する既存方針を維持。境界は上限であり、境界だけでAPI実行権限が付与されるわけではない。

## 本人に必要な操作

1. ルートでログインしたウィンドウからオレゴンのCloudFormationを開く。
2. 「スタックの作成」→「新しいリソースを使用（標準）」。
3. 「既存のテンプレートを選択」→「テンプレートファイルのアップロード」で上記JSONを選択。
4. スタック名 `course-k-validation-access`。DeployUserNameは `course-k-deploy` のまま。
5. スタックオプションは既定のまま。実行用IAMロールは指定しない。
6. レビューでIAMの名前付きリソース作成の確認にチェックして作成。
7. CREATE_COMPLETE後、操作者ロールで認証・権限・RDSバージョン・枠を確認して、アプリ用スタックのChange Setへ進む。

このアクセス用スタックはアプリ検証用スタックと区別して管理する。アプリの撤去と保持物確認が完了した後、管理者が削除すると追加の操作者ロール・ポリシー・境界を削除できる。course-k-deployユーザーそのものは、このスタックが作成した資源ではないので削除しない。

## 検証の状態

2テンプレートに対するcfn-lint、JSON構文、管理ポリシーの非空白文字数を確認。AWS公式ポリシー生成器のアクション一覧で、記載したアクション名/パターンも照合。設定前はAWS Access Analyzer・CloudFormationの実API検証と作成は未実施だった。現在の結果は冒頭と実検証記録を参照。cfn-lint成功を権限の実動作保証としない。IAMコンソール/CloudFormationでエラーがあれば原因を確認して修正し、権限一括管理者化で回避しない。

## 根拠・理由

- [ブラウザ認証](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-sign-in.html)：長期アクセスキーを追加しないため。
- [CalledVia](https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_policies_condition-keys.html#condition-keys-calledvia)：操作者によるサービス直接変更を減らすため。サービスロール利用時とは条件の扱いが異なる。
- [権限境界](https://docs.aws.amazon.com/IAM/latest/UserGuide/access_policies_boundaries.html)：配備担当が作る実行ロールの権限上限を固定するため。

本人が行った認証操作と、LLMが準備した権限設定・検証を区別して記録した。管理者権限を一時的に丸ごと付与する案より設定量は増えるが、操作者自身と実行ロールの管理権限を分けて、後から撤去可能にする判断。
