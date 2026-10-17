# 東京とバージニア北部：同条件の費用比較

2026-10-18。リージョン変更の指示ではなく比較調査。AWS資源・認証プロファイル・権限テンプレートは変更していない。東京前提だったこれまでの試算を、構成を維持して比較する。

## 結果

| 月額・税別USD | 東京 ap-northeast-1 | バージニア北部 us-east-1 |
|---|---:|---:|
| Fargate Node：0.5vCPU / 1GiB | 17.99 | 14.42 |
| Fargate Go：0.25vCPU / 1GiB | 10.61 | 8.51 |
| RDS PostgreSQL db.t4g.micro | 18.25 | 11.68 |
| DB gp3：20GiB | 2.76 | 2.30 |
| 公開IPv4：2個 | 7.30 | 7.30 |
| ログ・画像保存・入口・DNS等 | 4.05 | 3.75 |
| **合計** | **60.96** | **47.96** |

非丸め値で月13.001925ドル、約21.33%減、年156.0231ドル減。主要な差はFargate合計5.673925ドルとRDS計算6.57ドル。

この2地域の比較ではバージニア北部が安い。全リージョン・全サービスの最安を証明した調査ではない。S3リクエスト単価のようにバージニア北部のほうが若干高い項目もある。

## 固定した条件

- 月730時間、ARM64 LinuxのFargateを2タスク常駐。Spot・Savings Plans・期間限定クレジットは使用しない。
- Node/GoのCPU・RAMは同じ。単価を安く見せるために性能要件を縮小していない。AWSでの容量はまだ未検証。
- DBは単一AZのRDS PostgreSQL通常サポート。db.t4g.micro、gp3 20GiB。
- API・アプリ・DB・保存領域はすべて同じリージョン。東京と米国間でDBだけを分ける案ではない。
- S3保存計6GiB、PUT等1万回、GET等10万回、ECR計2GiB、HTTP API10万回、Cloud Map discovery10万回、登録1,000回、2資源。
- ログ取込1GiB・保存1GiB、標準アラーム8個、RDS管理秘密値1個とAPI1,000回。
- 既存試算との純粋比較のため、公開+非公開DNSゾーン計1ドル、DNS0.04ドル、AZ通信予備0.10ドル、疎通/通知予備0.05ドルを両地域へ同額計上。予備費は実測額や地域別単価の証拠ではない。
- 今回の独自ドメインなし検証は公開DNSゾーンを作らないので、その検証環境だけの実請求額とは異なる。AliasのDNS問合せやCloudWatch等の無料枠も、この比較では追加控除しない。
- 外向き30GiBはアカウント共通の月100GB無料枠が残る前提。無料枠を使い切っている場合、東京+3.42ドル→64.38ドル、米国+2.70ドル→50.66ドル。
- 税・為替・ドメイン更新・有料CI、更新時の追加タスク、CPUクレジット超過、予備を超える通信・保存・バックアップ等は別。

## 選定への意味

費用を優先するなら、バージニア北部を有力候補とする根拠が得られた。日本からの閲覧・編集では東京よりネットワーク遅延が増える可能性があるが、本アプリでの遅延は未測定。遅延を具体的なミリ秒や体感で断定しない。

AWS Well-Architectedも地域差を比較し、遅延やデータ所在地等の必要性がなければ低価格な地域を選ぶ観点を示している。日本語のブログだから必ず東京を選ぶという要件はない。

現在の課金承認・アクセス設定資料は東京用。地域は未変更。変更を採用する場合は、配備/権限テンプレート中の地域・SSM ARN・KMS ViaService条件等を揃えて変更し、米国側のサービス利用枠とRDSバージョンも確認する。前の東京用ファイルをそのまま別地域に流用しない。

## 証拠・再計算

- [取得単価と原本URL・SHA256](aws-region-price-evidence-2026-10-18.json)：両地域の公式公開Price Listを同日に実取得。認証不要。
- [計算内訳](aws-region-comparison-2026-10-18.json)：合計は丸め前の各項目を合算。
- [以前の東京明細](aws-two-task-costs-2026-10-17.json)：今回の東京再取得でも60.961575ドルと一致。
- [Fargate公式料金](https://aws.amazon.com/fargate/pricing/)
- [RDS PostgreSQL公式料金](https://aws.amazon.com/rds/postgresql/pricing/)
- [公開IPv4公式料金](https://aws.amazon.com/vpc/pricing/)
- [データ転送無料枠](https://aws.amazon.com/ec2/pricing/on-demand/)
- [Route 53公式料金](https://aws.amazon.com/route53/pricing/)
- [AWS Well-Architected：地域別費用の選択](https://docs.aws.amazon.com/wellarchitected/latest/framework/cost_pricing_model_region_cost.html)

LLMは公式料金の取得・同条件での再計算・差分整理に利用。地域決定、実測性能、稼働中の環境変更が完了したとは記録しない。
