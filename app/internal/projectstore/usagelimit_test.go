package projectstore

// 単体テスト（AI 利用量上限設定の値の扱い）。実行: make -C app test-unit

import (
	"strings"
	"testing"
)

func ratio(v float64) *float64 { return &v }

// warn_ratio の既定は 0.8。未設定の上限は警告閾値を持たない。
func TestWarnRatioOrDefault(t *testing.T) {
	// 既定値は 0.8。定数と突き合わせるだけでは既定値の変更を検知できないため、
	// 設計上の値をテスト側にリテラルで固定する。
	if DefaultWarnRatio != 0.8 {
		t.Errorf("警告閾値の既定が設計上の値と違う: got %v, want 0.8", DefaultWarnRatio)
	}
	if got := (&UsageLimit{TokensMax: 100}).WarnRatioOrDefault(); got != 0.8 {
		t.Errorf("既定の警告閾値が違う: got %v, want 0.8", got)
	}
	if got := (&UsageLimit{TokensMax: 1000}).WarnThreshold(); got != 800 {
		t.Errorf("既定の警告閾値でのしきい値が違う: got %d, want 800", got)
	}
	if got := (&UsageLimit{TokensMax: 100, WarnRatio: ratio(0.5)}).WarnRatioOrDefault(); got != 0.5 {
		t.Errorf("指定した警告閾値が使われない: got %v", got)
	}
	var unset *UsageLimit
	if got := unset.WarnRatioOrDefault(); got != 0 {
		t.Errorf("未設定の警告閾値が 0 でない: got %v", got)
	}
	if got := (&UsageLimit{TokensMax: 1000, WarnRatio: ratio(0.9)}).WarnThreshold(); got != 900 {
		t.Errorf("警告を出し始めるトークン数が違う: got %d, want 900", got)
	}
	if got := unset.WarnThreshold(); got != 0 {
		t.Errorf("未設定の警告閾値のしきい値が 0 でない: got %d", got)
	}
}

// 未設定同士は同値。既定値の 0.8 と明示した 0.8 も同値（無意味な変更履歴を作らないため）。
func TestUsageLimitEqual(t *testing.T) {
	var unset *UsageLimit
	if !unset.Equal(nil) {
		t.Error("未設定同士が同値と判定されない")
	}
	if unset.Equal(&UsageLimit{TokensMax: 100}) {
		t.Error("未設定と設定済みが同値と判定された")
	}
	a := &UsageLimit{TokensMax: 100}
	b := &UsageLimit{TokensMax: 100, WarnRatio: ratio(DefaultWarnRatio)}
	if !a.Equal(b) {
		t.Error("既定の警告閾値と明示した既定値が同値と判定されない")
	}
	if a.Equal(&UsageLimit{TokensMax: 100, WarnRatio: ratio(0.5)}) {
		t.Error("警告閾値が違うのに同値と判定された")
	}
	if a.Equal(&UsageLimit{TokensMax: 200}) {
		t.Error("上限値が違うのに同値と判定された")
	}
}

// 不正な値は書き込み前に拒否し、理由を日本語 1 文で返す（利用者向けの入力エラー）。
func TestValidateUsageLimit(t *testing.T) {
	for name, tc := range map[string]struct {
		tokensMax int
		warnRatio *float64
		wantWord  string
	}{
		"上限が 0":     {0, nil, "トークン上限は 1 以上"},
		"上限が負":      {-1, nil, "トークン上限は 1 以上"},
		"警告閾値が 0":   {100, ratio(0), "警告閾値は 0 より大きく 1 以下"},
		"警告閾値が 1 超": {100, ratio(1.5), "警告閾値は 0 より大きく 1 以下"},
		"警告閾値が負":    {100, ratio(-0.5), "警告閾値は 0 より大きく 1 以下"},
	} {
		err := validateUsageLimit(tc.tokensMax, tc.warnRatio)
		if err == nil {
			t.Errorf("%s: 拒否されなかった", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantWord) {
			t.Errorf("%s: 理由が利用者向けでない: %v", name, err)
		}
	}
	if err := validateUsageLimit(100, ratio(1)); err != nil {
		t.Errorf("警告閾値 1（= 上限到達時に初めて警告）が拒否された: %v", err)
	}
	if err := validateUsageLimit(1, nil); err != nil {
		t.Errorf("最小の上限値が拒否された: %v", err)
	}
}

// 保存されたデータ側の検証（多層防御の最後の砦）。入力検証とは別に構造として弾く。
func TestProjectValidateRejectsBrokenUsageLimit(t *testing.T) {
	base := validProjectYAML
	for name, tc := range map[string]struct{ yaml, wantWord string }{
		"tokens_max が 0": {base + "usage_limit:\n  tokens_max: 0\n", "tokens_max が正の数ではありません"},
		"warn_ratio が 0": {base + "usage_limit:\n  tokens_max: 100\n  warn_ratio: 0\n", "warn_ratio が 0 超 1 以下ではありません"},
		"warn_ratio が 2": {base + "usage_limit:\n  tokens_max: 100\n  warn_ratio: 2\n", "warn_ratio が 0 超 1 以下ではありません"},
	} {
		_, err := UnmarshalProject([]byte(tc.yaml))
		if err == nil {
			t.Errorf("%s: 読み込みが成立した", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantWord) {
			t.Errorf("%s: どの層のどの理由で落ちたかが分からない: %v", name, err)
		}
	}
}
