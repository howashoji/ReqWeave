//go:build integration

// 結合テスト（監査ログ × プロジェクトストアの保存キュー・実ファイル I/O）。
// 実行: make -C app test-integration

package auditlog_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

const (
	sato   = "k.sato@example.co.jp"
	suzuki = "t.suzuki@example.co.jp"
)

func newProject(t *testing.T) *projectstore.Store {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proj")
	s, err := projectstore.CreateProject(root, projectstore.CreateOptions{
		TargetSystemName: "在庫管理システム",
		Author:           projectstore.Author{AuthorID: sato, DisplayName: "佐藤"},
	})
	if err != nil {
		t.Fatalf("プロジェクトを作成できません: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newLogger(t *testing.T, s *projectstore.Store, authorID string) *auditlog.Logger {
	t.Helper()
	l, err := auditlog.New(s, authorID)
	if err != nil {
		t.Fatalf("Logger を作れません: %v", err)
	}
	return l
}

// 月別 × 作業者別ファイルに追記し、読み出しは全作業者ファイルを日時でマージする。
func TestRecordsGoToMonthlyPerAuthorFilesAndMerge(t *testing.T) {
	s := newProject(t)
	other, err := projectstore.Open(s.Root(), projectstore.Author{AuthorID: suzuki, DisplayName: "鈴木"})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	base := time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)
	ls := newLogger(t, s, sato)
	lo := newLogger(t, other, suzuki)

	if err := lo.RecordChange(auditlog.ChangeRecord{At: base.Add(time.Minute), Target: "ISS-001",
		Change: auditlog.ChangeCreated}); err != nil {
		t.Fatal(err)
	}
	if err := ls.RecordChange(auditlog.ChangeRecord{At: base, Target: "DEC-001",
		Change: auditlog.ChangeCreated}); err != nil {
		t.Fatal(err)
	}
	if err := ls.RecordChange(auditlog.ChangeRecord{At: base.Add(2 * time.Minute), Target: "DEC-001",
		Change: auditlog.ChangeStatusChanged, Before: "draft", After: "agreed"}); err != nil {
		t.Fatal(err)
	}

	for _, author := range []string{sato, suzuki} {
		name := auditlog.FileName(auditlog.DirHistory, base, author)
		if _, err := os.Stat(filepath.Join(s.Root(), filepath.FromSlash(name))); err != nil {
			t.Errorf("作業者別ファイルがありません: %s: %v", name, err)
		}
	}

	recs, err := auditlog.ReadChanges(s.Root(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("読み出しに失敗: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("件数が違う: %d", len(recs))
	}
	wantOrder := []string{"DEC-001", "ISS-001", "DEC-001"}
	wantAuthor := []string{sato, suzuki, sato}
	for i, rec := range recs {
		if rec.Target != wantOrder[i] || rec.Author != wantAuthor[i] {
			t.Errorf("%d 件目が違う: %+v", i, rec)
		}
		if i > 0 && rec.At.Before(recs[i-1].At) {
			t.Errorf("日時順にマージされていない: %v < %v", rec.At, recs[i-1].At)
		}
	}
}

// 行単位で自己完結し、壊れた行は読み飛ばせること。
func TestCorruptedLineIsSkipped(t *testing.T) {
	s := newProject(t)
	base := time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)
	l := newLogger(t, s, sato)
	if err := l.RecordChange(auditlog.ChangeRecord{At: base, Target: "DEC-001", Change: auditlog.ChangeCreated}); err != nil {
		t.Fatal(err)
	}
	// 追記途中で切れた行を挟む
	if err := s.AppendFile(auditlog.FileName(auditlog.DirHistory, base, sato), []byte(`{"at":"2026-08-27T05:0`+"\n")); err != nil {
		t.Fatal(err)
	}
	if err := l.RecordChange(auditlog.ChangeRecord{At: base.Add(time.Minute), Target: "ISS-001", Change: auditlog.ChangeCreated}); err != nil {
		t.Fatal(err)
	}

	recs, err := auditlog.ReadChanges(s.Root(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("読み出しに失敗: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("壊れた行の前後が読めていない: %d 件 %+v", len(recs), recs)
	}
	if recs[0].Target != "DEC-001" || recs[1].Target != "ISS-001" {
		t.Errorf("内容が違う: %+v", recs)
	}
}

// 追記は保存キュー経由で直列化される。同時追記で行が壊れないこと。
func TestConcurrentAppendsKeepLinesIntact(t *testing.T) {
	s := newProject(t)
	base := time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)
	l := newLogger(t, s, sato)

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := l.RecordChange(auditlog.ChangeRecord{
				At: base.Add(time.Duration(i) * time.Second), Target: "DEC-001",
				Change: auditlog.ChangeUpdated, After: strings.Repeat("あ", 200),
			})
			if err != nil {
				t.Errorf("記録に失敗: %v", err)
			}
		}(i)
	}
	wg.Wait()

	recs, err := auditlog.ReadChanges(s.Root(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("読み出しに失敗: %v", err)
	}
	if len(recs) != n {
		t.Fatalf("同時追記で行が壊れた: %d 件（期待 %d 件）", len(recs), n)
	}
	for _, rec := range recs {
		if len([]rune(rec.After)) != 200 {
			t.Fatalf("行の内容が壊れている: %d 文字", len([]rune(rec.After)))
		}
	}
}

// AI 送信記録の往復（送信全文に改行を含んでも 1 行に収まること）。
func TestAISendRecordRoundTrip(t *testing.T) {
	s := newProject(t)
	base := time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)
	l := newLogger(t, s, sato)

	rec := auditlog.AISendRecord{
		At: base, Provider: "anthropic", Model: "claude-x", Session: "S-0001",
		Prompt:   "システム指示\n\n利用者の発話",
		Included: []string{"FR-INV-001", "terms.yaml"},
		ImportRefs: []auditlog.ImportRef{{ID: "IMP-001", SourceName: "現行業務.docx",
			ImportedAt: base.Add(-time.Hour)}},
	}
	// 送信直前の記録（実績はまだ無い送信行）。
	id, err := l.RecordAISend(rec)
	if err != nil {
		t.Fatalf("記録に失敗: %v", err)
	}
	if id == "" {
		t.Fatal("送信 ID が採番されていない")
	}
	// 実績受信時の追記（実績行）。
	if err := l.RecordAIUsage(id, 120, 340, 15); err != nil {
		t.Fatalf("実績の記録に失敗: %v", err)
	}

	got, err := auditlog.ReadAISends(s.Root(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("読み出しに失敗: %v", err)
	}
	// 2 行で記録しても、読み出しは ID でマージした 1 件になる。
	if len(got) != 1 {
		t.Fatalf("件数が違う: %d", len(got))
	}
	if got[0].ID != id {
		t.Errorf("送信 ID が一致しない: %q（期待 %q）", got[0].ID, id)
	}
	g := got[0]
	if g.Prompt != rec.Prompt {
		t.Errorf("送信全文が一致しない: %q", g.Prompt)
	}
	if g.Author != sato || g.Provider != "anthropic" || g.Model != "claude-x" || g.Session != "S-0001" {
		t.Errorf("宛先・作業者が一致しない: %+v", g)
	}
	if len(g.Included) != 2 || len(g.ImportRefs) != 1 || g.ImportRefs[0].ID != "IMP-001" {
		t.Errorf("同梱範囲・資料識別が一致しない: %+v", g)
	}
	if g.TokensIn == nil || *g.TokensIn != 120 || g.TokensOut == nil || *g.TokensOut != 340 ||
		g.TokensReasoning == nil || *g.TokensReasoning != 15 {
		t.Errorf("トークン実績が一致しない: %+v", g)
	}

	// 監査データにキー本体・キー参照名が含まれないこと。
	//
	// ai-log は月別ファイルであり、実績行の月は追記した時刻で決まる（RecordAIUsage の at =
	// 記録時刻）。送信月をまたいで実績が確定すると送信行と実績行は別ファイルへ入るため、
	// **1 ファイルではなく ai-log 全体**を対象にする（実行した月や月の境目に結果を左右させない）。
	raw := readAILogAll(t, s.Root())
	// 送信行 + 実績行の 2 行。送信全文の改行で行が増えていないこと。
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("送信全文の改行で行が分割されている: %d 行（期待 2 行）: %q", len(lines), raw)
	}
	for i, line := range lines {
		var probe map[string]any
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			t.Errorf("%d 行目が JSON として解釈できない（行が分割されている）: %v", i+1, err)
		}
	}
	for _, forbidden := range []string{"key_ref", "sk-ant-", "sk-", "AIza"} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("監査データに %q が含まれる", forbidden)
		}
	}
}

// readAILogAll は ai-log ディレクトリの全ファイルを連結して返す（月別ファイルの境界を無視する）。
func readAILogAll(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(auditlog.DirAILog))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ai-log を走査できない: %v", err)
	}
	var b strings.Builder
	files := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("ai-log の %s を読めない: %v", e.Name(), err)
		}
		files++
		b.Write(raw)
	}
	if files == 0 {
		t.Fatal("走査が空振りしている: ai-log にファイルが 1 件も無い")
	}
	return b.String()
}

// 期間指定は月別ファイル名で読むファイルを限定したうえで、レコードの日時でも絞る。
func TestReadChangesFiltersPeriod(t *testing.T) {
	s := newProject(t)
	l := newLogger(t, s, sato)
	july := time.Date(2026, 7, 15, 5, 0, 0, 0, time.UTC)
	august := time.Date(2026, 8, 15, 5, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{july, august} {
		if err := l.RecordChange(auditlog.ChangeRecord{At: at, Target: "DEC-001", Change: auditlog.ChangeCreated}); err != nil {
			t.Fatal(err)
		}
	}
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)
	recs, err := auditlog.ReadChanges(s.Root(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("期間で絞れていない: %d 件 %+v", len(recs), recs)
	}
	if !recs[0].At.Equal(august) {
		t.Errorf("残ったレコードが違う: %v", recs[0].At)
	}
}

// 抽象化層の送信記録が 2 行（送信行 + 実績行）で残り、読み出しで 1 件にマージされる。
func TestSendRecorderBridge(t *testing.T) {
	s := newProject(t)
	l := newLogger(t, s, sato)
	rec := auditlog.NewSendRecorder(l, func(err error) { t.Errorf("実績の追記に失敗: %v", err) })

	id, err := rec.RecordSend(aiprovider.SendRecord{
		Provider: aiprovider.ProviderAnthropic,
		Model:    "claude-test",
		Session:  "S-0001",
		Prompt:   "[system]\n指示\n\n[user]\n質問",
		Included: []string{"FR-INV-001"},
	})
	if err != nil {
		t.Fatalf("送信記録に失敗: %v", err)
	}
	rec.RecordUsage(id, aiprovider.TokenUsage{InputTokens: 11, OutputTokens: 23, ReasoningTokens: 5})

	got, err := auditlog.ReadAISends(s.Root(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("読み出しに失敗: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("マージ後の件数が違う: %d件 %+v", len(got), got)
	}
	g := got[0]
	if g.ID != id || g.Provider != "anthropic" || g.Model != "claude-test" || g.Session != "S-0001" {
		t.Errorf("送信行の内容が違う: %+v", g)
	}
	if g.TokensIn == nil || *g.TokensIn != 11 || g.TokensOut == nil || *g.TokensOut != 23 ||
		g.TokensReasoning == nil || *g.TokensReasoning != 5 {
		t.Errorf("実績がマージされていない: %+v", g)
	}
}

// 実績行の無い送信は欠測として返る（推定値で埋めない）。
func TestSendWithoutUsageIsMissing(t *testing.T) {
	s := newProject(t)
	l := newLogger(t, s, sato)
	rec := auditlog.NewSendRecorder(l, nil)

	if _, err := rec.RecordSend(aiprovider.SendRecord{
		Provider: aiprovider.ProviderOpenAI, Model: "gpt-test", Prompt: "[user]\n質問",
	}); err != nil {
		t.Fatalf("送信記録に失敗: %v", err)
	}

	got, err := auditlog.ReadAISends(s.Root(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("読み出しに失敗: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("件数が違う: %d", len(got))
	}
	if got[0].HasTokens() {
		t.Errorf("実績が無いのに値が入っている: %+v", got[0])
	}
}

// 実績行が翌月へ追記されても、期間指定の読み出しで取りこぼさない。
func TestUsageLineInNextMonthIsMerged(t *testing.T) {
	s := newProject(t)
	sendAt := time.Date(2026, 8, 31, 23, 50, 0, 0, time.UTC)
	usageAt := time.Date(2026, 9, 1, 0, 5, 0, 0, time.UTC)

	send := auditlog.AISendRecord{ID: "send-x", At: sendAt, Author: sato,
		Provider: "anthropic", Model: "claude-test", Prompt: "[user]\n質問"}
	usage := auditlog.AISendRecord{ID: "send-x", At: usageAt, Author: sato}
	usage.SetTokens(7, 9, 0)
	for _, rec := range []struct {
		at  time.Time
		val auditlog.AISendRecord
	}{{sendAt, send}, {usageAt, usage}} {
		line, err := json.Marshal(rec.val)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.AppendFile(auditlog.FileName(auditlog.DirAILog, rec.at, sato), append(line, '\n')); err != nil {
			t.Fatalf("追記に失敗: %v", err)
		}
	}

	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)
	got, err := auditlog.ReadAISends(s.Root(), from, to)
	if err != nil {
		t.Fatalf("読み出しに失敗: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("件数が違う: %d %+v", len(got), got)
	}
	if got[0].TokensIn == nil || *got[0].TokensIn != 7 {
		t.Errorf("翌月の実績行がマージされていない: %+v", got[0])
	}
}

// appendSend は ai-log へ 1 行追記する（送信行・実績行のどちらも）。
func appendSend(t *testing.T, s *projectstore.Store, at time.Time, author string, rec auditlog.AISendRecord) {
	t.Helper()
	line, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendFile(auditlog.FileName(auditlog.DirAILog, at, author), append(line, '\n')); err != nil {
		t.Fatalf("追記に失敗: %v", err)
	}
}

// snapshotFiles はプロジェクトフォルダ配下の相対パス → 内容ハッシュを返す
// （集計が副作用を持たないことの確認用）。
func snapshotFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = fmt.Sprintf("%x", sha256.Sum256(b))
		return nil
	})
	if err != nil {
		t.Fatalf("ファイル一覧を取得できません: %v", err)
	}
	return out
}

// usageFixture は 8 月（期間内 2 件・うち 1 件は実績行が 9 月へ追記）と 9 月（期間外 1 件）を書き込む。
func usageFixture(t *testing.T, s *projectstore.Store) (from, to time.Time) {
	t.Helper()
	jst := time.FixedZone("JST", 9*60*60)
	augA := time.Date(2026, 8, 10, 12, 0, 0, 0, jst)
	augB := time.Date(2026, 8, 31, 23, 50, 0, 0, jst)
	usageB := time.Date(2026, 9, 1, 0, 5, 0, 0, jst)
	sep := time.Date(2026, 9, 2, 10, 0, 0, 0, jst)

	a := auditlog.AISendRecord{ID: "u1", At: augA, Author: sato, Provider: "anthropic",
		Model: "claude-test", Session: "S-0001", Prompt: "[user]\n質問"}
	a.SetTokens(10, 20, 0)
	appendSend(t, s, augA, sato, a)

	// 送信行は 8 月・実績行は 9 月（月をまたぐ追記）
	b := auditlog.AISendRecord{ID: "u2", At: augB, Author: suzuki, Provider: "openai",
		Model: "gpt-test", Session: "S-0002", Prompt: "[user]\n質問"}
	appendSend(t, s, augB, suzuki, b)
	bu := auditlog.AISendRecord{ID: "u2", At: usageB, Author: suzuki}
	bu.SetTokens(3, 4, 5)
	appendSend(t, s, usageB, suzuki, bu)

	// 期間外（9/2）
	c := auditlog.AISendRecord{ID: "u3", At: sep, Author: sato, Provider: "google",
		Model: "gemini-test", Session: "S-0003", Prompt: "[user]\n質問"}
	c.SetTokens(100, 200, 0)
	appendSend(t, s, sep, sato, c)

	return time.Date(2026, 8, 1, 0, 0, 0, 0, jst),
		time.Date(2026, 8, 31, 23, 59, 59, 999999999, jst)
}

// 実ファイルからの期間集計。翌月へ追記された実績行も取りこぼさない。
func TestAggregateUsageOverRealFiles(t *testing.T) {
	s := newProject(t)
	from, to := usageFixture(t, s)

	got, err := auditlog.AggregateUsage(s.Root(), from, to)
	if err != nil {
		t.Fatalf("集計に失敗: %v", err)
	}
	// 8 月分のみ: u1（10+20）+ u2（3+4+5。実績行は 9 月ファイル）= 42
	if want := (auditlog.TokenTotals{In: 13, Out: 24, Reasoning: 5, Total: 42}); got.Tokens != want {
		t.Errorf("期間内の合計が違う: got %+v, want %+v", got.Tokens, want)
	}
	if got.Sends != 2 || got.Missing != 0 {
		t.Errorf("件数が違う: sends=%d missing=%d, want 2 / 0", got.Sends, got.Missing)
	}
	if len(got.ByAuthor) != 2 || got.ByAuthor[0].Key != sato || got.ByAuthor[0].Tokens.Total != 30 {
		t.Errorf("作業者別の内訳が違う: %+v", got.ByAuthor)
	}
	if len(got.ByProvider) != 2 || got.ByProvider[0].Key != "anthropic" {
		t.Errorf("プロバイダ別の内訳が違う: %+v", got.ByProvider)
	}
}

// 上限判定用の全期間累計は同一実装（AggregateUsage）から得る。
func TestTotalUsageIsAllTime(t *testing.T) {
	s := newProject(t)
	usageFixture(t, s)

	got, err := auditlog.TotalUsage(s.Root())
	if err != nil {
		t.Fatalf("累計の集計に失敗: %v", err)
	}
	// 42（8 月）+ 300（9/2）= 342
	if got.Tokens.Total != 342 {
		t.Errorf("全期間累計が違う: got %d, want 342", got.Tokens.Total)
	}
	if got.Sends != 3 {
		t.Errorf("全期間の送信件数が違う: got %d, want 3", got.Sends)
	}
}

// 集計値を永続化しない（集計でファイルが増減・変更しないこと）。
func TestAggregateUsageDoesNotWriteAnything(t *testing.T) {
	s := newProject(t)
	from, to := usageFixture(t, s)
	if err := s.Close(); err != nil {
		t.Fatalf("保存キューを閉じられません: %v", err)
	}

	before := snapshotFiles(t, s.Root())
	if len(before) == 0 {
		t.Fatal("プロジェクトフォルダにファイルが無い（フィクスチャが効いていない）")
	}
	if _, err := auditlog.AggregateUsage(s.Root(), from, to); err != nil {
		t.Fatalf("集計に失敗: %v", err)
	}
	if _, err := auditlog.TotalUsage(s.Root()); err != nil {
		t.Fatalf("累計の集計に失敗: %v", err)
	}
	after := snapshotFiles(t, s.Root())

	for path, hash := range before {
		got, ok := after[path]
		if !ok {
			t.Errorf("集計でファイルが消えた: %s", path)
			continue
		}
		if got != hash {
			t.Errorf("集計でファイルが書き換わった: %s", path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("集計でファイルが増えた: %s", path)
		}
	}
}

// 実績行の無い送信は欠測として件数に出す（0 と区別する）。
func TestAggregateUsageCountsMissingUsageLines(t *testing.T) {
	s := newProject(t)
	jst := time.FixedZone("JST", 9*60*60)
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, jst)
	appendSend(t, s, at, sato, auditlog.AISendRecord{ID: "m1", At: at, Author: sato,
		Provider: "anthropic", Model: "claude-test", Session: "S-0001", Prompt: "[user]\n質問"})

	got, err := auditlog.TotalUsage(s.Root())
	if err != nil {
		t.Fatalf("集計に失敗: %v", err)
	}
	if got.Sends != 1 || got.Missing != 1 || got.Tokens.Total != 0 {
		t.Errorf("欠測の扱いが違う: sends=%d missing=%d total=%d, want 1 / 1 / 0",
			got.Sends, got.Missing, got.Tokens.Total)
	}
}
