//go:build integration

// 結合テスト（エラーの分類ごとに定めた「動作ログへ記録する項目」× 実動作ログ）。
//
// 見るのは 2 つだけ:
//   - 分類ごとに**記録があり、表の項目が入っている**こと
//   - **記録してはいけないもの**（パスコード・キー・絶対パス・業務情報）が入っていないこと
//
// 表示文言（画面）は各画面のテストが見る。ここは記録だけを見る。

package binding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// logLine は動作ログの 1 行（event と付随項目だけを見る）。
type logLine struct {
	Event  string            `json:"event"`
	Fields map[string]string `json:"fields"`
}

// findLogLine は指定の事象の最後の 1 行を返す（無ければテストを失敗させる）。
func findLogLine(t *testing.T, dir, event string) logLine {
	t.Helper()
	var found *logLine
	for _, raw := range strings.Split(appLogBody(t, dir), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var line logLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("動作ログを JSON Lines として読めない: %v（%s）", err, raw)
		}
		if line.Event == event {
			copied := line
			found = &copied
		}
	}
	if found == nil {
		t.Fatalf("動作ログに %q が無い:\n%s", event, appLogBody(t, dir))
	}
	return *found
}

// expectFields は記録項目の一致を確かめる。
func expectFields(t *testing.T, line logLine, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if got := line.Fields[k]; got != v {
			t.Errorf("%s の項目 %q が %q（期待 %q）", line.Event, k, got, v)
		}
	}
}

// 利用量上限系: 対象操作・累計消費・上限値。
func TestUsageBlockedIsLogged(t *testing.T) {
	stub := &usageStub{streamingStub: streamingStub{scripts: []string{dialogueQuestion}}}
	a, root := openWithUsageLimit(t, stub)
	dir := attachAppLog(t, a)

	appendConsumption(t, root, "k.sato@example.co.jp", time.Now().UTC(), 5000, "SEND-0001")
	setLimit(t, a, 1000, nil)

	sess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AskNextQuestion(sess.ID); err == nil {
		t.Fatal("上限に達しているのに AI 呼び出しが始まった")
	}
	line := findLogLine(t, dir, eventUsageBlocked)
	expectFields(t, line, map[string]string{
		"operation": "次の質問の生成", "consumed_tokens": "5000", "limit_tokens": "1000",
	})
	// 発話・プロンプトは残さない。
	if strings.Contains(appLogBody(t, dir), "在庫管理") {
		t.Error("動作ログに業務情報が出た")
	}
}

// 受け渡し系: ファイル形式版・失敗種別（＋質問票 ID）。**パスコードは記録しない**。
func TestExchangeFailureIsLogged(t *testing.T) {
	src, _ := issueForRespond(t)
	a := newRespondAPI(t)
	dir := attachAppLog(t, a)

	const wrong = "WrongPasscode99"
	if _, err := a.OpenQuestionnaireFile(src, wrong); err == nil {
		t.Fatal("誤ったパスコードで開けた")
	}
	line := findLogLine(t, dir, eventExchangeFailed)
	expectFields(t, line, map[string]string{
		"op": exchangeOpOpenIssue, "kind": exchangeKindPasscode, "format_version": "1.0",
	})
	if !strings.HasPrefix(line.Fields["questionnaire_id"], "QS-") {
		t.Errorf("質問票 ID が記録されていない: %+v", line.Fields)
	}
	body := appLogBody(t, dir)
	// 試したパスコード・ファイルのパスは残さない（パスコードはどこにも保存しない）。
	if strings.Contains(body, wrong) || strings.Contains(body, src) {
		t.Errorf("記録してはいけない値が動作ログに出た:\n%s", body)
	}
}

// 取り込み系: 資料 ID・失敗種別・検出形式。
func TestImportFailureIsLogged(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	dir := attachAppLog(t, a)

	// 対象外の形式（資料 ID はまだ無い）。
	unsupported := filepath.Join(t.TempDir(), "会議メモ.rtf")
	if err := os.WriteFile(unsupported, []byte("本文"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ImportFile(unsupported, "material"); err == nil {
		t.Fatal("対象外の形式を取り込めた")
	}
	line := findLogLine(t, dir, eventImportFailed)
	expectFields(t, line, map[string]string{"kind": importKindUnsupported, "format": "rtf"})
	if _, ok := line.Fields["import_id"]; ok {
		t.Errorf("まだ存在しない資料 ID を記録した: %+v", line.Fields)
	}
	// ファイル名・パス・本文は残さない。
	if body := appLogBody(t, dir); strings.Contains(body, "会議メモ") || strings.Contains(body, unsupported) {
		t.Errorf("記録してはいけない値が動作ログに出た:\n%s", body)
	}
}

// 共同作業系: 対象・失敗種別。**押せるかどうかの問い合わせでは記録しない**。
func TestCollabDenialIsLogged(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	demoteToViewer(t, root, "k.sato@example.co.jp")
	dir := attachAppLog(t, a)

	// 押せるかどうかの問い合わせ（画面を描くたびに走る）では記録しない。
	if _, err := a.CurrentPermission(); err != nil {
		t.Fatal(err)
	}
	for _, event := range appLogEvents(t, dir) {
		if event == eventCollabDenied {
			t.Fatal("押せるかどうかの問い合わせで拒否が記録された（実際に試した操作が埋もれる）")
		}
	}

	// 実際に試した操作は記録する。
	if _, err := a.ImportClipboardText("本文", "material"); err == nil {
		t.Fatal("閲覧権限で取り込めた")
	}
	line := findLogLine(t, dir, eventCollabDenied)
	expectFields(t, line, map[string]string{"target": "資料の取り込み", "kind": collabKindRole})
	// 利用者 ID は残さない（予約者のもの以外）。
	if body := appLogBody(t, dir); strings.Contains(body, "k.sato@example.co.jp") {
		t.Errorf("利用者 ID が動作ログに出た:\n%s", body)
	}
}

// データ系: 対象パス（プロジェクト内相対）・失敗種別。**絶対パスは記録しない**。
func TestProjectDataFailureIsLogged(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, root := newDialogueAPI(t, stub)
	dir := attachAppLog(t, a)

	// project.yaml を壊す（形式不整合）。
	if err := os.WriteFile(filepath.Join(root, projectstore.FileProject), []byte("これは YAML の\x00破片"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.OpenDialogueProject(root); err == nil {
		t.Fatal("壊れたプロジェクトを開けた")
	}
	line := findLogLine(t, dir, eventProjectData)
	expectFields(t, line, map[string]string{"kind": dataKindMalformed, "path": projectstore.FileProject})
	// 絶対パス（利用者名を含む端末固有情報）は残さない。
	if body := appLogBody(t, dir); strings.Contains(body, root) {
		t.Errorf("絶対パスが動作ログに出た:\n%s", body)
	}
}

// 走査が空振りしていないことの番人（記録の口が消えたら気づく）。
func TestFailureEventNamesAreDistinct(t *testing.T) {
	names := map[string]bool{}
	for _, name := range []string{
		eventUsageBlocked, eventExchangeFailed, eventImportFailed, eventCollabDenied,
		eventProjectData, eventAICallFailed,
	} {
		if name == "" {
			t.Fatal("事象名が空（分類ごとに事象名を 1 つ持つはず）")
		}
		if names[name] {
			t.Errorf("事象名が重複している: %q（分類ごとに 1 つのはず）", name)
		}
		names[name] = true
	}
	if len(names) != 6 {
		t.Fatalf("事象名が %d 件（AI 通信系 + 本イシューの 5 分類 = 6 件のはず）", len(names))
	}
	_ = aiprovider.ErrClassTransient // 分類体系は増やしていないことの目印
}
