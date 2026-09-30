//go:build integration

package binding

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// errStubCleanup は後片づけの失敗の再現（内容は動作ログの確認にのみ使う）。
var errStubCleanup = errors.New("一時領域を削除できません")

// 子プロセス型アダプタの後片づけが、本システムの起動・終了・設定変更で呼ばれること。
//
// 呼ぶ側（バインディング層）はプロバイダを列挙しない。登録したアダプタのフックが呼ばれることを、
// 試験用のプロバイダ ID で確かめる（Codex アダプタの実装に依存しない）。
func TestAppLifecycleNotifiesSubprocessAdapters(t *testing.T) {
	var started, stopped, changed int
	aiprovider.RegisterLifecycle("test-binding-lifecycle", aiprovider.LifecycleHooks{
		OnAppStart:        func() error { started++; return nil },
		OnAppStop:         func() error { stopped++; return nil },
		OnSettingsChanged: func() error { changed++; return nil },
	})

	a, _ := newTestAPI(t, &stubAdapter{})
	attachAppLog(t, a)

	a.Startup(context.Background())
	if started != 1 {
		t.Errorf("起動時の後片づけの呼び出し = %d 回, want 1", started)
	}

	a.aiSettingsChanged()
	if changed != 1 {
		t.Errorf("設定変更の通知 = %d 回, want 1", changed)
	}

	a.Shutdown(context.Background())
	if stopped != 1 {
		t.Errorf("終了時の停止の呼び出し = %d 回, want 1", stopped)
	}
}

// 後片づけの失敗は動作ログへ警告として残る（黙って落とさない）。
func TestSubprocessAdapterCleanupFailureIsLogged(t *testing.T) {
	aiprovider.RegisterLifecycle("test-binding-lifecycle-failing", aiprovider.LifecycleHooks{
		OnAppStart: func() error { return errStubCleanup },
	})

	a, _ := newTestAPI(t, &stubAdapter{})
	dir := attachAppLog(t, a)

	a.Startup(context.Background())

	body := appLogBody(t, dir)
	if !strings.Contains(body, "ai.provider_start_cleanup_failed") {
		t.Errorf("後片づけの失敗が動作ログに無い:\n%s", body)
	}
	if !strings.Contains(body, "test-binding-lifecycle-failing") {
		t.Errorf("失敗したプロバイダが動作ログに無い:\n%s", body)
	}
}
