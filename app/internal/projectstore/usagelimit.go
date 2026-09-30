package projectstore

// 本ファイルは AI 利用量上限設定の保持と変更。
//
// 経路: `records` ロックで直列化 → 読み直し → 検証 →
// 原子的書き込み。**権限判定（オーナーのみ）と変更履歴
// `usage-limit-changed` の記録は呼び出し側**（バインディング層）が行う（members.yaml /
// roster.yaml と同じ分担）。変更前後の値は戻り値で返し、呼び出し側が before / after に記録できる。
//
// 上限判定に使う累計は保存しない（実績レコードとの二重管理禁止。累計は
// auditlog の集計で判定時に得る）。

import (
	"errors"
	"fmt"
)

// DefaultWarnRatio は警告閾値の既定（project.yaml の warn_ratio の既定 0.8）。
const DefaultWarnRatio = 0.8

// errUsageLimitUnchanged は「値が変わらないので書き込まない」ことを表す内部センチネル。
var errUsageLimitUnchanged = errors.New("usage limit unchanged")

// WarnRatioOrDefault は警告閾値を返す（未指定なら既定 0.8）。
func (u *UsageLimit) WarnRatioOrDefault() float64 {
	if u == nil {
		return 0
	}
	if u.WarnRatio == nil {
		return DefaultWarnRatio
	}
	return *u.WarnRatio
}

// WarnThreshold は警告を出し始めるトークン数（上限 × 警告閾値）を返す。
func (u *UsageLimit) WarnThreshold() int {
	if u == nil {
		return 0
	}
	return int(float64(u.TokensMax) * u.WarnRatioOrDefault())
}

// Equal は 2 つの上限設定が同値かを返す（未設定同士は同値）。
func (u *UsageLimit) Equal(other *UsageLimit) bool {
	if u == nil || other == nil {
		return u == nil && other == nil
	}
	return u.TokensMax == other.TokensMax && u.WarnRatioOrDefault() == other.WarnRatioOrDefault()
}

// validateUsageLimit は設定値の妥当性を利用者向けの日本語 1 文で判定する。
func validateUsageLimit(tokensMax int, warnRatio *float64) error {
	if tokensMax <= 0 {
		return fmt.Errorf("トークン上限は 1 以上の数値で入力してください（入力値: %d）。", tokensMax)
	}
	if warnRatio != nil && (*warnRatio <= 0 || *warnRatio > 1) {
		return fmt.Errorf("警告閾値は 0 より大きく 1 以下の割合で入力してください（入力値: %v）。", *warnRatio)
	}
	return nil
}

// SetUsageLimit は AI 利用量上限を設定・変更する。
//
// warnRatio が nil のときは既定（DefaultWarnRatio）を保存する。値が現在と同じ場合は
// 書き込みを行わず、before と after に同じ値を返す（呼び出し側が変更履歴を記録しないで済むように）。
func (s *Store) SetUsageLimit(tokensMax int, warnRatio *float64) (before, after *UsageLimit, err error) {
	if err := validateUsageLimit(tokensMax, warnRatio); err != nil {
		return nil, nil, err
	}
	ratio := DefaultWarnRatio
	if warnRatio != nil {
		ratio = *warnRatio
	}
	next := &UsageLimit{TokensMax: tokensMax, WarnRatio: &ratio}

	err = s.WithShortLock(LockRecords, func() error {
		return s.UpdateProject(func(p *Project) error {
			before = p.UsageLimit.clone()
			if before.Equal(next) {
				after = before
				return errUsageLimitUnchanged
			}
			p.UsageLimit = next.clone()
			after = next.clone()
			return nil
		})
	})
	if errors.Is(err, errUsageLimitUnchanged) {
		return before, after, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return before, after, nil
}

// ClearUsageLimit は AI 利用量上限を解除する（キーごと消す = 未設定に戻す）。
//
// 解除後は警告・停止が発生しない（上限未設定のプロジェクトでは警告・停止が発生しない）。
func (s *Store) ClearUsageLimit() (before *UsageLimit, err error) {
	err = s.WithShortLock(LockRecords, func() error {
		return s.UpdateProject(func(p *Project) error {
			before = p.UsageLimit.clone()
			if before == nil {
				return errUsageLimitUnchanged
			}
			p.UsageLimit = nil
			return nil
		})
	})
	if errors.Is(err, errUsageLimitUnchanged) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return before, nil
}

// UsageLimitSetting は現在の上限設定を返す（未設定なら nil）。
func (s *Store) UsageLimitSetting() *UsageLimit {
	return s.Project().UsageLimit
}

// clone は上限設定の複製を返す（nil はそのまま nil）。
func (u *UsageLimit) clone() *UsageLimit {
	if u == nil {
		return nil
	}
	out := *u
	if u.WarnRatio != nil {
		r := *u.WarnRatio
		out.WarnRatio = &r
	}
	return &out
}
