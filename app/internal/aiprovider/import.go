package aiprovider

// 本ファイルは取り込み分析の送信の同意ゲートを担う。
//
// 取り込み資料の本文を含むリクエストは、担当者の同意操作なしに送信されない。
// 判定は本層の唯一の送信入口である StreamRetrying に置き、アダプタの StreamMessage を
// 上位から直接呼ぶ経路は depcheck（consent-gate-bypass）で禁じる。
// この 2 つで「同意なしに資料が外部送信されない」を構造的に担保する。

import "time"

// ImportRef は取り込み分析の送信対象資料の識別（送信記録の import_refs）。
//
// 送信記録専用のメタデータであり、プロバイダへ送るリクエスト本体には含めない
// （アダプタは Model / System / Messages / Effort / ResponseSchema だけを写す）。
type ImportRef struct {
	ID         string    // IMP-nnn
	SourceName string    // 取り込み時の資料名（source_name）
	ImportedAt time.Time // 取り込み日時（UTC）
}

// consentRequiredMessage は同意ゲートで送信を止めたときの原因。
//
// 利用者向け文言はエラーカタログが Class × 発生源から作るため、
// ここでは何が起きたかだけを書く（ProviderError と同じ方針）。
const consentRequiredMessage = "取り込み資料を含む送信に同意操作がありません（同意前の資料は送信しません）"

// checkImportConsent は同意ゲート。
//
// ImportRefs が空でないリクエストは ConsentGiven が true でない限り送信しない。
// 恒久的エラー（再試行しても状況が変わらない）として区分する。
func checkImportConsent(req ChatRequest, provider ProviderID) *ProviderError {
	if len(req.ImportRefs) == 0 || req.ConsentGiven {
		return nil
	}
	return &ProviderError{
		Class:    ErrClassPermanent,
		Provider: provider,
		Message:  consentRequiredMessage,
	}
}
