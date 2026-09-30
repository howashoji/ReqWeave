package updater

import "fmt"

// Kind は更新処理の失敗種別。画面表示は
// この種別ごとに「原因＋次に取る行動」の日本語 1 文を割り当てる。
type Kind string

const (
	// KindMalformed は更新情報を形式として読み取れない。
	KindMalformed Kind = "malformed"
	// KindUnsupportedSchema は更新情報の形式版に本アプリが対応していない。
	KindUnsupportedSchema Kind = "unsupported_schema"
	// KindUntrusted は署名が信頼できない（鍵不一致・改ざん）。
	KindUntrusted Kind = "untrusted"
	// KindNoTrustedKeys はアプリに信頼公開鍵が 1 つも埋め込まれていない。
	// 改ざんの疑い（KindUntrusted）とは原因も次の行動も異なるため分けて扱う。
	KindNoTrustedKeys Kind = "no_trusted_keys"
	// KindHashMismatch は配布物の SHA-256 がマニフェスト記載値と一致しない。
	KindHashMismatch Kind = "hash_mismatch"
	// KindSizeMismatch は配布物のサイズがマニフェスト記載値と一致しない。
	KindSizeMismatch Kind = "size_mismatch"
	// KindNoAsset は現在の OS / アーキテクチャ向けの配布物が無い。
	KindNoAsset Kind = "no_asset"
	// KindNetwork は取得に失敗した（通信断・HTTP エラー・タイムアウト）。
	KindNetwork Kind = "network"
	// KindNotUserArea は置換対象がユーザー領域の外にある、または作業場所を用意できない。
	// 管理者権限を要する場所へは書き込まない。
	KindNotUserArea Kind = "not_user_area"
)

// Error は更新処理の失敗。
//
// Message は利用者向けの 1 文（「原因＋次に取る行動」の原因部分）であり、
// **URL・ハッシュ・鍵素材・内部の生エラー文字列を含めない**（シークレットキーを画面・ログに出さないのと同じ方針）。
// 原因調査に要る詳細は cause 側に置き、Unwrap でのみ辿れるようにする。
type Error struct {
	Kind  Kind
	msg   string
	cause error
}

// Error は利用者向けの 1 文を返す。
func (e *Error) Error() string { return e.msg }

// Message は利用者向けの 1 文を返す（Error と同じ。呼び出し側の意図を明示するための別名）。
func (e *Error) Message() string { return e.msg }

// Unwrap は原因の誤りを返す（診断用。利用者向け表示には使わない）。
func (e *Error) Unwrap() error { return e.cause }

// Detail は診断用の文字列を返す。利用者向け画面には出さない。
func (e *Error) Detail() string {
	if e.cause == nil {
		return string(e.Kind)
	}
	return fmt.Sprintf("%s: %v", e.Kind, e.cause)
}

// KindOf は err が *Error ならその種別を返す。
func KindOf(err error) (Kind, bool) {
	if e, ok := err.(*Error); ok {
		return e.Kind, true
	}
	return "", false
}

// NewErrorForTest は指定した種別の代表的な誤りを作る。
//
// 利用者向け文言の写像（バインディング側）を、
// 実際の失敗を起こさずに検証するために使う。
func NewErrorForTest(kind Kind) *Error {
	msgs := map[Kind]string{
		KindMalformed:         "更新情報を読み取れないため、現行版のまま更新を中止しました。時間をおいて再実行してください",
		KindUnsupportedSchema: "更新情報の形式に対応していないため、現行版のまま更新を中止しました。配布元から最新版を入手してください",
		KindUntrusted:         "更新ファイルの発行元を確認できないため、現行版のまま更新を中止しました。時間をおいて再実行してください",
		KindNoTrustedKeys:     "このアプリでは更新を確認できません。配布元から最新版を入手してください",
		KindHashMismatch:      "更新ファイルが壊れているため、現行版のまま更新を中止しました。時間をおいて再実行してください",
		KindSizeMismatch:      "更新ファイルが壊れているため、現行版のまま更新を中止しました。時間をおいて再実行してください",
		KindNoAsset:           "この環境向けの更新が公開されていません。配布元の案内を確認してください",
		KindNetwork:           "更新の確認に失敗しました。通信を確認して再実行してください",
		KindNotUserArea:       "このアプリの場所では自動更新できません。配布元から最新版を入手してください",
	}
	msg, ok := msgs[kind]
	if !ok {
		msg = "更新に失敗しました。しばらく待って再実行してください"
	}
	return &Error{Kind: kind, msg: msg}
}
