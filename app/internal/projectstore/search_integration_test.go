//go:build integration

// 対話履歴の全文検索の結合テスト（実ファイル I/O）。
// 実行: make -C app test-integration

package projectstore

import (
	"fmt"
	"strings"
	"testing"
)

// seedSearchProject は検索用のセッションを 3 本作る。
//
//	S-0001 requirements / owner        … 「在庫の締め処理」を含む発話（担当者・エージェント双方）
//	S-0002 basic-design  / owner        … 「在庫の締め処理」を含む発話 1 件（中断発話）
//	S-0003 requirements  / stakeholder  … 該当しない発話のみ
func seedSearchProject(t *testing.T) *Store {
	t.Helper()
	s := createTestProject(t)

	s1, err := s.CreateSession(SessionOwner, "requirements")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []Utterance{
		{Speaker: SpeakerAgent, Body: "在庫の締め処理はいつ行いますか。"},
		{Speaker: SpeakerUser, Body: "月末に行います。"},
		{Speaker: SpeakerUser, Body: "在庫の締め処理は倉庫ごとに違います。"},
	} {
		if _, err := s.AppendUtterance(s1.ID, u); err != nil {
			t.Fatal(err)
		}
	}

	s2, err := s.CreateSession(SessionOwner, "basic-design")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendUtterance(s2.ID, Utterance{
		Speaker: SpeakerAgent, Status: UtteranceInterrupted,
		Body: "在庫の締め処理の設計を確認します",
	}); err != nil {
		t.Fatal(err)
	}

	s3, err := s.CreateSession(SessionStakeholder, "requirements")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendUtterance(s3.ID, Utterance{Speaker: SpeakerUser, Body: "出荷の伝票を起票します。"}); err != nil {
		t.Fatal(err)
	}
	return s
}

// 担当者・エージェント双方の発話本文が検索対象になる。
func TestSearchUtterancesCoversBothSpeakers(t *testing.T) {
	s := seedSearchProject(t)

	got, err := s.SearchUtterances(UtteranceSearchQuery{Text: "在庫の締め処理"})
	if err != nil {
		t.Fatalf("検索に失敗: %v", err)
	}
	if got.Total != 3 {
		t.Fatalf("該当件数が違う: %d 件（期待 3 件）: %+v", got.Total, got.Hits)
	}
	speakers := map[string]int{}
	for _, h := range got.Hits {
		speakers[h.Speaker]++
	}
	if speakers[SpeakerAgent] != 2 || speakers[SpeakerUser] != 1 {
		t.Errorf("話者の内訳が違う: %+v", speakers)
	}
	if got.ScannedSessions != 3 {
		t.Errorf("走査したセッション数が違う: %d（期待 3）", got.ScannedSessions)
	}
	if got.ScannedUtterances != 5 {
		t.Errorf("走査した発話数が違う: %d（期待 5）", got.ScannedUtterances)
	}
	if got.Truncated {
		t.Error("上限に達していないのに切り捨て扱いになっている")
	}
	// 結果は一覧で判断できる情報を持つ。
	first := got.Hits[0]
	if first.SessionID != "S-0001" || first.UtteranceID != "utt-00001" ||
		first.Phase != "requirements" || first.Type != SessionOwner || first.At.IsZero() {
		t.Errorf("結果の項目が揃っていない: %+v", first)
	}
	if !strings.Contains(first.Excerpt, "在庫の締め処理") {
		t.Errorf("抜粋に該当箇所が入っていない: %q", first.Excerpt)
	}
}

// フェーズ・種別の絞り込みと併用でき、除外分は結果に出ない。
func TestSearchUtterancesRespectsFilters(t *testing.T) {
	s := seedSearchProject(t)

	byPhase, err := s.SearchUtterances(UtteranceSearchQuery{Text: "在庫の締め処理", Phase: "basic-design"})
	if err != nil {
		t.Fatal(err)
	}
	if byPhase.Total != 1 || byPhase.Hits[0].SessionID != "S-0002" {
		t.Errorf("フェーズの絞り込みが効いていない: %+v", byPhase)
	}
	if byPhase.ScannedSessions != 1 {
		t.Errorf("絞り込みで除外したセッションまで走査している: %d 件", byPhase.ScannedSessions)
	}

	byType, err := s.SearchUtterances(UtteranceSearchQuery{Text: "在庫", Type: SessionStakeholder})
	if err != nil {
		t.Fatal(err)
	}
	if byType.Total != 0 {
		t.Errorf("種別の絞り込みが効いていない: %+v", byType.Hits)
	}
}

// 中断された発話も検索対象で、中断である旨が結果に出る。
func TestSearchUtterancesIncludesInterrupted(t *testing.T) {
	s := seedSearchProject(t)

	got, err := s.SearchUtterances(UtteranceSearchQuery{Text: "設計を確認"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 1 {
		t.Fatalf("中断発話が検索されていない: %+v", got)
	}
	if got.Hits[0].Status != UtteranceInterrupted {
		t.Errorf("中断である旨が結果に無い: %+v", got.Hits[0])
	}
}

// 該当 0 件はエラーではなく「0 件」として返る（呼び出し側が条件を示せる）。
func TestSearchUtterancesReturnsZeroHitsWithoutError(t *testing.T) {
	s := seedSearchProject(t)

	got, err := s.SearchUtterances(UtteranceSearchQuery{Text: "存在しない語句"})
	if err != nil {
		t.Fatalf("0 件がエラーになっている: %v", err)
	}
	if got.Total != 0 || len(got.Hits) != 0 {
		t.Errorf("0 件でない: %+v", got)
	}
	if got.ScannedUtterances != 5 {
		t.Errorf("走査が行われていない: %+v", got)
	}
}

// 語句が空（空白のみを含む）のときは、原因と次の行動の 1 文で拒否する。
func TestSearchUtterancesRejectsEmptyQuery(t *testing.T) {
	s := seedSearchProject(t)

	for _, text := range []string{"", "   ", "\t\n"} {
		if _, err := s.SearchUtterances(UtteranceSearchQuery{Text: text}); err == nil {
			t.Errorf("空の語句が受理された: %q", text)
		}
	}
}

// 英字は大文字小文字を区別しない（日本語の本文に混在する ID・用語を拾えること）。
func TestSearchUtterancesIsCaseInsensitive(t *testing.T) {
	s := createTestProject(t)
	sess, err := s.CreateSession(SessionOwner, "requirements")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendUtterance(sess.ID, Utterance{Speaker: SpeakerUser, Body: "SKU の採番規則を決めます。"}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"sku", "SKU", "Sku"} {
		got, err := s.SearchUtterances(UtteranceSearchQuery{Text: q})
		if err != nil {
			t.Fatal(err)
		}
		if got.Total != 1 {
			t.Errorf("%q で見つからない: %+v", q, got)
		}
	}
}

// 上限を超える該当は総数を数え切ったうえで一覧を切り、切ったことを伝える（黙って落とさない）。
func TestSearchUtterancesTruncatesButCountsAll(t *testing.T) {
	s := createTestProject(t)
	sess, err := s.CreateSession(SessionOwner, "requirements")
	if err != nil {
		t.Fatal(err)
	}
	const total = 25
	for i := 0; i < total; i++ {
		if _, err := s.AppendUtterance(sess.ID, Utterance{
			Speaker: SpeakerUser, Body: fmt.Sprintf("在庫の話 その%d", i+1),
		}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.SearchUtterances(UtteranceSearchQuery{Text: "在庫", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != total {
		t.Errorf("総数が数え切れていない: %d（期待 %d）", got.Total, total)
	}
	if len(got.Hits) != 10 {
		t.Errorf("一覧が上限で切られていない: %d 件", len(got.Hits))
	}
	if !got.Truncated {
		t.Error("切ったことが伝わっていない")
	}
}
