# RDS公開CAバンドル

AWS公式配布元 https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem から2026年10月17日に取得した公開証明書です。秘密鍵・認証情報ではありません。

保存名 `rds-global-bundle.crt`、SHA-256 `90a6f1a7978a1b1b096dca6c57511ca48d3b8cf052f8e704baf358f34ea2f21b`。

Goイメージの `DATABASE_CA_FILE` で指定し、MySQLドライバーのTLS設定へ読み込みます。本番のアプリ用URLは `mysql://`、`tls=verify-full` とRDSエンドポイントのホスト名を使います。ドライバーは信頼CAと接続先ホスト名の両方を検証します。`sslrootcert` をURLへ明示する場合はそのファイルが優先されます。CA供給は実RDSのTLS接続試験の代わりではありません。

AWS CAの更新時は公式配布物と変更内容を確認して差し替え、ハッシュ・取得日・対象リージョンを記録し、イメージを再ビルドします。ホストの検証や署名検証を無効化して接続を通しません。ローカルComposeの開発DBは引き続きtls=falseです。
