package auditlog

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type fakeSink struct {
	lines map[string][][]byte
}

func newFakeSink() *fakeSink { return &fakeSink{lines: map[string][][]byte{}} }

func (f *fakeSink) AppendFile(rel string, line []byte) error {
	f.lines[rel] = append(f.lines[rel], append([]byte(nil), line...))
	return nil
}

// 利用者 ID のファイル名安全化は、異なる ID が同一ファイル名に衝突しないこと。
func TestSafeAuthorFileName(t *testing.T) {
	if got, want := SafeAuthorFileName("k.sato@example.co.jp"), "k%2esato%40example%2eco%2ejp"; got != want {
		t.Errorf("安全化表記が違う: got %q, want %q", got, want)
	}
	ids := []string{
		"k.sato@example.co.jp",
		"k-sato@example.co.jp",
		"ksato@example.co.jp",
		"k.sato@example.co.jp2",
		`example\ksato@example.co.jp`,
		"k/sato@example.co.jp",
	}
	seen := map[string]string{}
	for _, id := range ids {
		name := SafeAuthorFileName(id)
		if prev, dup := seen[name]; dup {
			t.Errorf("異なる利用者 ID が同じファイル名になった: %q と %q → %q", prev, id, name)
		}
		seen[name] = id
		for _, bad := range []string{"/", `\`, ":", "*", "?", `"`, "<", ">", "|", " "} {
			if strings.Contains(name, bad) {
				t.Errorf("ファイル名に使えない文字が残った: %q（%q）", bad, name)
			}
		}
	}
}

// 月別 × 作業者別。月は UTC で決める。
func TestFileName(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	at := time.Date(2026, 9, 1, 5, 0, 0, 0, jst) // UTC では 2026-08-31
	got := FileName(DirHistory, at, "k.sato@example.co.jp")
	want := "audit/history/2026-08.k%2esato%40example%2eco%2ejp.ndjson"
	if got != want {
		t.Errorf("ファイル名が違う: got %q, want %q", got, want)
	}
}

// レコードの主フィールドが決めた形式と一致すること。
// キー本体・キー参照名を持つフィールドを持たない。
func TestRecordSchemaMatchesDesign(t *testing.T) {
	at := time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)
	change := ChangeRecord{At: at, Author: "k.sato@example.co.jp", Target: "DEC-001",
		Change: ChangeCreated, Before: "a", After: "b", Evidence: "S-0001#utt-00012", OSUser: "ksato"}
	assertJSONKeys(t, change, []string{"at", "author", "target", "change", "before", "after", "evidence", "os_user"})

	send := AISendRecord{ID: "b1f0", At: at, Author: "k.sato@example.co.jp", Provider: "anthropic",
		Model: "claude", Session: "S-0001", Prompt: "本文", Included: []string{"FR-XXX-001"},
		ImportRefs: []ImportRef{{ID: "IMP-001", SourceName: "資料.docx", ImportedAt: at}}}
	send.SetTokens(10, 20, 5)
	assertJSONKeys(t, send, []string{"id", "at", "author", "provider", "model", "session", "prompt",
		"included", "import_refs", "tokens_in", "tokens_out", "tokens_reasoning"})
}

func assertJSONKeys(t *testing.T, v any, want []string) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("組み立てに失敗: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("解釈に失敗: %v", err)
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("%T に %q がありません: %s", v, k, b)
		}
		delete(m, k)
	}
	for k := range m {
		t.Errorf("%T に設計にないフィールド %q があります", v, k)
	}
	lower := strings.ToLower(string(b))
	for _, forbidden := range []string{"\"key\"", "key_ref", "secret", "api_key", "apikey"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("監査記録に %q が含まれます（キー本体・キー参照名は記録しない）: %s", forbidden, b)
		}
	}
}

// トークン実績は欠測（API が返さない）と 0 を区別する（集計で欠測の件数を別に示すため）。
func TestTokensOmittedWhenMissing(t *testing.T) {
	b, err := json.Marshal(AISendRecord{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "tokens_in") {
		t.Errorf("欠測のトークン実績がキーごと省略されていない: %s", b)
	}
	rec := AISendRecord{Prompt: "x"}
	rec.SetTokens(0, 0, 0)
	b, err = json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"tokens_in":0`) {
		t.Errorf("実績 0 が記録されていない: %s", b)
	}
}

func TestRecordChangeWritesOneLineToAuthorFile(t *testing.T) {
	sink := newFakeSink()
	l, err := New(sink, "k.sato@example.co.jp")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)
	if err := l.RecordChange(ChangeRecord{At: at, Target: "ISS-001", Change: ChangeStatusChanged,
		Before: "open\n途中で改行", After: "closed"}); err != nil {
		t.Fatalf("記録に失敗: %v", err)
	}
	name := FileName(DirHistory, at, "k.sato@example.co.jp")
	lines := sink.lines[name]
	if len(lines) != 1 {
		t.Fatalf("行数が違う（%s）: %d", name, len(lines))
	}
	line := string(lines[0])
	if !strings.HasSuffix(line, "\n") {
		t.Error("NDJSON の行が改行で終わっていない")
	}
	if strings.Count(line, "\n") != 1 {
		t.Errorf("本文の改行で行が分割されている（行単位の自己完結が崩れる）: %q", line)
	}
}

// 記録する作業者は Logger の利用者 ID で上書きする（他人名義の記録を作れない）。
func TestRecordOverwritesAuthor(t *testing.T) {
	sink := newFakeSink()
	l, _ := New(sink, "k.sato@example.co.jp")
	at := time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)
	if err := l.RecordChange(ChangeRecord{At: at, Author: "someone.else@example.co.jp",
		Target: "DEC-001", Change: ChangeCreated}); err != nil {
		t.Fatal(err)
	}
	var rec ChangeRecord
	if err := json.Unmarshal(sink.lines[FileName(DirHistory, at, "k.sato@example.co.jp")][0], &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Author != "k.sato@example.co.jp" {
		t.Errorf("author が上書きされていない: %q", rec.Author)
	}
}

func TestRecordValidatesRequiredFields(t *testing.T) {
	sink := newFakeSink()
	l, _ := New(sink, "k.sato@example.co.jp")
	if err := l.RecordChange(ChangeRecord{Change: ChangeCreated}); err == nil {
		t.Error("target なしの変更履歴が受理された")
	}
	if err := l.RecordChange(ChangeRecord{Target: "DEC-001"}); err == nil {
		t.Error("change なしの変更履歴が受理された")
	}
	if _, err := l.RecordAISend(AISendRecord{Prompt: "x"}); err == nil {
		t.Error("provider / model なしの送信記録が受理された")
	}
	if _, err := l.RecordAISend(AISendRecord{Provider: "anthropic", Model: "m"}); err == nil {
		t.Error("送信本文なしの送信記録が受理された")
	}
	if err := l.RecordAIUsage("", 1, 2, 3); err == nil {
		t.Error("送信 ID なしの実績行が受理された")
	}
	if _, err := New(nil, "k.sato@example.co.jp"); err == nil {
		t.Error("書き込み先なしの Logger が作れた")
	}
	if _, err := New(sink, ""); err == nil {
		t.Error("利用者 ID なしの Logger が作れた")
	}
}

func TestMonthInPeriod(t *testing.T) {
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)
	if !monthInPeriod("2026-08.a.ndjson", from, to) {
		t.Error("期間内の月が除外された")
	}
	if monthInPeriod("2026-07.a.ndjson", from, to) {
		t.Error("期間外（前月）が含まれた")
	}
	if monthInPeriod("2026-09.a.ndjson", from, to) {
		t.Error("期間外（翌月）が含まれた")
	}
	if !monthInPeriod("2026-09.a.ndjson", time.Time{}, time.Time{}) {
		t.Error("期間無制限で除外された")
	}
	if monthInPeriod("broken.ndjson", time.Time{}, time.Time{}) {
		t.Error("月として解釈できない名前が含まれた")
	}
}
