package binding

import (
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 上限到達の通知は「対象項目・現在値・性能保証外である旨」を必ず含む。
// 8 種すべてで確かめる（1 種だけ文言を作り忘れる事故を捕まえる）。
func TestScaleMessageCarriesRequiredFacts(t *testing.T) {
	kinds := projectstore.ScaleKinds()
	if len(kinds) != 8 {
		t.Fatalf("上限の対象が 8 種ではありません: %d 種", len(kinds))
	}
	for _, k := range kinds {
		limit := projectstore.ScaleLimitOf(k)
		t.Run(string(k)+"/上限到達", func(t *testing.T) {
			u := projectstore.EvaluateScale(k, limit, "", projectstore.DefaultScaleWarnRatio)
			if u.Level != projectstore.ScaleLevelExceeded {
				t.Fatalf("上限ちょうどが到達扱いになっていない: %q", u.Level)
			}
			msg := scaleMessage(u)
			if !strings.Contains(msg, projectstore.ScaleLabelOf(k)) {
				t.Errorf("対象項目が通知文に無い: %q", msg)
			}
			if !strings.Contains(msg, "性能保証の対象外") {
				t.Errorf("性能保証外である旨が通知文に無い: %q", msg)
			}
			if !strings.Contains(msg, "操作は続けられ") {
				t.Errorf("操作を継続できる旨が通知文に無い（拒否と誤解される）: %q", msg)
			}
			if !strings.Contains(msg, scaleAmount(k, limit)) {
				t.Errorf("現在値が通知文に無い: %q", msg)
			}
			// 生のコード値（kind）を画面文言へ出さない。
			if strings.Contains(msg, string(k)) {
				t.Errorf("内部の識別子が通知文に出ている: %q", msg)
			}
		})
		t.Run(string(k)+"/予告", func(t *testing.T) {
			// 予告閾値ちょうど（既定 80%）。上限が 10 で割り切れない種別は無い。
			current := limit * 8 / 10
			u := projectstore.EvaluateScale(k, current, "", projectstore.DefaultScaleWarnRatio)
			if u.Level != projectstore.ScaleLevelWarn {
				t.Fatalf("予告閾値ちょうどが予告扱いになっていない: 現在値 %d / 上限 %d / 判定 %q",
					current, limit, u.Level)
			}
			msg := scaleMessage(u)
			if !strings.Contains(msg, "上限に近づいています") {
				t.Errorf("予告であることが通知文に無い: %q", msg)
			}
			if !strings.Contains(msg, scaleAmount(k, current)) {
				t.Errorf("現在値が通知文に無い: %q", msg)
			}
		})
	}
}

// 発話の通知は対象セッションを含む（どのセッションの話か分からないと対処できない）。
func TestScaleMessageNamesTargetSession(t *testing.T) {
	u := projectstore.EvaluateScale(projectstore.ScaleUtterances, 1000, "S-0007",
		projectstore.DefaultScaleWarnRatio)
	msg := scaleMessage(u)
	if !strings.Contains(msg, "S-0007") {
		t.Errorf("対象セッションが通知文に無い: %q", msg)
	}
}

// 総量だけは MB 表記（件数で出すと桁が読めない）。
func TestScaleAmountFormatsBytesAsMegabytes(t *testing.T) {
	got := scaleAmount(projectstore.ScaleTotalBytes, projectstore.ScaleLimitBytes)
	if got != "500.0 MB" {
		t.Errorf("総量の表記が %q（期待 \"500.0 MB\"）", got)
	}
	if n := scaleAmount(projectstore.ScaleSessions, 1234); n != "1,234 件" {
		t.Errorf("件数の表記が %q（期待 \"1,234 件\"）", n)
	}
}

func TestGroupDigits(t *testing.T) {
	cases := map[int64]string{0: "0", 7: "7", 999: "999", 1000: "1,000",
		524288000: "524,288,000", -1234: "-1,234"}
	for in, want := range cases {
		if got := groupDigits(in); got != want {
			t.Errorf("groupDigits(%d) = %q（期待 %q）", in, got, want)
		}
	}
}

// 閾値未満は通知しない（通知欄を無意味に占有しない）。
func TestScaleStatusOmitsBelowThreshold(t *testing.T) {
	u := projectstore.EvaluateScale(projectstore.ScaleSessions, 1, "",
		projectstore.DefaultScaleWarnRatio)
	if u.Level != projectstore.ScaleLevelNone {
		t.Fatalf("1 件で通知対象になっている: %q", u.Level)
	}
}
