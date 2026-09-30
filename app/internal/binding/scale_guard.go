package binding

// 本ファイルは規模上限（性能を保証する規模の目安）の警告の判定と通知文の組み立て。
//
// トークン上限（usage_guard.go）と違い、**操作を止めない**。上限は性能保証の前提であり、
// 超過しても保存・追加は成功する。ここが返すのは通知だけで、呼び出しの可否には一切関与しない。
//
// 判定は呼ぶたびにプロジェクトフォルダの実体を数え直す（保存のたびに再評価する）。
// 画面は返り値の Message をそのまま出す（画面側で判定をやり直さない・生のコード値を出さない）。

import (
	"fmt"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// scaleWarnRatio は予告閾値（上限の 80%。70〜90% の範囲で選べる値のうち既定の値を採る）。
// 範囲外の値を渡しても projectstore 側が既定へ丸めるため、判定が止まることはない。
const scaleWarnRatio = projectstore.DefaultScaleWarnRatio

// ScaleWarning は規模上限の通知 1 件（表示先はトークン上限の警告と同じ通知欄）。
type ScaleWarning struct {
	// Kind は対象の識別（画面は表示に使わない）。
	Kind string `json:"kind"`
	// Label は対象の利用者向け表記。
	Label string `json:"label"`
	// Level は "warn"（予告）または "exceeded"（上限到達）。
	Level string `json:"level"`
	// Message は通知欄へ出す日本語 1 文（対象項目・現在値・性能保証外である旨を含む）。
	Message string `json:"message"`
}

// ScaleStatus は現在の規模上限の通知を返す（予告・到達のみ。閾値未満は含まない）。
//
// 「同時に扱うプロジェクト」はプロジェクトを開いていなくても判定できるため常に見る。
// 残る 7 種はプロジェクトを開いているときだけ判定する（開いていなければ通知は無い）。
func (a *API) ScaleStatus() ([]ScaleWarning, error) {
	var usages []projectstore.ScaleUsage

	if settings, err := a.settings(); err == nil {
		usages = append(usages, projectstore.EvaluateScale(
			projectstore.ScaleProjects, int64(len(settings.RecentProjects)), "", scaleWarnRatio))
	}

	if s, err := a.current(); err == nil {
		inProject, err := s.store.ScaleUsageAll(scaleWarnRatio)
		if err != nil {
			return nil, err
		}
		usages = append(usages, inProject...)
	}

	out := make([]ScaleWarning, 0, len(usages))
	for _, u := range usages {
		if u.Level == projectstore.ScaleLevelNone {
			continue
		}
		out = append(out, ScaleWarning{
			Kind: string(u.Kind), Label: u.Label, Level: u.Level, Message: scaleMessage(u),
		})
	}
	return out, nil
}

// scaleMessage は通知欄へ出す 1 文を組み立てる（「原因＋次の行動」の形）。
//
// 上限到達時は「対象項目・現在値・性能保証外である旨」を必ず含める。
// 併せて**操作を続けられること**を明示する（拒否ではないことを利用者に伝えるため）。
func scaleMessage(u projectstore.ScaleUsage) string {
	target := u.Label
	if u.Scope != "" {
		target = fmt.Sprintf("%s（%s）", u.Label, u.Scope)
	}
	current, limit := scaleAmount(u.Kind, u.Current), scaleAmount(u.Kind, u.Limit)
	if u.Level == projectstore.ScaleLevelExceeded {
		return fmt.Sprintf(
			"%sが %sに達しました（上限の目安 %s）。このまま操作は続けられますが、"+
				"動作の速さは性能保証の対象外になります。不要な項目の整理や、"+
				"プロジェクトの分割をご検討ください。", target, current, limit)
	}
	return fmt.Sprintf(
		"%sが %sです（上限の目安 %s）。上限に近づいています。"+
			"上限を超えても操作は続けられますが、動作の速さは性能保証の対象外になります。",
		target, current, limit)
}

// scaleAmount は現在値・上限を利用者向けの表記にする（総量だけ MB 表示）。
func scaleAmount(kind projectstore.ScaleKind, value int64) string {
	if kind == projectstore.ScaleTotalBytes {
		return fmt.Sprintf("%.1f MB", float64(value)/(1024*1024))
	}
	return fmt.Sprintf("%s 件", groupDigits(value))
}

// groupDigits は 3 桁区切り（画面と同じ規則。ロケール実装に依存させない）。
func groupDigits(v int64) string {
	s := fmt.Sprintf("%d", v)
	neg := ""
	if len(s) > 0 && s[0] == '-' {
		neg, s = "-", s[1:]
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return neg + string(out)
}
