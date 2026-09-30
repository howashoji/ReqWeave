package binding

import (
	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// enum を画面に出すときはラベルへ写像し、
// フォールバックで生のコード値を表示しない。
func TestPhaseAndRoleLabels(t *testing.T) {
	phases := map[string]string{
		projectstore.PhaseRequirements: "要件定義",
		projectstore.PhaseBasicDesign:  "基本設計",
	}
	for phase, want := range phases {
		if got := phaseLabel(phase); got != want {
			t.Errorf("%q: got %q, want %q", phase, got, want)
		}
	}
	if got := phaseLabel("future-phase"); got != "状態不明" || strings.Contains(got, "future") {
		t.Errorf("未知のフェーズで生のコード値が出た: %q", got)
	}

	roles := map[string]string{
		projectstore.RoleOwner:  "オーナー",
		projectstore.RoleEditor: "編集",
		projectstore.RoleViewer: "閲覧",
	}
	for role, want := range roles {
		if got := roleLabel(role); got != want {
			t.Errorf("%q: got %q, want %q", role, got, want)
		}
	}
	if got := roleLabel("admin"); got != "権限不明" || strings.Contains(got, "admin") {
		t.Errorf("未知の権限で生のコード値が出た: %q", got)
	}
}

// 業務領域は 4 領域。外部から取得しない静的データ。
func TestDomainPresets(t *testing.T) {
	a := &API{}
	presets, err := a.DomainPresets()
	if err != nil {
		t.Fatalf("業務領域の選択肢を取得できない: %v", err)
	}
	if len(presets) != 4 {
		t.Fatalf("領域数が違う: %d", len(presets))
	}
	want := map[string]bool{"sales": true, "inventory": true, "accounting": true, "workflow": true}
	for _, p := range presets {
		if !want[p.ID] {
			t.Errorf("想定外の領域: %q", p.ID)
		}
		delete(want, p.ID)
		if p.Label == "" || p.Summary == "" {
			t.Errorf("%s: ラベル・要旨が空", p.ID)
		}
	}
	if len(want) != 0 {
		t.Errorf("欠けている領域: %v", want)
	}
	if err := validateDomainPresets([]string{"sales", "workflow"}); err != nil {
		t.Errorf("正しい領域が拒否された: %v", err)
	}
	if err := validateDomainPresets([]string{"sales", "hr"}); err == nil {
		t.Error("一覧外の領域が受理された")
	}

	// 選択肢は対話エンジンのプリセット定義（観点まで持つ正本）から導く（二重管理をしない）。
	definition, err := dialogue.LoadDomainPresets()
	if err != nil {
		t.Fatalf("プリセット定義を読み込めない: %v", err)
	}
	if len(definition.Domains) != len(presets) {
		t.Fatalf("定義と選択肢の領域数が食い違う: %d / %d", len(definition.Domains), len(presets))
	}
	for i, d := range definition.Domains {
		if presets[i].ID != d.ID || presets[i].Label != d.Name || presets[i].Summary != d.Summary {
			t.Errorf("選択肢[%d]が定義と食い違う: %+v / %+v", i, presets[i], d)
		}
	}
}
