# RDS公開CAバンドル

AWS公式配布元 https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem から2026年10月17日に取得した公開証明書です。秘密鍵・認証情報ではありません。

保存名 `rds-global-bundle.crt`、SHA-256 `90a6f1a7978a1b1b096dca6c57511ca48d3b8cf052f8e704baf358f34ea2f21b`。

Goイメージの `PGSSLROOTCERT` で指定します。pgxはlibpq互換環境変数を読み込みます。本番DSNは `sslmode=verify-full` とRDSエンドポイントのホスト名を使います。`sslrootcert` をDSNへ明示する場合はその値が優先されるため、誤ったファイルを指定しないよう配備時に確認します。CA供給は実RDSのTLS接続試験の代わりではありません。

AWS CAの更新時は公式配布物と変更内容を確認して差し替え、ハッシュ・取得日・対象リージョンを記録し、イメージを再ビルドします。ホストの検証や署名検証を無効化して接続を通しません。ローカルComposeの開発DBは引き続きsslmode=disableです。
