package projectstore

// 単体テスト（変更要約の区分わけ）。実行: make -C app test-unit

import (
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
)

func TestSummaryKindOf(t *testing.T) {
	tests := []struct {
		name   string
		rec    auditlog.ChangeRecord
		want   string
		wantOK bool
	}{
		{"決定事項", auditlog.ChangeRecord{Target: "DEC-001", Change: auditlog.ChangeCreated}, SummaryDecisions, true},
		{"未決事項", auditlog.ChangeRecord{Target: "ISS-002", Change: auditlog.ChangeCreated}, SummaryOpenIssues, true},
		{"未決事項の決着", auditlog.ChangeRecord{Target: "ISS-002", Change: auditlog.ChangeStatusChanged}, SummaryOpenIssues, true},
		{"要件項目の作成", auditlog.ChangeRecord{Target: "FR-INV-001", Change: auditlog.ChangeCreated}, SummaryRequirements, true},
		{"要件項目の状態変化", auditlog.ChangeRecord{Target: "FR-INV-001", Change: auditlog.ChangeStatusChanged}, SummaryConfirmation, true},
		{"非機能要件", auditlog.ChangeRecord{Target: "NFR-PF-001", Change: auditlog.ChangeUpdated}, SummaryRequirements, true},
		{"質問票", auditlog.ChangeRecord{Target: "QS-001", Change: auditlog.ChangeStatusChanged}, SummaryQuestionnaire, true},
		{"成果物の確定", auditlog.ChangeRecord{Target: "requirements/v1", Change: auditlog.ChangeStatusChanged}, SummaryConfirmation, true},
		{"基本設計の確定", auditlog.ChangeRecord{Target: "basic-design/v2", Change: auditlog.ChangeStatusChanged}, SummaryConfirmation, true},
		{"メンバー追加", auditlog.ChangeRecord{Target: "y.suzuki@example.co.jp", Change: auditlog.ChangeMemberAdded}, SummaryMembers, true},
		{"権限変更", auditlog.ChangeRecord{Target: "y.suzuki@example.co.jp", Change: auditlog.ChangeRoleChanged}, SummaryMembers, true},
		{"オーナー引き継ぎ", auditlog.ChangeRecord{Target: "y.suzuki@example.co.jp", Change: auditlog.ChangeOwnerTakeover}, SummaryMembers, true},
		// 予約の設定・解除は「予約と作業状況」。
		{"予約の設定", auditlog.ChangeRecord{Target: "terms", Change: auditlog.ChangeReservationSet}, SummaryReservations, true},
		{"予約の解除", auditlog.ChangeRecord{Target: "documents-requirements", Change: auditlog.ChangeReservationReleased}, SummaryReservations, true},
		// 区分に無いものは対象外（プロジェクトの開閉・端末紐づけ・フェーズ移行）。
		{"開いた記録", auditlog.ChangeRecord{Target: "uuid", Change: auditlog.ChangeProjectOpened}, "", false},
		{"端末紐づけ", auditlog.ChangeRecord{Target: "uuid", Change: auditlog.ChangeAuthorBinding}, "", false},
		{"フェーズ移行", auditlog.ChangeRecord{Target: "project", Change: auditlog.ChangeStatusChanged}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := summaryKindOf(tt.rec)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("区分わけが違う: got (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestChangeSummaryCounts(t *testing.T) {
	s := ChangeSummary{Items: []ChangeSummaryItem{
		{Kind: SummaryDecisions}, {Kind: SummaryDecisions}, {Kind: SummaryMembers},
	}}
	if s.IsEmpty() {
		t.Error("項目があるのに空と判定された")
	}
	counts := s.CountByKind()
	if counts[SummaryDecisions] != 2 || counts[SummaryMembers] != 1 {
		t.Errorf("区分ごとの件数が違う: %+v", counts)
	}
	if !(ChangeSummary{}).IsEmpty() {
		t.Error("空の要約が空と判定されない")
	}
}

// 要約の単位は「取り込み」。入ってきたレコードのうち、自分以外・区分のあるものだけを
// 日時の昇順で並べる。起点となる取り込みが無いときは対象なしで返す（過去の全変更を見せない）。
func TestChangeSummaryOfIncorporation(t *testing.T) {
	s := &Store{author: Author{AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤"}}
	at := func(min int) time.Time {
		return time.Date(2026, 9, 1, 9, min, 0, 0, time.UTC)
	}
	incoming := []auditlog.ChangeRecord{
		// 日時は昇順でない（並べ替えられること）
		{At: at(30), Author: "y.suzuki@example.co.jp", Target: "ISS-001",
			Change: auditlog.ChangeStatusChanged, Before: "open", After: "resolved"},
		{At: at(10), Author: "y.suzuki@example.co.jp", Target: "DEC-001", Change: auditlog.ChangeCreated},
		// 自分の変更（対象外）
		{At: at(20), Author: "k.sato@example.co.jp", Target: "DEC-002", Change: auditlog.ChangeCreated},
		// 区分の無い変更（対象外）
		{At: at(40), Author: "y.suzuki@example.co.jp", Target: "uuid", Change: auditlog.ChangeProjectOpened},
	}

	got := s.ChangeSummaryOfIncorporation("head-1", false, incoming)
	if got.NoIncorporation || got.Incorporation != "head-1" {
		t.Fatalf("取り込みの位置が違う: %+v", got)
	}
	if len(got.Items) != 2 {
		t.Fatalf("対象の絞り込みが違う: %+v", got.Items)
	}
	if got.Items[0].Target != "DEC-001" || got.Items[1].Target != "ISS-001" {
		t.Errorf("日時の昇順に並んでいない: %+v", got.Items)
	}
	if got.Items[0].Kind != SummaryDecisions || got.Items[1].Kind != SummaryOpenIssues {
		t.Errorf("区分わけが違う: %+v", got.Items)
	}
	for _, item := range got.Items {
		if item.Author == "k.sato@example.co.jp" {
			t.Errorf("自分の変更が含まれた: %+v", item)
		}
	}

	// 起点が無いときは、入ってきたレコードがあっても対象なしにする。
	none := s.ChangeSummaryOfIncorporation("head-1", true, incoming)
	if !none.NoIncorporation || len(none.Items) != 0 {
		t.Errorf("起点が無いのに変更を提示した: %+v", none)
	}
}
