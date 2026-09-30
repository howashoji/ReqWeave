package sync

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// 失敗の分類と利用者への提示（文言はエラーの文言の一覧の「同期系」の様式）。
//
// git の生のエラー文言・終了コードを画面へ出さない。Detail は折りたたみ表示用で、
// マスキング済み。いずれの失敗でも**作業コピーの内容が変わらない旨を明示**する。

// FailureKind は失敗種別（同期系の 7 種 + 本モジュール固有の失敗）。
type FailureKind string

const (
	FailUnreachable      FailureKind = "unreachable"
	FailAuth             FailureKind = "auth_failed"
	FailForbidden        FailureKind = "forbidden"
	FailNotFound         FailureKind = "not_found"
	FailNonFastForward   FailureKind = "non_fast_forward"
	FailIDRangeExhausted FailureKind = "id_range_exhausted"
	FailCanceled         FailureKind = "canceled"

	// FailUnavailable は git が無く同期を利用できない。
	FailUnavailable FailureKind = "unavailable"
	// FailNotMember は取得したプロジェクトのメンバーに自分が無い（メンバーでない利用者は参加できない）。
	FailNotMember FailureKind = "not_member"
	// FailAlreadyPresent は同一プロジェクトの作業コピーが既にこの端末にある。
	FailAlreadyPresent FailureKind = "already_present"
	// FailRestoreUnconfirmed は復元した作業コピーの反映を利用者が確認していない
	// （自動退避から復元した内容を、無確認で同期先へ書かない）。中止であって失敗ではない。
	FailRestoreUnconfirmed FailureKind = "restore_unconfirmed"
	// FailInternal は分類できない失敗（作業コピーは復帰点へ戻してある）。
	FailInternal FailureKind = "internal"
)

// failureLabels は失敗種別の表示名（生のコード値・git のエラー文言を出さない）。
var failureLabels = map[FailureKind]string{
	FailUnreachable:        "同期先に接続できなかった",
	FailAuth:               "認証できなかった",
	FailForbidden:          "同期先の権限が足りなかった",
	FailNotFound:           "同期先にプロジェクトが無かった",
	FailNonFastForward:     "先に取り込みが必要だった",
	FailIDRangeExhausted:   "番号帯を使い切っていた",
	FailCanceled:           "中止した",
	FailUnavailable:        "同期に必要なプログラムが無かった",
	FailNotMember:          "メンバーに登録されていなかった",
	FailAlreadyPresent:     "同じプロジェクトの作業コピーが既にあった",
	FailRestoreUnconfirmed: "復元した内容の確認が済んでいなかった",
	FailInternal:           "原因を特定できない失敗",
}

// Label は失敗種別の表示名を返す（空の種別 = 失敗していない場合は空文字）。
func (k FailureKind) Label() string {
	if k == "" {
		return ""
	}
	if label, ok := failureLabels[k]; ok {
		return label
	}
	return failureLabels[FailInternal]
}

// Operation は同期の操作種別（同期の記録の op）。
type Operation string

const (
	OpClone       Operation = "clone"
	OpIncorporate Operation = "incorporate"
	OpPublish     Operation = "publish"
	// OpCheck は接続確認（記録の対象外）。
	OpCheck Operation = "check"
)

// opLabels は操作の表示名（画面・記録に git の語を出さない）。
var opLabels = map[Operation]string{
	OpClone:       "取得",
	OpIncorporate: "取り込み",
	OpPublish:     "反映",
	OpCheck:       "接続確認",
}

// Label は操作の表示名を返す。
func (o Operation) Label() string {
	if l, ok := opLabels[o]; ok {
		return l
	}
	return "同期"
}

// Failure は同期の失敗。error として返し、呼び出し側は errors.As で取り出す。
type Failure struct {
	Kind FailureKind `json:"kind"`
	Op   Operation   `json:"op"`
	// Message は利用者向けの文言（原因＋次に取る行動＋作業コピーが変わらない旨）。
	Message string `json:"message"`
	// Detail は折りたたみ表示用の詳細（マスキング済み。空のこともある）。
	Detail string `json:"detail,omitempty"`
}

func (f *Failure) Error() string { return f.Message }

// AsFailure は err から Failure を取り出す。
func AsFailure(err error) (*Failure, bool) {
	var f *Failure
	if errors.As(err, &f) {
		return f, true
	}
	return nil, false
}

// unchangedNote は失敗時に必ず添える一文（作業コピーが変わらないことを伝える）。
func unchangedNote(op Operation) string {
	switch op {
	case OpClone:
		return "この端末には何も作られていません。"
	default:
		return "作業コピーの内容は変わっていません。"
	}
}

// newFailure は種別に応じた利用者向け文言を組み立てる。
func newFailure(kind FailureKind, op Operation, detail string) *Failure {
	var msg string
	switch kind {
	case FailUnreachable:
		msg = fmt.Sprintf("同期先に接続できません。接続が回復してから、もう一度%sしてください。作業はこのまま続けられます。", op.Label())
	case FailAuth:
		msg = "同期先の認証に失敗しました。設定で認証情報を登録し直してください。"
	case FailForbidden:
		msg = "同期先への書き込みが許可されていません。同期先の管理者へ確認してください。"
	case FailNotFound:
		msg = "同期先にこのプロジェクトが見つかりません。所在を確認するか、オーナーが最初の反映を行ってください。"
	case FailNonFastForward:
		msg = "先に取り込みが必要です。取り込んでから、もう一度反映してください。"
	case FailIDRangeExhausted:
		msg = "新しい番号を作れなくなりました。同期すると続けられます。"
	case FailCanceled:
		msg = fmt.Sprintf("%sを中止しました。", op.Label())
	case FailUnavailable:
		msg = "同期に必要なプログラム（git）がこの端末にありません。git を導入してから、もう一度実行してください。同期以外の機能はそのまま使えます。"
	case FailNotMember:
		msg = "このプロジェクトのメンバーに登録されていません。オーナーに登録を依頼してください。"
	case FailAlreadyPresent:
		msg = "このプロジェクトの作業コピーは既にこの端末にあります。取得ではなく取り込みを実行してください。"
	case FailRestoreUnconfirmed:
		msg = "この作業コピーはバックアップから復元されています。同期先へ載せる内容を確認してから、あらためて反映してください。"
	default:
		msg = fmt.Sprintf("%sに失敗しました。もう一度実行しても解決しない場合は詳細を確認してください。", op.Label())
	}
	return &Failure{Kind: kind, Op: op, Message: msg + unchangedNote(op), Detail: strings.TrimSpace(detail)}
}

// 失敗分類のパターン（LC_ALL=C で固定した git の英語出力に対して照合する）。
var (
	nonFFPatterns = regexp.MustCompile(`(?i)non-fast-forward|fetch first|\[rejected\]|failed to push some refs|stale info`)
	authPatterns  = regexp.MustCompile(`(?i)authentication failed|permission denied \(publickey|could not read username|could not read password|invalid username or password|invalid credentials|host key verification failed|no such identity|401|unable to load private key|libcrypto|incorrect passphrase`)
	forbidPattern = regexp.MustCompile(`(?i)\b403\b|permission denied|permission to .* denied|write access|read-only|read only|pre-receive hook declined|insufficient permission|not authorized|access denied|EACCES`)
	notFoundPat   = regexp.MustCompile(`(?i)does not appear to be a git repository|repository not found|repository '.*' not found|\b404\b|not a git repository|no such file or directory`)
	unreachPat    = regexp.MustCompile(`(?i)could not resolve host|unable to access|connection refused|connection timed out|timed out|network is unreachable|no route to host|ssh: connect to host|failed to connect|temporary failure in name resolution|could not read from remote repository|input/output error|connection reset|early eof|remote end hung up|the remote end hung up|broken pipe`)
)

// classifyGitError は git の失敗出力から失敗種別を推定する。判定順は限定的なものから広いものへ。
func classifyGitError(err error) FailureKind {
	var ge *gitError
	if !errors.As(err, &ge) {
		return FailInternal
	}
	s := ge.stderr
	switch {
	case nonFFPatterns.MatchString(s):
		return FailNonFastForward
	case authPatterns.MatchString(s):
		return FailAuth
	case forbidPattern.MatchString(s):
		return FailForbidden
	case notFoundPat.MatchString(s):
		return FailNotFound
	case unreachPat.MatchString(s):
		return FailUnreachable
	default:
		return FailInternal
	}
}

// failureFrom は git 等のエラーを Failure へ変換する（既に Failure ならそのまま）。
func failureFrom(op Operation, err error) *Failure {
	if f, ok := AsFailure(err); ok {
		return f
	}
	if errors.Is(err, errGitUnavailable) {
		return newFailure(FailUnavailable, op, "")
	}
	if isContextError(err) {
		return newFailure(FailCanceled, op, "")
	}
	var ge *gitError
	if errors.As(err, &ge) {
		return newFailure(classifyGitError(err), op, ge.Error())
	}
	return newFailure(FailInternal, op, err.Error())
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
