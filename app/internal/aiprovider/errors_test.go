package aiprovider

import (
	"errors"
	"testing"
)

// 3 社共通の規則。
func TestClassifyHTTPStatus(t *testing.T) {
	cases := map[int]ErrorClass{
		401: ErrClassConfig,
		403: ErrClassConfig,
		408: ErrClassTransient,
		429: ErrClassTransient,
		500: ErrClassTransient,
		503: ErrClassTransient,
		529: ErrClassTransient,
		400: ErrClassConfig, // 400 の内訳は各アダプタが上書きする
		404: ErrClassConfig, // 判別不能な 4xx は設定起因（再試行で悪化させない安全側）
		422: ErrClassConfig,
	}
	for status, want := range cases {
		if got := ClassifyHTTPStatus(status); got != want {
			t.Errorf("%d: got %v, want %v", status, got, want)
		}
	}
}

// 再試行するのは一時的エラーのみ。
func TestRetryable(t *testing.T) {
	cases := map[ErrorClass]bool{
		ErrClassTransient: true,
		ErrClassConfig:    false,
		ErrClassPermanent: false,
	}
	for class, want := range cases {
		e := &ProviderError{Class: class}
		if got := e.Retryable(); got != want {
			t.Errorf("%v: got %v, want %v", class, got, want)
		}
	}
}

func TestNetworkErrorIsTransient(t *testing.T) {
	e := NetworkError(ProviderAnthropic, errors.New("dial tcp: connection refused"))
	if e.Class != ErrClassTransient || !e.Retryable() {
		t.Errorf("ネットワーク断が一時的として扱われない: %+v", e)
	}
	if e.Provider != ProviderAnthropic {
		t.Errorf("発生源が記録されていない: %+v", e)
	}
}

func TestProviderErrorMessageIncludesClassAndStatus(t *testing.T) {
	e := &ProviderError{Class: ErrClassConfig, Provider: ProviderOpenAI, HTTPStatus: 401,
		Code: "invalid_api_key", Message: "Incorrect API key provided"}
	s := e.Error()
	for _, want := range []string{"openai", "config", "401", "invalid_api_key"} {
		if !contains(s, want) {
			t.Errorf("エラー文字列に %q が無い: %q", want, s)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
