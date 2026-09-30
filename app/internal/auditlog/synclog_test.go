package auditlog

// 単体テスト（同期の記録の形式。認証情報を記録に残さないことを含む）。
// 実行: make -C app test-unit

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
	"time"
)

// memSink はメモリ上の追記先（保存キューを介さずに記録の形式だけを見る）。
type memSink struct {
	lines map[string][]string
}

func (m *memSink) AppendFile(rel string, line []byte) error {
	if m.lines == nil {
		m.lines = map[string][]string{}
	}
	m.lines[rel] = append(m.lines[rel], strings.TrimRight(string(line), "\n"))
	return nil
}

// 月別 × 作業者別のファイルへ追記し、作業者・日時は記録側が確定させる。
func TestRecordSyncWritesMonthlyPerAuthorFile(t *testing.T) {
	sink := &memSink{}
	l, err := New(sink, "k.sato@example.co.jp")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	err = l.RecordSync(SyncRecord{
		At: at, Author: "y.suzuki@example.co.jp", // 他人を騙れない（記録側が上書きする）
		Op: "incorporate", RemoteKind: "folder", RemoteLocation: "/Volumes/share/proj.git",
		Summary: map[string]int{"requirements": 3, "decisions": 1}, Conflicts: 2, Result: "ok",
	})
	if err != nil {
		t.Fatalf("記録に失敗: %v", err)
	}
	want := FileName(DirSyncLog, at, "k.sato@example.co.jp")
	lines := sink.lines[want]
	if len(lines) != 1 {
		t.Fatalf("記録先が違う: %+v（期待 %s）", sink.lines, want)
	}
	if !strings.HasPrefix(want, "audit/sync-log/") {
		t.Errorf("保存先が同期の記録の置き場所（audit/sync-log/）と違う: %s", want)
	}

	var got SyncRecord
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("記録を解釈できない: %v", err)
	}
	if got.Author != "k.sato@example.co.jp" {
		t.Errorf("作業者が記録側で確定していない: %q", got.Author)
	}
	if !got.At.Equal(at) || got.Op != "incorporate" || got.Result != "ok" || got.Conflicts != 2 {
		t.Errorf("主フィールドが違う: %+v", got)
	}
	if got.Summary["requirements"] != 3 || got.Summary["decisions"] != 1 {
		t.Errorf("区分ごとの件数が違う: %+v", got.Summary)
	}

	// 操作・結果の無い記録は受け付けない（黙って壊れた記録を残さない）。
	if err := l.RecordSync(SyncRecord{At: at, Result: "ok"}); err == nil {
		t.Error("操作の無い記録が受理された")
	}
	if err := l.RecordSync(SyncRecord{At: at, Op: "publish"}); err == nil {
		t.Error("結果の無い記録が受理された")
	}
}

// 同期先の所在に認証情報が混ざっていても記録へ残さない（二重の防御）。
func TestRecordSyncStripsCredentials(t *testing.T) {
	sink := &memSink{}
	l, _ := New(sink, "k.sato@example.co.jp")
	if err := l.RecordSync(SyncRecord{
		At: time.Now().UTC(), Op: "publish", RemoteKind: "git_internal",
		RemoteLocation: "https://user:ghp_abcdefghijklmnopqrstuvwxyz0123456789@git.example.co.jp/team/proj.git",
		Result:         "ok",
	}); err != nil {
		t.Fatal(err)
	}
	for _, lines := range sink.lines {
		for _, line := range lines {
			if strings.Contains(line, "ghp_abcdefghijklmnopqrstuvwxyz0123456789") || strings.Contains(line, "user:") {
				t.Fatalf("認証情報が記録に残った: %s", line)
			}
			if !strings.Contains(line, "git.example.co.jp/team/proj.git") {
				t.Errorf("所在そのものが失われた: %s", line)
			}
		}
	}
}

// 記録の型が認証情報を持てる形になっていない（構造的非出力）。
//
// 「所在に資格情報を混ぜない」は実行時の除去で担保するが、**トークン・鍵を入れる置き場所を型に作らない**
// ことを構造として固定する（フィールドが増えたときに機械で気づく）。
func TestSyncRecordHasNoCredentialField(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("パッケージを解析できない: %v", err)
	}
	forbidden := []string{"key", "token", "secret", "password", "passphrase", "credential", "cred"}
	fields := 0
	for _, file := range pkgs["auditlog"].Files {
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok || spec.Name.Name != "SyncRecord" {
				return true
			}
			st, ok := spec.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, f := range st.Fields.List {
				for _, name := range f.Names {
					fields++
					lower := strings.ToLower(name.Name)
					for _, word := range forbidden {
						if strings.Contains(lower, word) {
							t.Errorf("同期の記録に認証情報を置ける名前のフィールドがある: %s", name.Name)
						}
					}
				}
			}
			return false
		})
	}
	if fields < 6 {
		t.Fatalf("SyncRecord の走査が不足している: %d フィールド", fields)
	}
}
