package auditlog

import "github.com/howashoji/ReqWeave/app/internal/aiprovider"

// SendRecorder は AIプロバイダ抽象化層の送信記録の発行先（aiprovider.SendRecorder の実装）。
//
// 抽象化層は本型のインタフェースだけを知り、監査ログの形式を知らない。
type SendRecorder struct {
	log *Logger
	// onError は実績行の追記に失敗したときの通知先（nil なら通知しない）。
	// 送信自体は完了しているため、失敗しても対話は止めない。
	onError func(error)
}

// NewSendRecorder は Logger を送信記録の発行先として包む。
func NewSendRecorder(l *Logger, onError func(error)) *SendRecorder {
	return &SendRecorder{log: l, onError: onError}
}

// RecordSend は送信直前に送信行を追記し、送信 ID を返す（実績は後から実績行で追記する）。
//
// 受け取る値は aiprovider.SendRecord のフィールドのみで、認証ヘッダ・キー・キー参照名を
// 載せる経路を持たない。
func (r *SendRecorder) RecordSend(rec aiprovider.SendRecord) (string, error) {
	return r.log.RecordAISend(AISendRecord{
		Provider:   string(rec.Provider),
		Model:      rec.Model,
		Session:    rec.Session,
		Prompt:     rec.Prompt,
		Included:   rec.Included,
		ImportRefs: importRefs(rec.ImportRefs),
	})
}

// RecordUsage は送信 ID に紐づく実績行を追記する（応答を受け取った後）。
func (r *SendRecorder) RecordUsage(sendID string, usage aiprovider.TokenUsage) {
	err := r.log.RecordAIUsage(sendID, usage.InputTokens, usage.OutputTokens, usage.ReasoningTokens)
	if err != nil && r.onError != nil {
		r.onError(err)
	}
}

// importRefs は抽象化層の取り込み資料識別を監査レコードの import_refs へ写す
// （取り込み分析でどの資料を送ったかを残す）。空のときは nil を返し、キーごと省略させる。
func importRefs(refs []aiprovider.ImportRef) []ImportRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]ImportRef, 0, len(refs))
	for _, r := range refs {
		out = append(out, ImportRef{ID: r.ID, SourceName: r.SourceName, ImportedAt: r.ImportedAt})
	}
	return out
}
