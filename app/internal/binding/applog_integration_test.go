//go:build integration

// 結合テスト（動作ログ）。
//
// 画面が呼ぶバインディングを通して、
//   - 動作ログがアプリ設定領域配下の logs/ に書かれること
//   - 出力層のマスキングを通り、ダミーのシークレットキー・同期先の認証情報が
//     平文でも Base64 でも残らないこと（ログ全体を両方の形で検索する）
//   - 推論努力の縮退と SendRecorder の onError が記録されること
//   - 上限（実定数 10MB）でローテーションし、保持数を超えるファイルを作らないこと
//
// を確認する。

package binding

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/applog"
	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/masking"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// attachAppLog は API へ実物の動作ログを取り付ける（テスト用の一時領域）。
func attachAppLog(t *testing.T, a *API) string {
	t.Helper()
	logger, err := applog.New(a.paths)
	if err != nil {
		t.Fatalf("動作ログを開けない: %v", err)
	}
	a.log = logger
	t.Cleanup(func() { _ = logger.Close() })
	// 保存先はログ出力層に聞く（他の層は LogsDir を参照しない = depcheck の logs-writer）。
	return logger.Dir()
}

// appLogBody は動作ログの現行ファイルの内容を返す。
func appLogBody(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatalf("動作ログを読めない: %v", err)
	}
	return string(data)
}

// appLogEvents は記録された event の一覧を返す（JSON Lines として解釈できることも兼ねて確認する）。
func appLogEvents(t *testing.T, dir string) []string {
	t.Helper()
	var events []string
	for i, line := range strings.Split(appLogBody(t, dir), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("%d 行目が JSON Lines として読めない: %v（%q）", i+1, err, line)
		}
		events = append(events, rec.Event)
	}
	return events
}

func hasEvent(events []string, want string) bool {
	for _, e := range events {
		if e == want {
			return true
		}
	}
	return false
}

// usageFailingStub は実績イベントの直前に監査ログのファイルを書き込み不可にし、
// SendRecorder の onError（監査記録の追記失敗）を実際に起こす。
type usageFailingStub struct {
	streamingStub
	// aiLogDir は audit/ai-log の絶対パス。実績イベントの直前に 0500 へ落とす。
	aiLogDir string
}

func (s *usageFailingStub) StreamMessage(ctx context.Context, req aiprovider.ChatRequest) (<-chan aiprovider.StreamEvent, error) {
	s.calls++
	s.requests = append(s.requests, req)
	ch := make(chan aiprovider.StreamEvent, 4)
	go func() {
		defer close(ch)
		ch <- aiprovider.StreamEvent{Kind: aiprovider.EventTextDelta, Text: dialogueQuestion}
		// 送信行（RecordSend）はここまでで書けている。その追記先を読み取り専用にして、
		// 実績行（RecordUsage）の追記だけを失敗させる（実績行の追記に失敗したときの onError）。
		denyWrite(s.aiLogDir)
		ch <- aiprovider.StreamEvent{Kind: aiprovider.EventUsage,
			Usage: &aiprovider.TokenUsage{InputTokens: 10, OutputTokens: 20}}
		ch <- aiprovider.StreamEvent{Kind: aiprovider.EventDone}
	}()
	return ch, nil
}

// denyWrite は dir 直下の既存ファイルを読み取り専用にする（追記の失敗を起こすため）。
func denyWrite(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		_ = os.Chmod(filepath.Join(dir, e.Name()), 0o400)
	}
}

// allowWrite は denyWrite で落とした権限を戻す（後片づけ）。
func allowWrite(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		_ = os.Chmod(filepath.Join(dir, e.Name()), 0o600)
	}
}

// 主要動作イベント（起動・終了）が logs/ に残る。
func TestOperationLogRecordsStartupAndShutdown(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, _ := newDialogueAPI(t, stub)
	dir := attachAppLog(t, a)

	ctx := context.Background()
	a.Startup(ctx)
	a.Shutdown(ctx)

	events := appLogEvents(t, dir)
	if !hasEvent(events, "app.start") {
		t.Errorf("起動が記録されていない: %v", events)
	}
	if !hasEvent(events, "app.stop") {
		t.Errorf("終了が記録されていない: %v", events)
	}
}

// 推論努力の縮退と監査記録の失敗が logs/ に残る。
func TestOperationLogRecordsDegradeAndRecordFailure(t *testing.T) {
	stub := &usageFailingStub{}
	a, root := newDialogueAPI(t, &stub.streamingStub)
	// newDialogueAPI は streamingStub を返すアダプタを組むため、実績を出す派生スタブへ差し替える。
	a.newAdapter = func(id aiprovider.ProviderID, keys aiprovider.KeyProvider, ref aiprovider.KeyRef,
		opts aiprovider.AdapterOptions) (aiprovider.Adapter, error) {
		return stub, nil
	}
	dir := attachAppLog(t, a)
	// 推論努力の縮退を実際に起こすため、推論努力を受け付けないモデルへ切り替える
	//（models.json の claude-haiku-4-5-20251001 は supports_reasoning: false）。
	selectModel(t, a, "claude-haiku-4-5-20251001")

	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("対話用に開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })

	stub.aiLogDir = filepath.Join(root, auditlog.DirAILog)
	if err := os.MkdirAll(stub.aiLogDir, 0o700); err != nil {
		t.Fatalf("監査ログの保存先を作れない: %v", err)
	}
	t.Cleanup(func() { allowWrite(stub.aiLogDir) })

	sess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatalf("セッションを開始できない: %v", err)
	}
	if ok, err := a.AskNextQuestion(sess.ID); err != nil || !ok {
		t.Fatalf("質問を要求できない: %v (ok=%v)", err, ok)
	}
	waitForState(t, a, sess.ID, dialogue.StateAwaitingAnswer)

	events := appLogEvents(t, dir)
	// 初期設定のモデルは推論努力を受け付けない（SupportsReasoning 未設定）ため縮退する。
	if !hasEvent(events, "ai.effort_degraded") {
		t.Errorf("推論努力の縮退が記録されていない: %v", events)
	}
	// 実績行の追記に失敗したことが残る（送信自体は成功しているため対話は止まらない）。
	if !hasEvent(events, "audit.record_failed") {
		t.Errorf("監査記録の失敗が記録されていない: %v", events)
	}
}

// selectModel は設定中のモデルを差し替える（設定画面のモデル選択と同じ保存先）。
func selectModel(t *testing.T, a *API, modelID string) {
	t.Helper()
	settings, err := a.settings()
	if err != nil {
		t.Fatalf("アプリ設定を読めない: %v", err)
	}
	for i := range settings.Providers {
		settings.Providers[i].Model = modelID
	}
	if err := projectstore.SaveSettings(a.paths, settings); err != nil {
		t.Fatalf("アプリ設定を保存できない: %v", err)
	}
}

// 動作ログへ渡した秘密情報が平文でも Base64 でも残らない。
func TestOperationLogMasksSecrets(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, _ := newDialogueAPI(t, stub)
	dir := attachAppLog(t, a)

	const dummyKey = "sk-ant-api03-DUMMYKEYFORLEAKTEST0123456789"
	const dummySync = "https://ghp_DUMMYSYNCTOKEN0123456789@example.invalid/team/proj.git"
	const dummyPass = "password=DUMMYSYNCPASSWORD0123"

	a.log.Error("test.leak", "認証に失敗しました: "+dummyKey)
	a.log.Error("test.leak", "同期に失敗しました", applog.F("remote", dummySync))
	a.log.Error("test.leak", "git の詳細", applog.F("detail", dummyPass))

	body := appLogBody(t, dir)
	for _, secret := range []string{
		dummyKey,
		"ghp_DUMMYSYNCTOKEN0123456789",
		"DUMMYSYNCPASSWORD0123",
	} {
		if strings.Contains(body, secret) {
			t.Errorf("秘密情報が平文で残った: %q", secret)
		}
		if enc := base64.StdEncoding.EncodeToString([]byte(secret)); strings.Contains(body, enc) {
			t.Errorf("秘密情報が Base64 表現で残った: %q", secret)
		}
	}
	if !strings.Contains(body, masking.Masked) {
		t.Error("マスキング後の表記が無い（出力層のフィルタを通っていない）")
	}
	// マスキング後も JSON Lines として読める（appLogEvents が壊れた行で fail する）。
	appLogEvents(t, dir)
}

// 同期の失敗が動作ログへ残り、同期先の所在・認証情報は載らない。
func TestOperationLogRecordsSyncFailure(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, root := newDialogueAPI(t, stub)
	dir := attachAppLog(t, a)

	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("対話用に開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	if client, err := a.syncClient(a.session); err != nil {
		t.Fatal(err)
	} else if av := client.Availability(); !av.Available {
		// 前提が揃わないときは skip せず fail させる（検証しないまま緑にしない）
		t.Fatalf("この環境に git が無いため検証できない: %s", av.Reason)
	}

	// 実在しない共有フォルダ上の同期先（到達できない = 失敗種別の unreachable / not_found）。
	missing := filepath.Join(t.TempDir(), "no-such-share", "proj.git")
	view, err := a.CheckSyncConnection(projectstore.SyncKindFolder, missing)
	if err != nil {
		t.Fatalf("接続確認を実行できない: %v", err)
	}
	if view.OK || view.Failure == nil {
		t.Fatalf("到達できない同期先が成功と判定された: %+v", view)
	}

	events := appLogEvents(t, dir)
	if !hasEvent(events, "sync.failed") {
		t.Fatalf("同期の失敗が記録されていない: %v", events)
	}
	body := appLogBody(t, dir)
	// 同期先の所在そのものは動作ログへ載せない（所在に認証情報が入りうるため）。
	if strings.Contains(body, missing) {
		t.Errorf("同期先の所在が動作ログに残った: %q", missing)
	}
	// git の詳細（Detail）は記録しない。共有フォルダの絶対パス等の**端末固有情報**が
	// 混じりうるため。画面の折りたたみ表示では出す。
	if strings.Contains(body, `"detail"`) {
		t.Errorf("失敗の詳細が動作ログに残った（端末固有情報が混じりうる）: %s", body)
	}
	// 失敗種別と操作は残る（調査の手がかり）。
	if !strings.Contains(body, `"kind"`) {
		t.Errorf("失敗種別が記録されていない: %s", body)
	}
}

// 実定数（10MB・5 ファイル）でローテーションし、6 ファイル目を作らない。
func TestOperationLogRotatesAtRealThreshold(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, _ := newDialogueAPI(t, stub)
	dir := attachAppLog(t, a)

	// 既に世代が出そろっている状態（app.1.log 〜 app.4.log）を作る。
	for i := 1; i <= applog.MaxFiles-1; i++ {
		p := filepath.Join(dir, "app."+string(rune('0'+i))+".log")
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("世代ファイルを作れない: %v", err)
		}
	}

	// 上限（10MB）を実際に超えさせる。1 行あたり約 1MB で 11 行。
	big := strings.Repeat("x", 1<<20)
	for i := 0; i < 11; i++ {
		a.log.Info("bulk", "", applog.F("filler", big))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("保存先を読めない: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(entries) != applog.MaxFiles {
		t.Fatalf("ファイル数が %d（期待 %d）: %v", len(entries), applog.MaxFiles, names)
	}
	for _, name := range names {
		if name == "app.5.log" {
			t.Errorf("保持数を超えるファイルが作られた: %v", names)
		}
	}
	// ローテーションが実際に起きている（現行ファイルが上限より小さい）。
	info, err := os.Stat(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatalf("現行ファイルが無い: %v", err)
	}
	if info.Size() > applog.MaxFileBytes {
		t.Errorf("現行ファイルが上限を超えている: %d", info.Size())
	}
	if info.Size() >= 11<<20 {
		t.Errorf("ローテーションしていない（全量が 1 ファイルに残った）: %d", info.Size())
	}
}
