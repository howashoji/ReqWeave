package aiprovider

import (
	"errors"
	"testing"
)

// 登録した後片づけのフックが、本システムの起動・終了・設定変更で呼ばれること。
func TestLifecycleHooksRunOnEachEvent(t *testing.T) {
	var calls []string
	RegisterLifecycle("test-lifecycle", LifecycleHooks{
		OnAppStart:        func() error { calls = append(calls, "start"); return nil },
		OnAppStop:         func() error { calls = append(calls, "stop"); return nil },
		OnSettingsChanged: func() error { calls = append(calls, "settings"); return nil },
	})

	AppStarted(nil)
	SettingsChanged(nil)
	AppStopping(nil)

	want := []string{"start", "settings", "stop"}
	if len(calls) != len(want) {
		t.Fatalf("呼ばれた回数 = %d, want %d（%v）", len(calls), len(want), calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("呼び出し[%d] = %q, want %q", i, calls[i], want[i])
		}
	}
}

// 1 つのアダプタの後片づけが失敗しても、他のアダプタの後片づけを飛ばさず、失敗を記録すること。
func TestLifecycleHookFailureIsRecordedAndOthersStillRun(t *testing.T) {
	ran := false
	RegisterLifecycle("test-lifecycle-a", LifecycleHooks{
		OnAppStop: func() error { return errors.New("一時領域を削除できません") },
	})
	RegisterLifecycle("test-lifecycle-b", LifecycleHooks{
		OnAppStop: func() error { ran = true; return nil },
	})

	var recorded []map[string]string
	rec := EventRecorder(func(level EventLevel, event, message string, fields map[string]string) {
		if level != EventLevelWarn {
			t.Errorf("失敗の記録の重大度 = %v, want warn", level)
		}
		if event == "" || message == "" {
			t.Errorf("記録に event / message が無い: %q %q", event, message)
		}
		recorded = append(recorded, fields)
	})
	AppStopping(rec)

	if !ran {
		t.Error("先に失敗したアダプタがあると、後続のアダプタの後片づけが実行されない")
	}
	if len(recorded) != 1 {
		t.Fatalf("記録された失敗 = %d 件, want 1（%v）", len(recorded), recorded)
	}
	if recorded[0]["provider"] != "test-lifecycle-a" {
		t.Errorf("記録の provider = %q, want test-lifecycle-a", recorded[0]["provider"])
	}
	if recorded[0]["error"] == "" {
		t.Error("記録に失敗の内容が無い")
	}
}

// 同じプロバイダの二重登録は起動時に落ちる（レジストリ本体 = Register と同じ扱い）。
func TestRegisterLifecycleRejectsDuplicate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("二重登録が受理された")
		}
	}()
	RegisterLifecycle("test-lifecycle-dup", LifecycleHooks{})
	RegisterLifecycle("test-lifecycle-dup", LifecycleHooks{})
}

// 記録先が未設定（nil）でも後片づけは実行され、パニックしないこと。
func TestLifecycleWithoutRecorder(t *testing.T) {
	ran := false
	RegisterLifecycle("test-lifecycle-norec", LifecycleHooks{
		OnAppStart: func() error { ran = true; return errors.New("記録先が無い状態での失敗") },
	})
	AppStarted(nil)
	if !ran {
		t.Error("記録先が未設定だと後片づけが実行されない")
	}
}
