package binding

// 本ファイルは、エラーの各分類について決めた「ログに記録する内容」を動作ログへ残す口。
//
// **表示文言と記録は別物**である。画面へ出すのは「原因＋次の行動」の 1 文で、
// ここで残すのは**後から切り分けるための事実だけ**。分類ごとに決めた項目だけを残し、
// **それ以外の項目を足さない**（動作ログに記録しない情報 = 本文・氏名・パス・
// パスコード・キー・認証情報）。
//
// 分類ごとに 1 事象名を持つ（AI 通信系 = `ai.call_failed` / 同期系 = `sync.failed` /
// 更新系 = `update.check_failed` は既存。本ファイルは残る 5 分類を受け持つ）。
// 事象名は動作ログの allowlist と揃える。増やすときは allowlist と同時に動かす。

import (
	"errors"
	"strconv"

	"github.com/howashoji/ReqWeave/app/internal/applog"
	"github.com/howashoji/ReqWeave/app/internal/exchange"
)

// 事象名（分類ごとに 1 つ）。
const (
	eventUsageBlocked   = "usage.blocked"       // 利用量上限系
	eventExchangeFailed = "exchange.failed"     // 受け渡し系
	eventImportFailed   = "import.failed"       // 取り込み系
	eventCollabDenied   = "collab.denied"       // 共同作業系
	eventProjectData    = "project.data_failed" // データ系
)

// 受け渡し系の操作の別（どちら側で起きたか）。
const (
	exchangeOpOpenIssue      = "open_issue"      // 回答モードで質問票を開く
	exchangeOpValidateReturn = "validate_return" // 担当者側で返送ファイルを検証する
)

// 受け渡し系の失敗種別（動作ログの中だけで使う区分。画面へは出さない）。
const (
	exchangeKindPasscode   = "passcode_mismatch" // パスコード不一致（**試した値は記録しない**）
	exchangeKindNotIssue   = "not_issue_file"    // 質問票（発行用）ではないファイル
	exchangeKindUnreadable = "unreadable"        // 形式不正・版非互換・読み取り不能
)

// recordExchangeFailure は受け渡し系の失敗を残す（ファイル形式版・失敗種別＋質問票 ID）。
//
// **ファイルパス・ファイル名・パスコードは残さない**（パスコードはどこにも保存しない方針のため）。質問票 ID と形式版は
// 平文メタデータ（コンテナの `manifest.yaml`）から取れる範囲でだけ添える。取れない場合
// （そもそもコンテナとして読めない場合）は種別だけを残す — **空文字で埋めない**。
func (a *API) recordExchangeFailure(op, src string, err error) {
	if err == nil {
		return
	}
	fields := []applog.Field{
		applog.F("op", op),
		applog.F("kind", exchangeFailureKind(err)),
	}
	// 平文メタデータだけを読み直す（復号しない。読めなければ添えない）。
	if container, readErr := exchange.ReadContainer(src); readErr == nil {
		if id := container.Manifest.QuestionnaireID; id != "" {
			fields = append(fields, applog.F("questionnaire_id", id))
		}
		if v := container.Manifest.ExchangeFormatVersion; v != "" {
			fields = append(fields, applog.F("format_version", v))
		}
	}
	a.log.Warn(eventExchangeFailed, "受け渡しファイルを扱えませんでした", fields...)
}

// exchangeFailureKind は失敗を記録用の区分へ写す（利用者向け文言はエラーそのものが持つ）。
func exchangeFailureKind(err error) string {
	switch {
	case errors.Is(err, exchange.ErrPasscodeMismatch):
		return exchangeKindPasscode
	case errors.Is(err, exchange.ErrNotIssueFile):
		return exchangeKindNotIssue
	default:
		return exchangeKindUnreadable
	}
}

// 取り込み系の失敗種別。
const (
	importKindNotExtractable = "not_extractable"    // テキストを取り出せない（原本は保持する）
	importKindUnsupported    = "unsupported_format" // 対象外の形式（取り込める形式は限定している）
	importKindTooLarge       = "too_large"          // 分割上限の超過
)

// recordImportFailure は取り込み系の失敗を残す（資料 ID・失敗種別・検出形式）。
//
// **資料の本文・ファイル名・元のパスは残さない**（資料 ID から原本へたどれるため不要）。
// 値が無い項目（対象外形式では資料 ID がまだ無い）は**空で埋めずに落とす**。
func (a *API) recordImportFailure(importID, kind, format string) {
	fields := []applog.Field{applog.F("kind", kind)}
	if importID != "" {
		fields = append(fields, applog.F("import_id", importID))
	}
	if format != "" {
		fields = append(fields, applog.F("format", format))
	}
	a.log.Warn(eventImportFailed, "資料を取り込み・分析できませんでした", fields...)
}

// 共同作業系の失敗種別。
const (
	collabKindNotMember = "not_member"  // メンバー外
	collabKindRole      = "role"        // 権限不足
	collabKindBusy      = "window_busy" // 同一端末の別ウィンドウが処理中
	collabKindReserved  = "reserved"    // 予約された対象への着手（禁止はしない）
)

// recordCollabDenied は共同作業系の拒否を残す（対象・失敗種別・予約者の author_id）。
//
// **利用者 ID は予約者のものだけ**を残す（予約の衝突は「誰と重なったか」が分からないと追えないため。
// 記録する項目として明示的に決めたもの）。それ以外の利用者 ID・メンバー一覧・パスは残さない。
func (a *API) recordCollabDenied(target, kind, reservedBy string) {
	fields := []applog.Field{
		applog.F("target", target),
		applog.F("kind", kind),
	}
	if reservedBy != "" {
		fields = append(fields, applog.F("reserved_by", reservedBy))
	}
	a.log.Warn(eventCollabDenied, "共同作業の条件を満たさないため操作を進めませんでした", fields...)
}

// データ系の失敗種別。
const (
	dataKindUnreadable = "unreadable"      // 読込失敗（ファイルが無い・壊れている）
	dataKindMalformed  = "malformed"       // 形式不整合（解釈できない）
	dataKindTooNew     = "format_too_new"  // 自版より新しい形式（壊さないよう書き込まない）
	dataKindMigration  = "needs_migration" // 自版より古い形式（移行が要る）
	dataKindSaveFailed = "save_failed"     // 保存失敗（保存キューの流し切りで検出）
)

// recordProjectDataFailure はデータ系の失敗を残す（対象パス（プロジェクト内相対）・
// 失敗種別・整合性チェック結果）。
//
// **プロジェクト内の相対パスだけ**を残す（絶対パスは利用者名を含む端末固有情報であり、
// 動作ログに記録しない情報に当たる。同期系で Detail を落としているのと同じ理由）。
// 業務情報（対象システム名・要件本文）も残さない。
func (a *API) recordProjectDataFailure(relPath, kind, compat string) {
	fields := []applog.Field{applog.F("kind", kind)}
	if relPath != "" {
		fields = append(fields, applog.F("path", relPath))
	}
	if compat != "" {
		fields = append(fields, applog.F("compat", compat))
	}
	a.log.Warn(eventProjectData, "プロジェクトデータを扱えませんでした", fields...)
}

// recordUsageBlocked は利用量上限による開始拒否を残す（対象操作・累計消費・上限値）。
func (a *API) recordUsageBlocked(operation string, status UsageStatus) {
	limit := 0
	if status.LimitTokens != nil {
		limit = *status.LimitTokens
	}
	a.log.Warn(eventUsageBlocked, "AI の利用量が上限に達したため操作を開始しませんでした",
		applog.F("operation", operation),
		applog.F("consumed_tokens", strconv.Itoa(status.ConsumedTokens)),
		applog.F("limit_tokens", strconv.Itoa(limit)))
}
