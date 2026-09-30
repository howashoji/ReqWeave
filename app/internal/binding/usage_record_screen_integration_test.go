//go:build integration

// 結合テスト（トークン消費実績のバインディング）。

package binding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// appendSend は ai-log へ送信 1 件を追記する（tokens が nil のときは実績を持たない = 欠測）。
//
// appendConsumption（実績つき固定）と違い、欠測とプロバイダ・セッションを指定できる。
func appendSend(t *testing.T, root, author, id string, at time.Time, provider, session string, tokens *int) {
	t.Helper()
	store, err := projectstore.Open(root, projectstore.Author{AuthorID: author, DisplayName: "追記"})
	if err != nil {
		t.Fatalf("プロジェクトを開けない: %v", err)
	}
	defer store.Close()

	send := auditlog.AISendRecord{ID: id, At: at, Author: author, Provider: provider,
		Model: "claude-opus-5", Session: session, Prompt: "[user]\n質問"}
	if tokens != nil {
		send.SetTokens(*tokens, 0, 0)
	}
	line, err := json.Marshal(send)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendFile(auditlog.FileName(auditlog.DirAILog, at, author), append(line, '\n')); err != nil {
		t.Fatalf("ai-log へ追記できない: %v", err)
	}
}

func intPtr(v int) *int { return &v }

// プロジェクト単位・対話セッション単位・プロバイダ別の累計が返る（全期間）。
func TestTokenUsageTotalsByProviderAndSession(t *testing.T) {
	a, root := newUsageLimitAPI(t)
	base := time.Date(2026, 7, 3, 9, 0, 0, 0, time.Local)
	appendSend(t, root, "k.sato@example.co.jp", "u-1", base, "anthropic", "S-0001", intPtr(300))
	// 月をまたいだ送信も累計に入る（期間指定のないダッシュボードとの違い）。
	appendSend(t, root, "t.suzuki@example.co.jp", "u-2", base.AddDate(0, 1, 0), "openai", "S-0002", intPtr(200))

	view, err := a.TokenUsage(TokenUsageRequest{})
	if err != nil {
		t.Fatalf("消費実績を取得できない: %v", err)
	}
	if view.Tokens != 500 || view.Sends != 2 {
		t.Errorf("累計が違う: tokens=%d sends=%d", view.Tokens, view.Sends)
	}
	if view.TargetSystemName == "" {
		t.Error("対象システム名が空")
	}
	if len(view.ByProvider) != 2 {
		t.Fatalf("プロバイダ別の内訳が違う: %+v", view.ByProvider)
	}
	// 並びは消費降順（集計層の規約）。
	if view.ByProvider[0].Key != "anthropic" || view.ByProvider[0].Tokens != 300 {
		t.Errorf("プロバイダ別の先頭が違う: %+v", view.ByProvider[0])
	}
	if len(view.BySession) != 2 || view.BySession[0].Key != "S-0001" || view.BySession[0].Tokens != 300 {
		t.Errorf("セッション別の内訳が違う: %+v", view.BySession)
	}
}

// 直近 1 件は API 応答の実績値をそのまま返す（推定値で代用しない）。
func TestTokenUsageLatestIsTheNewestSend(t *testing.T) {
	a, root := newUsageLimitAPI(t)
	base := time.Date(2026, 8, 5, 10, 0, 0, 0, time.Local)
	appendSend(t, root, "k.sato@example.co.jp", "u-1", base, "anthropic", "S-0001", intPtr(300))
	appendSend(t, root, "k.sato@example.co.jp", "u-2", base.Add(2*time.Hour), "openai", "S-0002", intPtr(120))
	appendSend(t, root, "k.sato@example.co.jp", "u-3", base.Add(time.Hour), "anthropic", "S-0001", intPtr(999))

	view, err := a.TokenUsage(TokenUsageRequest{})
	if err != nil {
		t.Fatalf("消費実績を取得できない: %v", err)
	}
	if view.Latest == nil {
		t.Fatal("直近 1 件が返っていない")
	}
	if view.Latest.Provider != "openai" || view.Latest.Session != "S-0002" {
		t.Errorf("直近 1 件が最新の送信でない: %+v", view.Latest)
	}
	if !view.Latest.HasTokens || view.Latest.TokensTotal != 120 {
		t.Errorf("直近 1 件の実績値が違う: %+v", view.Latest)
	}
	want := base.Add(2 * time.Hour).UTC().Format(time.RFC3339)
	if view.Latest.At != want {
		t.Errorf("直近 1 件の日時が違う: %s（期待 %s）", view.Latest.At, want)
	}
}

// 実績が欠測の送信は「実績なし」として区別する（0 と混同しない・黙って落とさない）。
func TestTokenUsageDistinguishesMissingFromZero(t *testing.T) {
	a, root := newUsageLimitAPI(t)
	base := time.Date(2026, 8, 6, 10, 0, 0, 0, time.Local)
	appendSend(t, root, "k.sato@example.co.jp", "u-1", base, "anthropic", "S-0001", intPtr(300))
	appendSend(t, root, "k.sato@example.co.jp", "u-2", base.Add(time.Hour), "anthropic", "S-0001", nil)

	view, err := a.TokenUsage(TokenUsageRequest{})
	if err != nil {
		t.Fatalf("消費実績を取得できない: %v", err)
	}
	if view.Sends != 2 || view.MissingRecords != 1 {
		t.Errorf("欠測の計上が違う: sends=%d missing=%d", view.Sends, view.MissingRecords)
	}
	if view.Tokens != 300 {
		t.Errorf("欠測を 0 として合算していない: %d", view.Tokens)
	}
	if view.Latest == nil || view.Latest.HasTokens {
		t.Fatalf("欠測の直近 1 件が実績ありになっている: %+v", view.Latest)
	}
	if view.Latest.TokensTotal != 0 {
		t.Errorf("欠測なのに推定値が入っている: %+v", view.Latest)
	}
}

// 送信が 1 件も無いプロジェクトでも画面は成立する（直近 1 件は無し）。
func TestTokenUsageWithoutSends(t *testing.T) {
	a, _ := newUsageLimitAPI(t)

	view, err := a.TokenUsage(TokenUsageRequest{})
	if err != nil {
		t.Fatalf("消費実績を取得できない: %v", err)
	}
	if view.Latest != nil || view.Sends != 0 || view.Tokens != 0 {
		t.Errorf("送信の無いプロジェクトの結果が違う: %+v", view)
	}
}

// 消費実績の参照は閲覧権限でもできる（AI を呼ばないため障害中も成立する）。
func TestTokenUsageAllowedForViewer(t *testing.T) {
	a, root := newUsageLimitAPI(t)
	appendSend(t, root, "k.sato@example.co.jp", "u-1",
		time.Date(2026, 8, 7, 10, 0, 0, 0, time.Local), "anthropic", "S-0001", intPtr(150))
	demoteToViewer(t, root, "k.sato@example.co.jp")

	view, err := a.TokenUsage(TokenUsageRequest{})
	if err != nil {
		t.Fatalf("閲覧権限で参照できない: %v", err)
	}
	if view.Tokens != 150 {
		t.Errorf("累計が違う: %d", view.Tokens)
	}
}

// 上限設定時は消費率も返す（消費実績から利用量・上限の画面へ移っても同じ数字を見せるため）。
func TestTokenUsageIncludesLimit(t *testing.T) {
	a, root := newUsageLimitAPI(t)
	appendSend(t, root, "k.sato@example.co.jp", "u-1",
		time.Date(2026, 8, 8, 10, 0, 0, 0, time.Local), "anthropic", "S-0001", intPtr(250))
	if _, err := a.SetUsageLimit(UsageLimitRequest{TokensMax: 1000}); err != nil {
		t.Fatalf("上限を設定できない: %v", err)
	}

	view, err := a.TokenUsage(TokenUsageRequest{})
	if err != nil {
		t.Fatalf("消費実績を取得できない: %v", err)
	}
	if view.LimitTokens == nil || *view.LimitTokens != 1000 {
		t.Fatalf("上限が返っていない: %+v", view.LimitTokens)
	}
	if view.ConsumptionRatio == nil || *view.ConsumptionRatio != 0.25 {
		t.Fatalf("消費率が違う: %+v", view.ConsumptionRatio)
	}
}

// Path 指定では、開いていないプロジェクトも参照できる（メンバーであることが条件）。
func TestTokenUsageByPathRequiresMembership(t *testing.T) {
	a, root := newUsageLimitAPI(t)
	appendSend(t, root, "k.sato@example.co.jp", "u-1",
		time.Date(2026, 8, 9, 10, 0, 0, 0, time.Local), "anthropic", "S-0001", intPtr(180))
	if err := a.CloseDialogueProject(); err != nil {
		t.Fatalf("プロジェクトを閉じられない: %v", err)
	}

	view, err := a.TokenUsage(TokenUsageRequest{Path: root})
	if err != nil {
		t.Fatalf("パス指定で参照できない: %v", err)
	}
	if view.Tokens != 180 || view.Path != root {
		t.Errorf("パス指定の結果が違う: %+v", view)
	}

	// メンバー一覧の無いフォルダは読込エラーで拒否する。
	if _, err := a.TokenUsage(TokenUsageRequest{Path: t.TempDir()}); err == nil {
		t.Fatal("メンバー一覧の無いフォルダを拒否していない")
	}

	// メンバーから外れている場合は「登録されていません」で拒否する。
	removeMember(t, root, "k.sato@example.co.jp")
	_, err = a.TokenUsage(TokenUsageRequest{Path: root})
	if err == nil {
		t.Fatal("メンバー外のプロジェクトを拒否していない")
	}
	if !strings.Contains(err.Error(), "メンバーに登録されていません") {
		t.Errorf("拒否の理由が違う: %v", err)
	}
}

// removeMember は指定の利用者をメンバー一覧から取り除く（オーナーは別に残す）。
func removeMember(t *testing.T, root, authorID string) {
	t.Helper()
	path := filepath.Join(root, projectstore.FileMembers)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	members, err := projectstore.UnmarshalMembers(data)
	if err != nil {
		t.Fatal(err)
	}
	kept := members.Members[:0]
	for _, m := range members.Members {
		if m.AuthorID != authorID {
			kept = append(kept, m)
		}
	}
	members.Members = append(kept, projectstore.Member{
		AuthorID: "owner@example.co.jp", DisplayName: "オーナー",
		Role: projectstore.RoleOwner, AddedAt: time.Now().UTC(), AddedBy: authorID,
	})
	out, err := members.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}
