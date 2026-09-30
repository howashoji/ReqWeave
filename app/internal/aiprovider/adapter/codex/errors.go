package codex

// 本ファイルはエラーの正規化。分類は 3 つのまま増やさない（上位が他のプロバイダと同じに扱えるように）。
//
// 利用者向けの文言はここでは作らない（エラーカタログが Class × Code から作る）。
// `Message` には Codex の `message` を入れる（キー・トークンの形の文字列を含めない）。

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/masking"
)

// ProviderCodex は Codex App Server のプロバイダ ID。
const ProviderCodex = aiprovider.ProviderID("codex")

// 本アダプタ固有の Code（画面の文言はエラーカタログが持つ）。
const (
	// codeGuardFailed は起動後の検査の不合格。
	codeGuardFailed = "codex_guard_failed"
	// codeToolAttempt は道具の呼び出し・承認要求の検知。
	codeToolAttempt = "codex_tool_attempt"
	// codeUnexpected は本システムの使い方では起きないはずの状態。
	codeUnexpected = "codex_unexpected"
	// codeProcessExited は子プロセスの異常終了・標準入出力の切断。
	codeProcessExited = "codex_process_exited"
	// codeWorkspaceBusy は一時領域の排他が取れない状態（自動再試行の対象にしない）。
	codeWorkspaceBusy = "codex_workspace_busy"
	// codeInvalidSchema は構造化出力のスキーマを変換できない状態。
	codeInvalidSchema = "invalid_response_schema"
	// codeInvalidRequest は送る前に分かる入力不正。
	codeInvalidRequest = "invalid_request"
)

func permanentError(code, message string) *aiprovider.ProviderError {
	return &aiprovider.ProviderError{
		Class: aiprovider.ErrClassPermanent, Provider: ProviderCodex,
		Code: code, Message: masking.Mask(message),
	}
}

func transientError(code, message string) *aiprovider.ProviderError {
	return &aiprovider.ProviderError{
		Class: aiprovider.ErrClassTransient, Provider: ProviderCodex,
		Code: code, Message: masking.Mask(message),
	}
}

// workspaceBusyError は一時領域の排他を取れない状態。
//
// 分類は一時的だが、**自動再試行の対象にしない**（相手のウィンドウが閉じるまで解消せず、
// 再試行しても待ち時間が延びるだけのため）。再試行層には、待機の指示が
// 上限（RetryAfterMax）を超えるときに打ち切る規則があるので、それに載せて打ち切らせる。
func workspaceBusyError(message string) *aiprovider.ProviderError {
	err := transientError(codeWorkspaceBusy, message)
	err.RetryAfter = aiprovider.RetryAfterMax + time.Second
	return err
}

func configError(code, message string) *aiprovider.ProviderError {
	return &aiprovider.ProviderError{
		Class: aiprovider.ErrClassConfig, Provider: ProviderCodex,
		Code: code, Message: masking.Mask(message),
	}
}

// turnError は `error` 通知・`turn/completed`（failed）が持つ誤りの内容。
type turnError struct {
	Message        string          `json:"message"`
	CodexErrorInfo json.RawMessage `json:"codexErrorInfo"`
}

// normalizeTurnError は Codex の誤りを正規化エラーへ写す。
func normalizeTurnError(e *turnError) *aiprovider.ProviderError {
	if e == nil {
		return transientError(codeUnexpected, "Codex App Server が原因を示さずに失敗しました")
	}
	kind, status := parseCodexErrorInfo(e.CodexErrorInfo)
	switch kind {
	case "contextWindowExceeded":
		return permanentError("context_window_exceeded", e.Message)
	case "usageLimitExceeded":
		// ChatGPT のプランの残量切れ。**認証方式・プロバイダを自動で切り替えない**（課金とデータ利用の扱いが変わるため）ので設定起因とする。
		return configError("plan_usage_exhausted", e.Message)
	case "unauthorized":
		return configError("unauthorized", e.Message)
	case "badRequest":
		return configError("bad_request", e.Message)
	case "serverOverloaded", "internalServerError":
		return transientError(kind, e.Message)
	case "cyberPolicy":
		return permanentError("content_policy", e.Message)
	case "httpConnectionFailed", "responseStreamConnectionFailed",
		"responseStreamDisconnected", "responseTooManyFailedAttempts":
		return httpStatusError(kind, status, e.Message)
	case "other":
		// 接続の切断と HTTP 400 がどちらも `other` で届く（実測）。
		// 切断は一時的、それ以外は判別不能の安全側で設定起因とする。
		if strings.HasPrefix(e.Message, "stream disconnected") {
			return transientError("other", e.Message)
		}
		return configError("other", e.Message)
	default:
		// sandboxError / threadRollbackFailed / activeTurnNotSteerable / 未知の種類
		return permanentError(codeUnexpected, e.Message)
	}
}

// httpStatusError は添えられた HTTP の状態コードに 3 社共通の規則を当てる。
func httpStatusError(code string, status int, message string) *aiprovider.ProviderError {
	class := aiprovider.ErrClassTransient
	if status != 0 {
		class = aiprovider.ClassifyHTTPStatus(status)
	}
	return &aiprovider.ProviderError{
		Class: class, Provider: ProviderCodex, HTTPStatus: status,
		Code: code, Message: masking.Mask(message),
	}
}

// parseCodexErrorInfo は `codexErrorInfo`（文字列、または HTTP の状態コードを持つ 1 項目のオブジェクト）を読む。
func parseCodexErrorInfo(raw json.RawMessage) (kind string, status int) {
	if len(raw) == 0 {
		return "", 0
	}
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		return name, 0
	}
	var object map[string]struct {
		HTTPStatusCode *int `json:"httpStatusCode"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", 0
	}
	for key, value := range object {
		if value.HTTPStatusCode != nil {
			return key, *value.HTTPStatusCode
		}
		return key, 0
	}
	return "", 0
}

// normalizeRPCError は JSON-RPC の誤り応答を正規化する（設定起因）。
//
// Code は動的に作られるため、画面の文言は分類ごとの既定へ倒す。
func normalizeRPCError(err *rpcError) *aiprovider.ProviderError {
	return configError(fmt.Sprintf("rpc_%d", err.Code), err.Message)
}
