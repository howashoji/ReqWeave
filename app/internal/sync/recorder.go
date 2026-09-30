package sync

import "time"

// 同期の記録（いつ・どこへ・何を同期したか）。保存（audit/sync-log/）は呼び出し側が渡す Recorder が行う。
// 本ファイルは記録の型と受け口を定める。**認証情報を含めない**（Remote.Location は Display 済み）。

// Record は同期の記録 1 件。
type Record struct {
	At     time.Time `json:"at"`
	Author string    `json:"author"`
	Op     Operation `json:"op"`
	// RemoteKind / RemoteLocation は同期先の種別と所在（資格情報部を除去した文字列）。
	RemoteKind     RemoteKind `json:"remote_kind"`
	RemoteLocation string     `json:"remote_location"`
	Summary        Summary    `json:"summary,omitempty"`
	// Conflicts は三面マージで解決した件数。
	Conflicts int         `json:"conflicts"`
	Result    Result      `json:"result"`
	Failure   FailureKind `json:"failure,omitempty"`
}

// Result は操作の結果。
type Result string

const (
	ResultOK       Result = "ok"
	ResultFailed   Result = "failed"
	ResultCanceled Result = "canceled"
)

// Recorder は同期の記録の受け口。root は作業コピーのパス（取得では取得先）。
type Recorder interface {
	RecordSync(root string, rec Record) error
}

// NopRecorder は何も記録しない（記録が要らないテスト・呼び出し向け）。
type NopRecorder struct{}

// RecordSync は何もしない。
func (NopRecorder) RecordSync(string, Record) error { return nil }

// resultOf は失敗種別から記録上の結果を決める。
func resultOf(f *Failure) (Result, FailureKind) {
	if f == nil {
		return ResultOK, ""
	}
	// 利用者の確認待ちで止めたものは「中止」として記録する（失敗ではない）。
	if f.Kind == FailCanceled || f.Kind == FailRestoreUnconfirmed {
		return ResultCanceled, f.Kind
	}
	return ResultFailed, f.Kind
}
