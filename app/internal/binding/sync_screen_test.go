package binding

// 単体テスト（同期系の画面 = 同期・同期先の設定・三面マージの組み立て）。
// プロジェクトを開かずに検証できる範囲（表示の組み立て・承認の写し取り）を固定する。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
	syncmod "github.com/howashoji/ReqWeave/app/internal/sync"
)

// 失敗は種別の表示名と利用者向けの 1 文で示し、詳細は折りたたみへ回す。
func TestSyncFailureView(t *testing.T) {
	failure := syncmod.Failure{Kind: syncmod.FailAuth, Op: syncmod.OpIncorporate,
		Message: "同期先の認証に失敗しました。設定で認証情報を登録し直してください。作業コピーの内容は変わっていません。",
		Detail:  "fatal: Authentication failed"}
	view, ok := syncFailureView(&failure)
	if !ok {
		t.Fatal("同期の失敗として扱われない")
	}
	if view.KindLabel != "認証できなかった" {
		t.Errorf("失敗種別の表示名が違う: %q", view.KindLabel)
	}
	if !strings.Contains(view.Message, "作業コピーの内容は変わっていません") {
		t.Errorf("作業コピーが変わらない旨が無い: %q", view.Message)
	}
	if view.Detail != failure.Detail {
		t.Errorf("詳細が失われた: %q", view.Detail)
	}
	if _, ok := syncFailureView(errors.New("ただのエラー")); ok {
		t.Error("同期の失敗でないものを失敗として扱った")
	}
}

// 表示は区分の並び順（プロジェクトのフォルダ構成の順）に従い、件数のない区分を出さない。
func TestSyncSummaryItems(t *testing.T) {
	summary := syncmod.Summary{
		syncmod.CatDecisions:    {Added: 1},
		syncmod.CatRequirements: {Modified: 2, Removed: 1},
		syncmod.CatTerms:        {},
	}
	items := syncSummaryItems(summary)
	if len(items) != 2 {
		t.Fatalf("件数のない区分が出た: %+v", items)
	}
	if items[0].CategoryLabel != "要件項目" || items[0].Total != 3 {
		t.Errorf("並び順・件数が違う: %+v", items[0])
	}
	if items[1].CategoryLabel != "決定事項" || items[1].Added != 1 {
		t.Errorf("並び順・件数が違う: %+v", items[1])
	}
}

// 画面から渡された承認をそのまま写し、**補完しない**（成立判定は同期モジュール側）。
func TestNewSyncResolverCopiesApprovalsAsIs(t *testing.T) {
	resolver := newSyncResolver([]SyncResolutionInput{
		{ConflictID: "a", Choice: "theirs"},
		{ConflictID: "b", Choice: "both", Merged: "統合後"},
		{ConflictID: "c", Choice: "unknown-choice"},
		{ConflictID: "", Choice: "ours"}, // 対象が分からない承認は捨てる
	})
	got, err := resolver.ResolveConflicts(context.Background(), []syncmod.Conflict{{ID: "a"}, {ID: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("承認の写し取りが違う: %+v", got)
	}
	if got["a"].Choice != syncmod.ChoiceTheirs || got["b"].Merged != "統合後" {
		t.Errorf("承認の内容が変わった: %+v", got)
	}
	if got["c"].Choice != syncmod.Choice("unknown-choice") {
		t.Errorf("未知の選択を勝手に直した: %+v", got["c"])
	}
	if len(resolver.collected) != 2 {
		t.Errorf("提示された競合を控えていない: %+v", resolver.collected)
	}
}

// 承認が空のときは「承認なし」を渡す（既定の選択を作らない）。
func TestNewSyncResolverWithoutApprovalsHasNoDefaults(t *testing.T) {
	resolver := newSyncResolver(nil)
	got, err := resolver.ResolveConflicts(context.Background(), []syncmod.Conflict{{ID: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("承認が無いのに選択が作られた: %+v", got)
	}
}

// 未決事項の本文は相手・自分の両方の内容を残す（片側が削除でも空欄にしない）。
func TestSyncMergeIssueBodyKeepsBothSides(t *testing.T) {
	body := syncMergeIssueBody("用語「在庫」", "鈴木", "B の定義", "")
	if !strings.Contains(body, "鈴木の内容") || !strings.Contains(body, "B の定義") {
		t.Errorf("相手の内容が残っていない: %q", body)
	}
	if !strings.Contains(body, "自分の内容") || !strings.Contains(body, "（内容なし。削除されています）") {
		t.Errorf("削除された側が示されない: %q", body)
	}
	anonymous := syncMergeIssueBody("DEC-010", "", "B の決定", "A の決定")
	if !strings.Contains(anonymous, "他のメンバーの内容") {
		t.Errorf("作業者名が無いときの見出しが違う: %q", anonymous)
	}
}

// 同期先の種別の選択肢は 3 種で、外部 Git サーバだけが明示同意を要する。
func TestSyncKindOptions(t *testing.T) {
	if len(syncKindOptions) != 3 {
		t.Fatalf("同期先の種別が 3 種でない: %+v", syncKindOptions)
	}
	if syncKindOptions[0].Kind != projectstore.SyncKindFolder {
		t.Errorf("既定が共有フォルダでない: %+v", syncKindOptions[0])
	}
	for _, o := range syncKindOptions {
		wantConsent := o.Kind == projectstore.SyncKindGitExternal
		if o.RequiresConsent != wantConsent {
			t.Errorf("明示同意の要否が違う: %+v", o)
		}
		if o.RequiresConsent && o.ConsentText == "" {
			t.Errorf("同意文が無い: %+v", o)
		}
		if o.RequiresCredential != (o.Kind != projectstore.SyncKindFolder) {
			t.Errorf("認証情報の要否が違う: %+v", o)
		}
	}
	if _, ok := syncKindOption("unknown"); ok {
		t.Error("未知の種別が選択肢として引けた")
	}
}

// 三面マージの 4 択は既定を持たず、値集合は同期モジュールの定義で閉じている。
func TestSyncChoiceOptions(t *testing.T) {
	want := []syncmod.Choice{syncmod.ChoiceTheirs, syncmod.ChoiceOurs, syncmod.ChoiceBoth, syncmod.ChoiceOpenIssue}
	if len(syncChoiceOptions) != len(want) {
		t.Fatalf("選択肢の数が違う: %+v", syncChoiceOptions)
	}
	for i, w := range want {
		if syncChoiceOptions[i].Choice != string(w) {
			t.Errorf("選択肢の並びが違う: %+v", syncChoiceOptions[i])
		}
		if syncChoiceOptions[i].Label != syncmod.ChoiceLabel[w] {
			t.Errorf("表示名が同期モジュールと食い違う: %+v", syncChoiceOptions[i])
		}
	}
}

// 成果物種別・現在フェーズから予約対象を引く（生のコード値を画面へ出さない）。
func TestDocumentReservationTargets(t *testing.T) {
	target, err := documentReservationTarget(projectstore.DocKindRequirements)
	if err != nil || target != projectstore.ReservationDocumentsRequirements {
		t.Fatalf("要件定義書の予約対象が違う: %q %v", target, err)
	}
	target, err = documentReservationTarget(projectstore.DocKindBasicDesign)
	if err != nil || target != projectstore.ReservationDocumentsBasicDesign {
		t.Fatalf("基本設計書の予約対象が違う: %q %v", target, err)
	}
	if _, err := documentReservationTarget("unknown"); err == nil {
		t.Error("未知の成果物種別が通った")
	}

	kind, err := documentKindOfPhase(projectstore.PhaseBasicDesign)
	if err != nil || kind != projectstore.DocKindBasicDesign {
		t.Fatalf("フェーズから成果物種別を引けない: %q %v", kind, err)
	}
	if _, err := documentKindOfPhase("unknown"); err == nil {
		t.Error("未知のフェーズが通った")
	}
}

// 予約選択 UI は 2 種の進め方を持ち、表示名を予約の一覧と共有する。
func TestWorkModeOptions(t *testing.T) {
	if len(workModeOptions) != 2 {
		t.Fatalf("進め方の選択肢が 2 種でない: %+v", workModeOptions)
	}
	for _, o := range workModeOptions {
		if o.Label != reservationModeLabel(o.Mode) {
			t.Errorf("表示名が予約の一覧と食い違う: %+v", o)
		}
		if o.Hint == "" {
			t.Errorf("選択の説明が無い: %+v", o)
		}
	}
	if !strings.Contains(workModeOptions[0].Hint, "反映して初めて") {
		t.Errorf("予約が反映して初めて効く旨が示されない: %+v", workModeOptions[0])
	}
}
