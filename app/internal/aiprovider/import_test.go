package aiprovider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var testImportRefs = []ImportRef{{
	ID:         "IMP-001",
	SourceName: "現行業務フロー.docx",
	ImportedAt: time.Date(2026, 8, 31, 1, 2, 3, 0, time.UTC),
}}

// ImportRefs が空でなく ConsentGiven が false のリクエストは送信されない。
// モック境界（アダプタ）が一度も呼ばれないことで「HTTP 呼び出しが発生しない」を確認する。
func TestImportSendBlockedWithoutConsent(t *testing.T) {
	rec := newFakeRecorder()
	a := &scriptedAdapter{scripts: [][]StreamEvent{okScript("送ってはいけない")}}
	req := ChatRequest{Model: "m", Messages: []Message{{Role: RoleUser, Content: "資料本文"}},
		ImportRefs: testImportRefs}

	ch, err := StreamRetrying(context.Background(), a, req, StreamOptions{Recorder: rec})
	if err == nil {
		t.Fatal("同意なしの取り込み送信がエラーにならなかった")
	}
	if ch != nil {
		t.Error("送信していないのにイベントチャネルが返った")
	}
	if a.calls != 0 {
		t.Errorf("同意なしでプロバイダへ送信された: %d 回", a.calls)
	}
	if len(rec.sends) != 0 {
		t.Errorf("送信していないのに送信記録が残った: %+v", rec.sends)
	}
	var perr *ProviderError
	if !errors.As(err, &perr) || perr.Class != ErrClassPermanent {
		t.Fatalf("恒久的エラーとして区分されていない: %#v", err)
	}
	if !strings.Contains(perr.Message, "同意") {
		t.Errorf("原因が同意ゲートだと分からない: %q", perr.Message)
	}
}

// 同意済みなら送信され、送信記録の import_refs に資料 ID・名称・取り込み日時が残る。
func TestImportSendRecordedWithConsent(t *testing.T) {
	rec := newFakeRecorder()
	a := &scriptedAdapter{scripts: [][]StreamEvent{okScript("候補 JSON")}}
	req := ChatRequest{Model: "m", System: "システム指示",
		Messages:   []Message{{Role: RoleUser, Content: "資料本文"}},
		ImportRefs: testImportRefs, ConsentGiven: true}

	ch, err := StreamRetrying(context.Background(), a, req, StreamOptions{Recorder: rec})
	if err != nil {
		t.Fatalf("同意済みの送信が開始できない: %v", err)
	}
	collectEvents(t, ch)

	if a.calls != 1 {
		t.Fatalf("同意済みなのに送信されていない: %d 回", a.calls)
	}
	if len(rec.sends) != 1 {
		t.Fatalf("送信記録の件数が違う: %d", len(rec.sends))
	}
	got := rec.sends[0].ImportRefs
	if len(got) != 1 {
		t.Fatalf("import_refs が記録されていない: %+v", got)
	}
	if got[0].ID != "IMP-001" || got[0].SourceName != "現行業務フロー.docx" ||
		!got[0].ImportedAt.Equal(testImportRefs[0].ImportedAt) {
		t.Errorf("import_refs の内容が違う: %+v", got[0])
	}
}

// ImportRefs は記録専用のメタデータであり、送信本文（プロバイダへ送る中身）に混ざらない。
func TestImportRefsNotInPrompt(t *testing.T) {
	rec := newFakeRecorder()
	a := &scriptedAdapter{scripts: [][]StreamEvent{okScript("候補 JSON")}}
	req := ChatRequest{Model: "m", System: "システム指示",
		Messages:   []Message{{Role: RoleUser, Content: "資料本文"}},
		ImportRefs: testImportRefs, ConsentGiven: true}

	ch, _ := StreamRetrying(context.Background(), a, req, StreamOptions{Recorder: rec})
	collectEvents(t, ch)

	prompt := BuildPrompt(req)
	for _, forbidden := range []string{"IMP-001", "現行業務フロー.docx"} {
		if strings.Contains(prompt, forbidden) {
			t.Errorf("送信本文に記録専用メタデータが混ざっている（%q）: %q", forbidden, prompt)
		}
	}
	if !strings.Contains(prompt, "資料本文") {
		t.Errorf("送信本文が組み立てられていない: %q", prompt)
	}
}

// 同意ゲートは取り込み資料を含まない通常の対話・生成の送信を止めない。
func TestConsentGateDoesNotAffectNormalSend(t *testing.T) {
	a := &scriptedAdapter{scripts: [][]StreamEvent{okScript("本文")}}

	ch, err := StreamRetrying(context.Background(), a,
		ChatRequest{Model: "m", Messages: []Message{{Role: RoleUser, Content: "質問への回答"}}},
		StreamOptions{})
	if err != nil {
		t.Fatalf("通常の送信が止められた: %v", err)
	}
	collectEvents(t, ch)
	if a.calls != 1 {
		t.Errorf("通常の送信が届いていない: %d 回", a.calls)
	}
}

// 同意ゲートの判定表（ImportRefs の有無 × ConsentGiven）。
func TestCheckImportConsentTable(t *testing.T) {
	cases := []struct {
		name    string
		req     ChatRequest
		blocked bool
	}{
		{"資料なし・同意なし", ChatRequest{}, false},
		{"資料なし・同意あり", ChatRequest{ConsentGiven: true}, false},
		{"資料あり・同意なし", ChatRequest{ImportRefs: testImportRefs}, true},
		{"資料あり・同意あり", ChatRequest{ImportRefs: testImportRefs, ConsentGiven: true}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkImportConsent(c.req, ProviderAnthropic)
			if c.blocked && err == nil {
				t.Error("送信を止めるべき組み合わせで通過した")
			}
			if !c.blocked && err != nil {
				t.Errorf("送信を止めるべきでない組み合わせで止めた: %v", err)
			}
		})
	}
}
