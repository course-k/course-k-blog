# AWS設定と実行手順

本番候補の認証・保存・独自ドメイン・費用・回復は[本番配置と運用](production.md)を参照。以下の補助コマンドは撤去済みの検証構成用で、本番へ転用しない。現在のHTTPSアプリには本番拡張のCognito/S3設定が必要。

このリポジトリのAWS定義は**オレゴンの検証用**。本番環境は未作成で、本書の手順を実AWSで再実行したわけではない。配置・運用上の選定理由は[現行設計](../docs/current-design.md)、残作業は[公開台帳](../docs/blog-publication-roadmap-2026-10-18.md)を参照する。

## 使用する設定

| ファイル | 役割・適用状態 |
|---|---|
| scripts/aws/production_access.py（リポジトリ直下） | 管理者用の本番アクセス設定を生成。操作者と実行ロールの権限上限を本番アプリから分離する。本人承認後に本番用アクセススタックへ適用済み。既存の検証用設定は更新しない |
| aws/validation-stack.template.json | Node / Go別サービス、非公開RDS、S3、入口、監視を含む一つの検証スタック。基本構成の実AWS検証に使った定義を移動したもの。本番用ではない |
| aws/validation-environment.example.json | 資格情報を含まない環境設定の例。アカウント・接続元プロファイル・新しい検証スタック名・ARN・MySQL版・ローカルイメージを指定する |
| access/validation-access-applied.template.json | 記録上、AWSへ反映済みのログ配信対応版。移動のみで権限は変更していない。現在のAWSとの再照合は未実施 |
| access/validation-access-pending.template.json | 未反映の修正候補。限定されたCloudFormation経由の削除前スナップショット作成と、MySQL移行用DBパラメーターグループの操作権限を含む。反映済み版や本番用の実効権限とは扱わない |
| database/ | 新規検証DB用のロール初期化と、その単発コンテナ |
| monitoring/ | 疎通確認と対応テスト |

入口だけの旧サンプルは全体スタックと二重管理せず、全体定義へ一本化する。IAMの適用済み版と候補版は、管理者が何を反映するか確認するために分ける。候補を正本へ上書きして「反映済み」と扱わない。権限の適用を補助スクリプトから自動実行しない。

## コード・環境値・秘密値の分離

- 実行コード：`scripts/aws/`。接続元の長期キー、固定アカウント、端末内CLIパスを埋め込まない。
- 環境設定：例を `config/aws-validation/environment.json` へコピーし、値を設定する。Git管理外。検証ではPublicOriginを省略または空にし、生成APIエンドポイントをオリジンに使う。画像の参照はARM64のローカルイメージ名。
- 接続元設定：`config/aws-validation/config`、`credentials`、`login-cache/`。既存の個人用ブラウザログインプロファイルを使用する。グローバルの仕事用設定へ書き込まない。
- ローカル実行状態：`config/aws-validation/<stack>/`。変更セット要求、digest、単発タスクIDなど。AWSの原応答・認証応答をそのまま保存しない。
- 秘密値：管理用パスワードはチャットや引数へ渡さない。`put-secrets`用の一時JSONは同じ無視対象ディレクトリ内へ0600で保存し、`appPassword`、`migrationPassword`、`adminHash`だけを持たせる。終了後は不要なコピーを削除する。

DB用パスワードは異なる24文字以上、管理者ハッシュはGoの `cmd/password` で生成する。通常用・移行用のDSNはRDS出力から組み立て、SSM SecureStringへ新規登録する。既存の同名パラメーターがあれば上書きせず停止する。途中失敗では一部だけ作成済みになり得るため、再実行前に所有と作成状態を確認する。

AWS CLIはPATH、`--cli`、または `AWS_VALIDATION_CLI` で指定する。CLI 2.32.0以上、Python 3.10以上、Docker（ARM64ビルド可能な環境）が前提。例のアカウント000000000000やMySQLの仮値ではAWSへ接続できない。

## オフラインの確認

リポジトリのルートから実行する。

```sh
python3 scripts/aws/validation.py --config infra/aws/validation-environment.example.json plan
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/aws -p 'test_*.py' -v
```

planとテストはAWS CLI・Docker・ネットワークを呼ばない。テストでは対象不一致、変更セットの削除/置換、DB単発タスクの失敗・旧版、秘密値上書き等を偽の応答で確認する。実AWSでの成立性やIAM権限の検査を代替しない。

## 接続と読み取り確認

以下は実AWSへアクセスする。費用が生じる配置・更新は対象と見積もりを確認したうえで実施する。

接続元のブラウザログインには、専用設定のパスを明示する。

```sh
AWS_CONFIG_FILE="$PWD/config/aws-validation/config" \
AWS_SHARED_CREDENTIALS_FILE="$PWD/config/aws-validation/credentials" \
AWS_LOGIN_CACHE_DIRECTORY="$PWD/config/aws-validation/login-cache" \
aws login --profile course-k-blog --region us-west-2

python3 scripts/aws-validation-preflight.py --config config/aws-validation/environment.json
```

プロファイルは環境設定に指定したものを使用する。共通セッション処理は指定アカウントの `course-k-validation-operator` をAssumeRoleし、取得した短期資格情報はメモリー内だけで扱う。継承したキー・仕事用プロファイル・接続先上書きを使わず、STSでアカウントとロールを照合する。

preflightはAZ、指定MySQL版、micro/gp3 20GiB、Fargate枠、CloudFormation構文を読み取り確認する。設定したDB版が提供されなければ、別版へ自動変更せず停止する。枠の総量は実配置の成功や空き容量を保証しない。

## 配置・更新の順序

`validation.py` のAWS変更コマンドは `--apply` を付けたときだけ動く。これは操作ミス防止用のスイッチで、費用や権限変更のユーザー承認を代替しない。`prepare` もAWSへ変更セットを作る書き込み操作である。

共通の指定は `python3 scripts/aws/validation.py --config config/aws-validation/environment.json`。各行に以下の引数を付ける。名前は操作ごとに一意な `validation-*` を明示し、実行前にinspectする。

| 順序 | コマンド・操作 | 次へ進む条件 |
|---|---|---|
| 1 | `--apply --stage foundation --name validation-foundation prepare` | 初期値はアプリとDBツール無効。作成する資源・IAM・費用を確認する。この段階でもRDS等に課金する |
| 2 | `--name validation-foundation inspect` → `--apply --name validation-foundation execute` | 状態を `status` で確認しCREATE_COMPLETEになる。executeは削除や置換の可能性がある変更を拒否する |
| 3 | ローカルでNode・Go・DB初期化イメージをLinux ARM64としてビルド → `--apply upload-images` | 専用ECR3件へ登録し、digestをローカル状態に保存。作業用Docker認証は一時ディレクトリだけで扱う |
| 4 | `--apply --secrets-file config/aws-validation/secrets.json put-secrets` | 通常/移行パスワード・DSN・管理者ハッシュをSSMへ登録。ローカル一時ファイルを削除する |
| 5 | `--apply --stage tools --name validation-tools prepare` → inspect → execute | digest固定とSSM存在を確認し、DBツール定義を作成。UPDATE_COMPLETEを確認 |
| 6 | `--apply --step bootstrap run-task` → `--step bootstrap task-status` | 現スタック・現タスク定義の全コンテナが停止、終了コード0。成功前に移行へ進まない |
| 7 | `--apply --step migration run-task` → `--step migration task-status` | 同様に移行成功を確認。タスクの要求に冪等トークンを使い、自動の再初期化はしない |
| 8 | `--apply --stage runtime --name validation-runtime prepare` → inspect → execute | 初期化・移行の現行定義での成功記録が必要。UPDATE_COMPLETEと正常性を確認 |
| 9 | 下記の仮記事試験と必要な監視確認 | HTTP試験と実ブラウザ操作を区別する。通知先と到達は別途確認する |

イメージ例に対応するビルド：

```sh
docker build --platform linux/arm64 -t course-k-web:validation frontend
docker build --platform linux/arm64 -t course-k-api:validation backend
docker build --platform linux/arm64 -f infra/database/Dockerfile -t course-k-bootstrap:validation .
```

DB初期化は新規検証DB専用で、既存本番DBへ実行しない。ツールはアプリ稼働中の初期化・移行と同じ初期化の反復を止める。通常の本番スキーマ更新フローは未整備。失敗した場合はECSの状態と原因を確認してから再実行方法を決め、ローカルの状態記録を削除して機械的に再実行しない。

## 仮記事によるHTTP確認

```sh
python3 scripts/aws/smoke.py --config config/aws-validation/environment.json --apply
```

対象は指定検証スタックから得たオレゴンの生成APIエンドポイントのみ。管理者パスワードを端末で入力する。記事作成・公開・下書き保存・更新・非公開、競合、Cookie、Origin、SSR・静的配信を確認する。リダイレクトを追わず、認証情報を別の接続先へ送らない。
仮記事は最後に非公開の下書きとして残る。途中失敗時は公開状態が残る可能性があるため、試験後に管理画面またはAPIで確認する。実ブラウザの編集操作や本番データの検証ではない。今回の整理ではこの実AWS試験は実行していない。

## 撤去

データを消す操作を汎用の自動スクリプトへ移植しない。検証用の仮データだけか、保持すべき記事・画像があるかを確認してから以下を行う。

1. 対象アカウント・地域・スタックID、保存対象と資源一覧を照合する。スタック以外の資源を巻き込まない。
2. 必要な保存・復元先を確認し、Node / Goのサービスを停止する。停止だけでDBや保存物の課金は止まらない。
3. CloudFormationでRDS削除保護を解除する変更セットを作り、DB置換がないことを確認して適用する。
4. 最終スナップショットの作成と保持方針を確認し、スタック削除を実行する。反映済み操作者ポリシーには削除前スナップショット作成権限の不足があるため、管理者による操作か候補ポリシーの明示的な採用・反映が必要。
5. RetainされたS3・ECR・ログ、専用SSM、手動スナップショットと保持バックアップを個別に照合する。保存データの削除は対象と不可逆性を確認した範囲で行う。
6. CloudFormationの完了だけでなく、残存するDB・保存物と費用の継続を確認する。IAMアクセス設定は別管理で、自動削除しない。

候補ポリシーの適用やデータ削除を自動実行するコマンドは提供しない。本番配備・復旧・通知の到達、完成機能を含む容量と不良版ロールバックは公開台帳R3/R5/R6の残件。
