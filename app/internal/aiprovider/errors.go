package aiprovider

import (
	"fmt"
	"time"
)

// ErrorClass は正規化したエラー区分。
type ErrorClass int

const (
	// ErrClassTransient は一時的: ネットワーク断・タイムアウト・5xx・レート制限。再試行の対象。
	ErrClassTransient ErrorClass = iota
	// ErrClassConfig は設定起因: キー無効・認証失敗・残高不足・モデル指定不正。再試行しない。
	ErrClassConfig
	// ErrClassPermanent は恒久的: コンテキスト長超過・入力不正。再試行しない。
	ErrClassPermanent
)

func (c ErrorClass) String() string {
	switch c {
	case ErrClassTransient:
		return "transient"
	case ErrClassConfig:
		return "config"
	case ErrClassPermanent:
		return "permanent"
	default:
		return "unknown"
	}
}

// ProviderError は正規化済みのプロバイダエラー。
//
// 利用者向け文言への変換は本層では行わない（エラーカタログが Class × 発生源から生成する）。
// Message はプロバイダ生メッセージであり、キー本体を含めない（ログ・画面へキーを出さないため）。
type ProviderError struct {
	Class      ErrorClass
	Provider   ProviderID
	HTTPStatus int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("%s: %s (status=%d code=%q): %s", e.Provider, e.Class, e.HTTPStatus, e.Code, e.Message)
}

// Retryable は再試行してよいエラーかを返す（再試行するのは一時的エラーのみ）。
func (e *ProviderError) Retryable() bool { return e.Class == ErrClassTransient }

// ClassifyHTTPStatus は 3 社共通の規則で HTTP ステータスを区分へ写像する。
//
//	401/403        → 設定起因
//	408/429/5xx    → 一時的
//	その他の 4xx   → 設定起因（再試行で悪化させない安全側）
//
// 400 の内訳（コンテキスト長超過・入力不正 = 恒久的 / モデル指定不正 = 設定起因）は
// プロバイダ固有コードを見る各アダプタが上書きする。
func ClassifyHTTPStatus(status int) ErrorClass {
	switch {
	case status == 401 || status == 403:
		return ErrClassConfig
	case status == 408 || status == 429:
		return ErrClassTransient
	case status >= 500:
		return ErrClassTransient
	case status >= 400:
		return ErrClassConfig
	default:
		return ErrClassTransient
	}
}

// NetworkError はネットワーク断・タイムアウト等、HTTP 応答が得られなかった場合の正規化。
func NetworkError(p ProviderID, err error) *ProviderError {
	return &ProviderError{
		Class:    ErrClassTransient,
		Provider: p,
		Message:  err.Error(),
	}
}
