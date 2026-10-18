# 文書索引

## 保存場所と読む順序

```text
docs/
├─ README.md                              文書の入口
├─ current-design.md                      現行仕様・設計理由
├─ ui-ux-decisions-current.md              採用UI
├─ blog-publication-roadmap-2026-10-18.md   進捗・整理計画の唯一の台帳
├─ repository-organization.md             保存ルール・構成図
├─ history/                               過去の判断・調査・実装・検証記録（25件）
└─ evidence/                              記録を裏付けるJSON・CSV・画像等（24件）
```

現在地は公開台帳、実装する仕様は現行設計とUI採用事項から読む。履歴は必要な判断の根拠をたどるときに開く。旧URLや過去コミット内のパスは当時の配置を示し、現在の参照先はこの索引とする。

## 正本を役割で分ける

| 用途 | 読む文書 | 更新する内容 |
|---|---|---|
| 進捗・残作業・次のチェックポイント | [公開までの進捗台帳](blog-publication-roadmap-2026-10-18.md) | 状態、完了証拠、依存、未決事項。進捗管理はこの1本 |
| 要件・API・DB・アーキテクチャ | [現行設計](current-design.md) | 仕様と責務、採用／提案の区別。作業台帳を複製しない |
| UIの採用仕様 | [採用事項](ui-ux-decisions-current.md) | 本人が選んだ配置・文字組み・配色。実装の進捗は台帳へ |
| リポジトリ内の保存場所と量 | [構成・棚卸し](repository-organization.md) | 保存境界、ブランチ差、整理の判断理由 |

README・設計書・履歴に別の「現在地」が残っていても、進捗は台帳を優先する。日付付きの実行記録は、その日に何を確認したかの証拠であり、独立した現在の作業計画ではない。

## 実装・検証の証拠

- [AWS一時配備・HTTP31項目・再配置・撤去](history/aws-validation-execution-2026-10-18.md)／[機械可読の選択済み証拠](evidence/aws-validation-evidence-2026-10-18.json)
- [採用UIの受領とバックエンド接続確認](history/ui-ux-backend-integration-review-2026-10-18.md)
- [初期のローカル基本機能](history/blog-prototype-progress-2026-10-15.md)
- [配備準備の実装](history/aws-readiness-implementation-2026-10-17.md)／[ローカル負荷・TLS確認](history/aws-validation-ready-2026-10-18.md)
- [ブラウザの拒否・調査記録](history/browser-access-investigation.md)

採用UIの[引き継ぎ](history/ui-ux-review-handoff-2026-10-18.md)、[実装・画面画像・固定API試験](history/ui-ux-implementation-2026-10-18.md)はmainへ統合済み。ブランチ別の文書をコピーして正本を二重管理せず、進捗は公開台帳へ記録する。

## 比較・判断の履歴

| 分野 | 主な資料 |
|---|---|
| AWS実行基盤 | [ECS＋Fargate採用](history/ecs-fargate-decision-2026-10-15.md)、[初期配置比較](history/ecs-fargate-deployment-design-2026-10-15.md) |
| 入口・タスク分離 | [振り分けの比較](history/ingress-comparison-2026-10-17.md)、[2タスク検証計画](history/aws-two-task-validation.md)、[旧配備前レビュー](history/aws-deployment-review.md) |
| AWS地域・料金 | [広域比較](history/aws-regions-wide-comparison-2026-10-18.md)、[東京とバージニア](history/aws-region-comparison-2026-10-18.md)、[米国内候補](history/aws-region-candidates-2026-10-18.md)、[遅延調査](history/aws-region-latency-research-2026-10-18.md) |
| アクセス設定 | [設定・修正・管理者操作の経緯](history/aws-access-setup-2026-10-18.md) |
| 外部記事 | [2026-10-15の取得調査](history/external-integration-verification-2026-10-15.md) |
| UI | [設計・比較](history/ui-ux-design.md)、[採用基準](history/ui-ux-adoption-criteria.md)、[API相談事項](history/ui-ux-api-needs.md)、[担当への初期指示](history/ui-ux-handoff-2026-10-18.md) |
| 初期計画 | [初期仕様案](history/blog-cp1-specification-2026-10-15.md)、[旧タスク台帳](history/blog-current-taskboard-2026-10-15.md) |

`evidence/`内の料金JSON・CSVや画面画像 は参照資料に付随する証拠。個別取得時の単価・画面であり、現在の本番費用や全機能の完成を表すものではない。

## 文書を増やすとき

現行資料は直下、判断・調査・実行記録は `history/`、選別した機械可読データや画像は `evidence/` へ置く。日付だけを理由に現行の台帳を履歴へ移さない。

通常の作業進捗だけで新しい台帳を作らず、公開台帳の該当項目を更新する。設計変更は現行設計、主要な比較・失敗・検証は対象を絞った記録へ残し、台帳からリンクする。秘密値・生の認証応答・未整理の巨大ログはコミットしない。
