package binding

// Wails 内部のログの橋渡しの単体テスト。
//
// 確かめること:
//   - バインディング呼び出し中の異常（Wails が recover して自分のロガーへ出すもの）が動作ログに残る
//   - 記録が出力層のマスキングを通る（秘密情報を動作ログに残さない）
//   - 呼び出しメッセージ（引数＝発話本文などの業務データ）を動作ログへ書かない
//   - 既定のロガーへの委譲を止めない（`wails dev` の端末出力を消さない）

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/applog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// baseRecorder は委譲先の Wails ロガーの代役（呼ばれた水準と本文を控える）。
type baseRecorder struct{ lines []string }

func (b *baseRecorder) Print(m string)   { b.lines = append(b.lines, "PRINT "+m) }
func (b *baseRecorder) Trace(m string)   { b.lines = append(b.lines, "TRACE "+m) }
func (b *baseRecorder) Debug(m string)   { b.lines = append(b.lines, "DEBUG "+m) }
func (b *baseRecorder) Info(m string)    { b.lines = append(b.lines, "INFO "+m) }
func (b *baseRecorder) Warning(m string) { b.lines = append(b.lines, "WARN "+m) }
func (b *baseRecorder) Error(m string)   { b.lines = append(b.lines, "ERROR "+m) }
func (b *baseRecorder) Fatal(m string)   { b.lines = append(b.lines, "FATAL "+m) }

// newBridge は一時領域の動作ログへ書く橋渡しロガーを組み立てる。
func newBridge(t *testing.T) (*wailsLogger, *baseRecorder, string) {
	t.Helper()
	paths := projectstore.AppPaths{Base: t.TempDir()}
	l, err := applog.New(paths)
	if err != nil {
		t.Fatalf("動作ログを開けない: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	base := &baseRecorder{}
	return newWailsLogger(base, l), base, l.Dir()
}

// appLogFile は動作ログの現行ファイルの内容を返す（未記録なら空）。
func appLogFile(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatalf("動作ログを読めない: %v", err)
	}
	return string(data)
}

func TestWailsLoggerRecordsWarningAndError(t *testing.T) {
	bridge, base, dir := newBridge(t)

	bridge.Print("印字")
	bridge.Trace("追跡")
	bridge.Debug("詳細")
	bridge.Info("情報")
	bridge.Warning("更新の確認に失敗しました")
	bridge.Error("プロジェクトを開けません")

	body := appLogFile(t, dir)
	if !strings.Contains(body, eventWailsWarn) || !strings.Contains(body, "更新の確認に失敗しました") {
		t.Errorf("警告が記録されていない: %q", body)
	}
	if !strings.Contains(body, eventWailsError) || !strings.Contains(body, "プロジェクトを開けません") {
		t.Errorf("エラーが記録されていない: %q", body)
	}
	for _, m := range []string{"印字", "追跡", "詳細", "情報"} {
		if strings.Contains(body, m) {
			t.Errorf("警告未満の水準 %q を記録した（呼び出し結果の業務データが載りうる）: %q", m, body)
		}
	}
	// 委譲は水準にかかわらず止めない（`wails dev` の端末出力を消さない）。
	if len(base.lines) != 6 {
		t.Errorf("委譲が %d 件（期待 6）: %v", len(base.lines), base.lines)
	}
}

func TestWailsLoggerDropsCallPayload(t *testing.T) {
	bridge, _, dir := newBridge(t)

	// Wails の Dispatcher がパニック時に出す形（呼び出しメッセージつき）。
	bridge.Error(`process message error: C{"name":"main.API.SendUtterance",` +
		`"args":["受注管理システムの在庫引当のルールを教えてください"],"callbackID":"x"} -> 想定外の状態です`)
	// 文言が変わっても構造で落ちること。
	bridge.Error(`unknown message: {"callbackID":"y","args":["社外秘の要件本文"]}`)

	body := appLogFile(t, dir)
	for _, secret := range []string{"受注管理システムの在庫引当", "社外秘の要件本文", "callbackID"} {
		if strings.Contains(body, secret) {
			t.Errorf("呼び出しメッセージ %q を動作ログへ書いた（業務データは動作ログへ載せない）: %q", secret, body)
		}
	}
	if strings.TrimSpace(body) != "" {
		t.Errorf("落とすべき行を記録した: %q", body)
	}
}

func TestWailsLoggerMasksSecrets(t *testing.T) {
	bridge, _, dir := newBridge(t)

	bridge.Error("キーが不正です sk-ant-api03-WAILSBRIDGEKEY0123456789")

	body := appLogFile(t, dir)
	if strings.Contains(body, "sk-ant-api03-WAILSBRIDGEKEY0123456789") {
		t.Errorf("マスキングを通っていない: %q", body)
	}
	if !strings.Contains(body, "***MASKED***") {
		t.Errorf("伏せ字が入っていない: %q", body)
	}
}

// 動作ログを開けない端末（log が nil）でも落ちないこと。
func TestWailsLoggerNilAppLog(t *testing.T) {
	base := &baseRecorder{}
	bridge := newWailsLogger(base, nil)

	bridge.Warning("警告")
	bridge.Error("エラー")

	if len(base.lines) != 2 {
		t.Errorf("委譲が %d 件（期待 2）: %v", len(base.lines), base.lines)
	}
}

func TestCarriesCallPayload(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    bool
	}{
		{"Dispatcher のパニック行", `process message error: C{"name":"x"} -> boom`, true},
		{"呼び出しメッセージの構造", `something {"callbackID":"1"}`, true},
		{"通常のエラー", "プロジェクトを開けません", false},
		{"引数という語を含む通常の文", "引数が不正です", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := carriesCallPayload(tt.message); got != tt.want {
				t.Errorf("carriesCallPayload(%q) = %v（期待 %v）", tt.message, got, tt.want)
			}
		})
	}
}
