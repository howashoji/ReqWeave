// Package auditlog は監査ログ。AI 送信記録・変更履歴・トークン実績の記録と利用量集計を担う。
//
// 記録はプロジェクトフォルダの audit/ 配下へ月別 × 作業者別の NDJSON で追記し、書き換えない。
package auditlog
