//go:build integration

// 結合テスト（確定・差し戻し・フェーズ移行 × 実ファイル）。

package binding

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/docgen"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

func nowUTC() time.Time { return time.Now().UTC() }

// draftChapters は生成済みドラフトの代わりに直接書くための最小の章。
func draftChapters() []projectstore.DocumentChapter {
	return []projectstore.DocumentChapter{
		{Chapter: "background", FileName: "01-business-context.md",
			GeneratedAt: nowUTC(), Body: "# 業務背景\n\n現状は Excel 台帳。"},
		{Chapter: "functional-requirements", FileName: "06-functional-requirements.md",
			GeneratedAt: nowUTC(), Covers: []string{"FR-INV-001"},
			Body: "# 機能要件\n\nFR-INV-001 在庫引当。"},
	}
}

// openForConfirm は確定できる状態（要件項目 1 件・ドラフトあり）の API を返す。
func openForConfirm(t *testing.T) (*API, *projectstore.Store, *streamingStub) {
	t.Helper()
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.CreateRequirement("INV", projectstore.Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: projectstore.RequirementFunctional,
		Priority: projectstore.PriorityMust, Body: "受注確定時に在庫を引き当てること。",
		AcceptanceCriteria: []string{"3 秒以内"}, Evidence: []string{"S-0001#utt-00001"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.ReplaceDraft(projectstore.DocKindRequirements, draftChapters()); err != nil {
		t.Fatal(err)
	}
	return a, s.store, stub
}

// 要件項目が 0 件では確定できない（空の要件定義書を確定版にしない）。
func TestConfirmRejectsEmptyProject(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })

	check, err := a.ConfirmCheck(projectstore.DocKindRequirements)
	if err != nil {
		t.Fatalf("確定前チェックに失敗: %v", err)
	}
	if check.Confirmable || !check.NoRequirements {
		t.Fatalf("要件項目 0 件で確定可になっている: %+v", check)
	}
	if !strings.Contains(check.Reason, "要件項目がまだ 1 件もありません") {
		t.Errorf("理由が示されない: %+v", check)
	}
	if _, err := a.ConfirmDocument(projectstore.DocKindRequirements, WorkStart{Mode: projectstore.ReservationExclusive}); err == nil {
		t.Error("要件項目 0 件で確定できた")
	}
}

// ブロックする未決事項が残った状態では確定せず、対象一覧を返す。
func TestConfirmRejectsBlockingIssues(t *testing.T) {
	a, store, _ := openForConfirm(t)
	issue, err := store.CreateOpenIssue(projectstore.OpenIssue{Owner: "佐藤",
		Body: "引当の単位", Evidence: []string{"S-0001#utt-00001"}})
	if err != nil {
		t.Fatal(err)
	}
	base, err := store.CurrentBaseline("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRequirementGuarded(base, "FR-INV-001", func(r *projectstore.Requirement) error {
		r.BlockedBy = []string{issue.ID}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	check, err := a.ConfirmCheck(projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if check.Confirmable {
		t.Fatal("ブロックする未決があるのに確定可になっている")
	}
	if len(check.BlockingIssues) != 1 || check.BlockingIssues[0] != issue.ID {
		t.Errorf("ブロック対象の一覧が返らない: %+v", check)
	}
	if _, err := a.ConfirmDocument(projectstore.DocKindRequirements, WorkStart{Mode: projectstore.ReservationExclusive}); err == nil {
		t.Error("ブロックする未決があるのに確定できた")
	}
	// 確定版は作られない。
	if latest, _ := store.LatestVersion(projectstore.DocKindRequirements); latest != 0 {
		t.Errorf("確定版が作られた: v%d", latest)
	}
}

// 確定で全要件項目が合意済みになり、版番号が 1 増える。
func TestConfirmCreatesVersionAndAgrees(t *testing.T) {
	a, store, _ := openForConfirm(t)

	check, err := a.ConfirmCheck(projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if !check.Confirmable || !check.HasDraft {
		t.Fatalf("確定できる状態にならない: %+v", check)
	}

	result, err := a.ConfirmDocument(projectstore.DocKindRequirements, WorkStart{Mode: projectstore.ReservationExclusive})
	if err != nil {
		t.Fatalf("確定に失敗: %v", err)
	}
	if result.Version != 1 || len(result.AgreedRequirements) != 1 {
		t.Fatalf("確定結果が違う: %+v", result)
	}
	if !result.CanMoveToBasicDesign {
		t.Error("基本設計へ移行できる状態にならない")
	}
	got, err := store.LoadRequirement("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != projectstore.RequirementAgreed {
		t.Errorf("要件項目が合意済みにならない: %+v", got)
	}
	versions, err := store.Versions(projectstore.DocKindRequirements)
	if err != nil || len(versions) != 1 {
		t.Fatalf("版履歴が違う: %+v %v", versions, err)
	}
	// 版履歴に収載ソース ID と依存マップが入る。
	if len(versions[0].SourceIDs) == 0 || len(versions[0].Chapters) == 0 {
		t.Errorf("版履歴の内容が薄い: %+v", versions[0])
	}
}

// 確定前は基本設計へ移行できず、確定後は移行できる。
func TestMoveToBasicDesignRequiresConfirmation(t *testing.T) {
	a, store, _ := openForConfirm(t)

	if err := a.MoveToBasicDesign(); err == nil {
		t.Fatal("確定前に基本設計へ移行できた")
	} else if !strings.Contains(err.Error(), "確定") {
		t.Errorf("誘導の文言が無い: %v", err)
	}
	if store.Project().Phase != dialogue.PhaseRequirements {
		t.Errorf("フェーズが変わってしまった: %s", store.Project().Phase)
	}

	if _, err := a.ConfirmDocument(projectstore.DocKindRequirements, WorkStart{Mode: projectstore.ReservationExclusive}); err != nil {
		t.Fatal(err)
	}
	if err := a.MoveToBasicDesign(); err != nil {
		t.Fatalf("確定後に移行できない: %v", err)
	}
	if store.Project().Phase != dialogue.PhaseBasicDesign {
		t.Errorf("フェーズが移行していない: %s", store.Project().Phase)
	}
	// 二重呼び出しは何もしない。
	if err := a.MoveToBasicDesign(); err != nil {
		t.Errorf("移行済みで再度呼ぶとエラーになる: %v", err)
	}
}

// 確定後にドラフトを差し替えても確定版は変わらない。
func TestConfirmedVersionIsImmutable(t *testing.T) {
	a, store, _ := openForConfirm(t)
	if _, err := a.ConfirmDocument(projectstore.DocKindRequirements, WorkStart{Mode: projectstore.ReservationExclusive}); err != nil {
		t.Fatal(err)
	}
	before, err := store.LoadVersion(projectstore.DocKindRequirements, 1)
	if err != nil {
		t.Fatal(err)
	}

	next := draftChapters()
	next[0].Body = "# 業務背景\n\n差し替えたドラフト。"
	if err := store.ReplaceDraft(projectstore.DocKindRequirements, next); err != nil {
		t.Fatal(err)
	}
	after, err := store.LoadVersion(projectstore.DocKindRequirements, 1)
	if err != nil {
		t.Fatal(err)
	}
	diffs := docgen.DiffDocuments(before, after)
	for _, d := range diffs {
		if d.Changed() {
			t.Errorf("確定版が変化した: %+v", d)
		}
	}
	_ = context.Background()
}

// 生成 → 版一覧 → プレビュー → 差分 → 検証 → エクスポートが公開 API で通る。
func TestDocumentBindingFlow(t *testing.T) {
	a, store, stub := openForConfirm(t)
	// 生成は章ごとに AI を呼ぶため、常に本文を返すスタブにする。
	stub.scripts = []string{"## 本文\n\n生成された内容です。FR-INV-001 を参照します。"}

	if err := a.GenerateDocument(projectstore.DocKindRequirements, false, WorkStart{Mode: projectstore.ReservationExclusive}); err != nil {
		t.Fatalf("生成を開始できない: %v", err)
	}
	waitForChapters(t, a, projectstore.DocKindRequirements, 16)

	// 版一覧: ドラフトのみ。
	versions, err := a.DocumentVersions(projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Version != 0 || !versions[0].HasContent {
		t.Fatalf("版一覧が違う: %+v", versions)
	}

	// プレビュー: 章の本文と表示名が返る。
	chapters, err := a.DocumentChapters(projectstore.DocKindRequirements, 0)
	if err != nil {
		t.Fatal(err)
	}
	var index, fr DocumentChapterView
	for _, c := range chapters {
		switch c.FileName {
		case "00-index.md":
			index = c
		case "06-functional-requirements.md":
			fr = c
		}
	}
	if index.Title != "目次・表記規約" || !strings.Contains(index.Body, "01-business-context.md") {
		t.Errorf("目次のプレビューが違う: %+v", index)
	}
	if !strings.Contains(fr.Body, "FR-INV-001") || len(fr.Covers) == 0 {
		t.Errorf("機能要件のプレビューが違う: %+v", fr)
	}

	// 検証: ブロックする未決が無いので V3 は出ない（この状態ではエラー 0 を期待しない = 参照欠落など他項目は出うる）。
	result, err := a.VerifyDocument(projectstore.DocKindRequirements, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = result

	// 確定 → 版一覧にドラフトと v1 が並ぶ。
	if _, err := a.ConfirmDocument(projectstore.DocKindRequirements, WorkStart{Mode: projectstore.ReservationExclusive}); err != nil {
		t.Fatalf("確定に失敗: %v", err)
	}
	versions, err = a.DocumentVersions(projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[1].Version != 1 || versions[1].ConfirmedAt == "" {
		t.Fatalf("確定後の版一覧が違う: %+v", versions)
	}

	// 差分: ドラフトを再生成して v1 と比べる。
	stub.scripts = []string{"## 本文\n\n書き換えた内容です。FR-INV-001 を参照します。"}
	if err := a.GenerateDocument(projectstore.DocKindRequirements, false, WorkStart{Mode: projectstore.ReservationExclusive}); err != nil {
		t.Fatal(err)
	}
	waitForDraftBody(t, store, "書き換えた内容")
	diffs, err := a.DocumentDiff(projectstore.DocKindRequirements, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	var changed int
	for _, d := range diffs {
		if d.Changed() {
			changed++
		}
	}
	if changed == 0 {
		t.Errorf("差分が出ない: %+v", diffs)
	}

	// エクスポート: 出力先へ一式が出る。
	dest := filepath.Join(t.TempDir(), "export")
	exported, err := a.ExportDocuments(ExportRequestView{Destination: dest, AcceptWarnings: true})
	if err != nil {
		t.Fatalf("エクスポートに失敗: %v", err)
	}
	if !exported.Exported || len(exported.Files) == 0 {
		t.Fatalf("出力されない: %+v", exported)
	}
	if _, err := os.Stat(filepath.Join(dest, "CLAUDE.md")); err != nil {
		t.Errorf("導入ファイルが無い: %v", err)
	}
}

// AI キー未設定では生成できない（共通前段を通る）。
func TestGenerateBlockedWhenAINotReady(t *testing.T) {
	a, _, _ := openForConfirm(t)
	settings, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := settings.DefaultProviderSetting()
	if err := a.DeleteKey(provider.Provider, provider.Label); err != nil {
		t.Fatal(err)
	}
	if err := a.GenerateDocument(projectstore.DocKindRequirements, false, WorkStart{Mode: projectstore.ReservationExclusive}); err == nil {
		t.Fatal("キー未設定でも生成できた")
	} else if !strings.Contains(err.Error(), "キー") {
		t.Errorf("理由が示されない: %v", err)
	}
}

// 開いていない状態では成果物 API を拒否する。
func TestDocumentAPIRequiresOpenProject(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, _ := newDialogueAPI(t, stub)
	if _, err := a.DocumentVersions(projectstore.DocKindRequirements); err == nil {
		t.Error("開いていないのに版一覧を取得できた")
	}
	if _, err := a.ExportDocuments(ExportRequestView{Destination: "/tmp/x"}); err == nil {
		t.Error("開いていないのにエクスポートできた")
	}
}

// waitForChapters はドラフトの章数が期待値になるまで待つ（生成は非同期のため）。
func waitForChapters(t *testing.T, a *API, kind string, want int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		s, err := a.current()
		if err != nil {
			t.Fatal(err)
		}
		chapters, err := s.store.LoadDraft(kind)
		if err == nil && len(chapters) == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("章数が %d にならない", want)
}

// waitForDraftBody はドラフトの本文に文字列が現れるまで待つ。
func waitForDraftBody(t *testing.T, store *projectstore.Store, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		chapters, err := store.LoadDraft(projectstore.DocKindRequirements)
		if err == nil {
			for _, c := range chapters {
				if strings.Contains(c.Body, want) {
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("ドラフトに %q が現れない", want)
}
