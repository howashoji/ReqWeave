//go:build integration

// 結合テスト（確定版の版番号の再採番を変更履歴へ記録する）。

package binding

import (
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 再採番の事実（旧番号・新番号・対象種別・作業者名・日時）が変更履歴に残る。
func TestVersionRenumberIsRecordedInHistory(t *testing.T) {
	a, root := newMemberAPI(t)
	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}
	store := s.store

	// 自分の作業コピーで v1 を確定する（仮採番）。
	chapters := []projectstore.DocumentChapter{{
		DocKind: projectstore.DocKindRequirements, Chapter: "functional-requirements",
		Status: projectstore.DocStatusGenerated, GeneratedAt: time.Now().UTC(),
		Covers: []string{"FR-INV-001"}, FileName: "06-functional-requirements.md", Body: "確定内容",
	}}
	if err := store.ReplaceDraft(projectstore.DocKindRequirements, chapters); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConfirmDraft(projectstore.DocKindRequirements, []string{"FR-INV-001"},
		map[string][]string{"functional-requirements": {"FR-INV-001"}}); err != nil {
		t.Fatal(err)
	}

	// 相手が同じ v1 を先に確定していた状態で取り込む（自分が付け替える側）。
	theirs := []byte("versions:\n" +
		"  - version: 1\n" +
		"    confirmed_at: 2020-01-01T00:00:00Z\n" +
		"    confirmed_by: y.suzuki@example.co.jp\n")
	applied, resolved, err := a.versionResolver(s).ResolveVersionCollisions(root,
		map[string][]byte{projectstore.DocKindRequirements: theirs})
	if err != nil {
		t.Fatalf("衝突の解消に失敗: %v", err)
	}
	if applied != 1 || len(resolved) != 1 {
		t.Fatalf("再採番の結果が違う: applied=%d resolved=%v", applied, resolved)
	}

	rec := changeOf(t, root, "requirements/v2", auditlog.ChangeVersionRenumbered)
	if rec == nil {
		t.Fatal("再採番が変更履歴に無い")
	}
	if rec.Author != "k.sato@example.co.jp" || rec.At.IsZero() {
		t.Errorf("作業者名・日時が記録されていない: %+v", *rec)
	}
	if !strings.Contains(rec.Before, "v1") || !strings.Contains(rec.After, "v2") ||
		!strings.Contains(rec.Before, "要件定義書") {
		t.Errorf("旧番号・新番号・対象種別が記録されていない: %+v", *rec)
	}
	// 内部の種別キーを記録の表示部分へ出さない（対象 ID には残す = 追跡のため）。
	if strings.Contains(rec.Before, "requirements") || strings.Contains(rec.After, "requirements") {
		t.Errorf("表示用の記述に内部キーが出ている: %+v", *rec)
	}

	// 確定版の内容は変わらない（v2 として読める）。
	got, err := store.LoadVersion(projectstore.DocKindRequirements, 2)
	if err != nil || len(got) == 0 || got[0].Body != "確定内容" {
		t.Errorf("付け替え後に内容が読めない: %+v %v", got, err)
	}
}
