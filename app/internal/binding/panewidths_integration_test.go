//go:build integration

package binding

import (
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 3 ペインの幅は端末ごとのアプリ設定へ保存し、範囲外は拒否する。
func TestPaneWidthsRoundTrip(t *testing.T) {
	a, _ := newDialogueAPI(t, &streamingStub{})

	// 未設定のうちは既定値と許容範囲が返る。
	view, err := a.PaneWidths()
	if err != nil {
		t.Fatalf("既定の幅を取得できない: %v", err)
	}
	if view.Versions != projectstore.DefaultVersionsPaneWidth || view.Checks != projectstore.DefaultChecksPaneWidth {
		t.Errorf("既定値が違う: %+v", view)
	}
	if view.Min != projectstore.MinPaneWidth || view.Max != projectstore.MaxPaneWidth {
		t.Errorf("許容範囲が返っていない: %+v", view)
	}

	// 保存すると次に取得したときも同じ値（端末ごとの設定に残る）。
	if err := a.SetPaneWidths(300, 420); err != nil {
		t.Fatalf("保存に失敗: %v", err)
	}
	again, err := a.PaneWidths()
	if err != nil {
		t.Fatal(err)
	}
	if again.Versions != 300 || again.Checks != 420 {
		t.Errorf("保存した幅が返らない: %+v", again)
	}

	// 範囲外は保存せず、許容範囲を添えて拒否する。
	for _, c := range []struct {
		name             string
		versions, checks int
	}{
		{"左が狭すぎる", projectstore.MinPaneWidth - 1, 420},
		{"右が広すぎる", 300, projectstore.MaxPaneWidth + 1},
	} {
		if err := a.SetPaneWidths(c.versions, c.checks); err == nil {
			t.Errorf("%s: 範囲外が受理された", c.name)
		}
	}
	kept, err := a.PaneWidths()
	if err != nil {
		t.Fatal(err)
	}
	if kept.Versions != 300 || kept.Checks != 420 {
		t.Errorf("拒否したのに保存値が変わった: %+v", kept)
	}
}
