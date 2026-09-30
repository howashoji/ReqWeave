package projectstore

import (
	"strings"
	"testing"
	"time"
)

func testSession() Session {
	return Session{ID: "S-0001", Type: SessionOwner, Phase: "requirements",
		StartedAt: time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC), Author: "k.sato@example.co.jp"}
}

// 担当者セッションは作成した作業者の利用者 ID を必ず持つ（セッションは作成した作業者が所有する）。
func TestSessionValidateRequiresAuthorForOwner(t *testing.T) {
	s := testSession()
	if err := s.Validate(); err != nil {
		t.Fatalf("正しいセッションが拒否された: %v", err)
	}
	s.Author = ""
	if err := s.Validate(); err == nil {
		t.Error("作業者なしの担当者セッションが受理された")
	}
	// ステークホルダーセッションは respondent 表示のため作業者を持たない。
	s.Type = SessionStakeholder
	if err := s.Validate(); err != nil {
		t.Errorf("ステークホルダーセッションが拒否された: %v", err)
	}
	s.ID = "0001"
	if err := s.Validate(); err == nil {
		t.Error("ID 形式が不正なセッションが受理された")
	}
}

// セッションファイルの書式でフロントマター + 発話ブロックを往復できる。
func TestParseSessionRoundTrip(t *testing.T) {
	sess := testSession()
	data := sess.marshalHeader()
	utterances := []Utterance{
		{ID: "utt-00001", Speaker: SpeakerAgent, At: sess.StartedAt, Status: UtteranceCompleted,
			Body: "質問です。\n\n背景説明も入ります。"},
		{ID: "utt-00002", Speaker: SpeakerUser, At: sess.StartedAt.Add(time.Minute),
			Status: UtteranceInterrupted, Body: "途中まで"},
	}
	for _, u := range utterances {
		data = append(data, u.marshalBlock()...)
	}

	got, gotUtterances, err := parseSession(data)
	if err != nil {
		t.Fatalf("解釈に失敗: %v", err)
	}
	if got.ID != sess.ID || got.Type != sess.Type || got.Phase != sess.Phase || got.Author != sess.Author {
		t.Errorf("フロントマターが一致しない: %+v", got)
	}
	if !got.StartedAt.Equal(sess.StartedAt) {
		t.Errorf("開始日時が一致しない: %v", got.StartedAt)
	}
	if len(gotUtterances) != 2 {
		t.Fatalf("発話数が違う: %d", len(gotUtterances))
	}
	for i, want := range utterances {
		g := gotUtterances[i]
		if g.ID != want.ID || g.Speaker != want.Speaker || g.Status != want.Status || g.Body != want.Body {
			t.Errorf("発話 %d が一致しない: %+v（期待 %+v）", i, g, want)
		}
		if !g.At.Equal(want.At) {
			t.Errorf("発話 %d の日時が一致しない: %v", i, g.At)
		}
	}
}

// 発話本文に見出し風の行（### で始まる Markdown）が含まれても発話の区切りと誤認しない。
func TestParseSessionKeepsMarkdownHeadingsInBody(t *testing.T) {
	sess := testSession()
	data := sess.marshalHeader()
	body := "以下は本文です。\n\n### 見出しに見える行\n\n- speaker: これはメタ行ではありません"
	data = append(data, Utterance{ID: "utt-00001", Speaker: SpeakerAgent, At: sess.StartedAt,
		Status: UtteranceCompleted, Body: body}.marshalBlock()...)

	_, got, err := parseSession(data)
	if err != nil {
		t.Fatalf("解釈に失敗: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("発話数が違う: %d", len(got))
	}
	if !strings.Contains(got[0].Body, "### 見出しに見える行") {
		t.Errorf("本文の見出しが失われた: %q", got[0].Body)
	}
}

// 発話 ID は utt-nnnnn（5 桁ゼロ埋め）。参照は S-nnnn#utt-nnnnn。
func TestUtteranceIDFormat(t *testing.T) {
	u := Utterance{ID: "utt-00012", Speaker: SpeakerAgent, At: time.Now(), Status: UtteranceCompleted, Body: "x"}
	if !strings.Contains(string(u.marshalBlock()), "### utt-00012") {
		t.Errorf("発話 ID の書式が違う: %s", u.marshalBlock())
	}
	if got := UtteranceRef("S-0003", "utt-00012"); got != "S-0003#utt-00012" {
		t.Errorf("参照形式が違う: %s", got)
	}
	if n, ok := parseUtteranceHeading("### utt-00012"); !ok || n != 12 {
		t.Errorf("見出しから番号を取り出せない: %d %v", n, ok)
	}
	if _, ok := parseUtteranceHeading("### 見出し"); ok {
		t.Error("見出しでない行を発話と誤認した")
	}
}
