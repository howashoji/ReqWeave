//go:build integration

// 結合テスト（番号帯の残量表示）。

package binding

import (
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 番号帯を使わないプロジェクト（単独利用）では警告を出さない。
func TestIDRangeWarningsEmptyForSoloProject(t *testing.T) {
	a, _ := newMemberAPI(t)
	got, err := a.IDRangeWarnings()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("単独利用で警告が出た: %+v", got)
	}
}

// 残りが少ない種別・使い切った種別だけが、種別名と残件数つきで警告される。
func TestIDRangeWarningsSurfaceLowAndExhausted(t *testing.T) {
	a, _ := newMemberAPI(t)
	store := storeOf(t, a)
	if _, err := store.ReserveIDRanges(50, nil); err != nil {
		t.Fatal(err)
	}

	// 残り十分（1 件だけ使う）→ 警告なし。
	if _, err := store.CreateDecision(projectstore.Decision{TopicKey: "custom/引当の起点",
		Body: "受注確定時に引き当てる。", Evidence: []string{"S-0001#utt-00001"}}); err != nil {
		t.Fatal(err)
	}
	if got, err := a.IDRangeWarnings(); err != nil || len(got) != 0 {
		t.Fatalf("残り十分なのに警告が出た: %+v %v", got, err)
	}

	// 決定事項を 40 件まで使う（残り 10 件 = 20%）→ 当該種別だけ警告。
	for i := 0; i < 39; i++ {
		if _, err := store.CreateDecision(projectstore.Decision{TopicKey: "custom/論点",
			Body: "決定内容。", Evidence: []string{"S-0001#utt-00001"}}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := a.IDRangeWarnings()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("警告の件数が違う: %+v", got)
	}
	w := got[0]
	if w.KindLabel != "決定事項" || w.Remaining != 10 || w.Exhausted {
		t.Errorf("警告の内容が違う: %+v", w)
	}
	if !strings.Contains(w.Message, "決定事項") || !strings.Contains(w.Message, "同期") {
		t.Errorf("原因と次の行動が示されない: %q", w.Message)
	}
	for _, forbidden := range []string{"decision", "from", "to", "range"} {
		if strings.Contains(strings.ToLower(w.Message), forbidden) {
			t.Errorf("内部の語（%s）が画面向け文言に出ている: %q", forbidden, w.Message)
		}
	}

	// 使い切ると「作成できない」旨になる。
	for i := 0; i < 10; i++ {
		if _, err := store.CreateDecision(projectstore.Decision{TopicKey: "custom/論点",
			Body: "決定内容。", Evidence: []string{"S-0001#utt-00001"}}); err != nil {
			t.Fatal(err)
		}
	}
	got, err = a.IDRangeWarnings()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Exhausted || got[0].Remaining != 0 {
		t.Fatalf("使い切りの警告が違う: %+v", got)
	}
	if !strings.Contains(got[0].Message, "作成できません") {
		t.Errorf("新規作成ができない旨が示されない: %q", got[0].Message)
	}
}
