//go:build integration

package binding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/keymanager/keytest"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// newConfiguredAPI は初期設定まで済んだ API を返す。
func newConfiguredAPI(t *testing.T, stub *stubAdapter) (*API, string) {
	t.Helper()
	a, label := newTestAPI(t, stub)
	if _, err := a.RegisterKey("anthropic", label, dummyKey); err != nil {
		t.Fatal(err)
	}
	if err := a.CompleteSetup(SetupRequest{
		ProviderID: "anthropic", Label: label, Model: "claude-opus-5", Effort: "standard",
		AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤",
	}); err != nil {
		t.Fatal(err)
	}
	return a, label
}

// 設定内容の表示。キー本体は載せず、状態はマスク表示。
func TestSettingsView(t *testing.T) {
	a, label := newConfiguredAPI(t, &stubAdapter{})

	view, err := a.SettingsView()
	if err != nil {
		t.Fatalf("設定を取得できません: %v", err)
	}
	if view.AuthorID != "k.sato@example.co.jp" || view.DisplayName != "佐藤" {
		t.Errorf("作業者名が表示されない: %+v", view)
	}
	if len(view.Providers) != 1 {
		t.Fatalf("プロバイダ設定の件数が違う: %d", len(view.Providers))
	}
	p := view.Providers[0]
	if p.Label != label || p.ProviderLabel != "Anthropic（Claude）" || p.Model != "claude-opus-5" {
		t.Errorf("設定内容が違う: %+v", p)
	}
	if p.EffortLabel != "標準" || p.KeyState != "登録済み" || !p.IsDefault {
		t.Errorf("表示ラベルが違う: %+v", p)
	}
	if strings.Contains(p.KeyMasked, dummyKey) || p.KeyMasked == "" {
		t.Errorf("キーがマスクされていない: %+v", p)
	}
	if view.DataPolicyNotice == "" {
		t.Error("データ利用ポリシーの注意喚起が無い（設定画面にも初回設定と同じ注意喚起を出す）")
	}
	if !view.AIReady || view.AIBlockedReason != "" {
		t.Errorf("キー登録済みなのに AI 機能がブロックされている: %+v", view)
	}
}

// キーを削除すると「キー未設定」になり、AI 機能がブロックされ誘導が出る。
func TestDeleteKeyBlocksAI(t *testing.T) {
	a, label := newConfiguredAPI(t, &stubAdapter{})

	if err := a.DeleteKey("anthropic", label); err != nil {
		t.Fatalf("キー削除に失敗: %v", err)
	}
	view, err := a.SettingsView()
	if err != nil {
		t.Fatal(err)
	}
	if view.Providers[0].KeyState != "未設定" {
		t.Errorf("削除後の状態が違う: %+v", view.Providers[0])
	}
	if view.AIReady {
		t.Error("キー未設定なのに AI 機能が使える")
	}
	if !strings.Contains(view.AIBlockedReason, "キーを登録") {
		t.Errorf("再登録への誘導が無い: %q", view.AIBlockedReason)
	}

	ready, err := a.AIReady()
	if err != nil {
		t.Fatal(err)
	}
	if ready.AIReady || ready.AIBlockedReason == "" {
		t.Errorf("AIReady の判定が違う: %+v", ready)
	}
}

// モデル・エフォートの変更が保存され、以後の設定に反映される。
func TestSaveProviderConfig(t *testing.T) {
	a, label := newConfiguredAPI(t, &stubAdapter{})

	if err := a.SaveProviderConfig(UpdateProviderRequest{
		Label: label, ProviderID: "anthropic", Model: "claude-haiku-4-5-20251001",
		Effort: "high", MakeDefault: true,
	}); err != nil {
		t.Fatalf("保存に失敗: %v", err)
	}
	view, err := a.SettingsView()
	if err != nil {
		t.Fatal(err)
	}
	p := view.Providers[0]
	if p.Model != "claude-haiku-4-5-20251001" || p.Effort != string(aiprovider.EffortHigh) || p.EffortLabel != "高" {
		t.Errorf("変更が反映されていない: %+v", p)
	}

	// 値集合の検証
	cases := map[string]UpdateProviderRequest{
		"対応外のプロバイダ":  {Label: label, ProviderID: "bedrock", Model: "m", Effort: "high"},
		"モデルが空":      {Label: label, ProviderID: "anthropic", Model: "  ", Effort: "high"},
		"エフォートが値集合外": {Label: label, ProviderID: "anthropic", Model: "m", Effort: "medium"},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if err := a.SaveProviderConfig(req); err == nil {
				t.Error("不正な入力が受理された")
			}
		})
	}

	// キー未登録の設定は既定にできない（未設定なら AI 機能を止める判定と揃える）
	otherLabel := keytest.Marker + uuid.NewString()
	if err := a.SaveProviderConfig(UpdateProviderRequest{
		Label: otherLabel, ProviderID: "openai", Model: "gpt-5.6-luna", Effort: "low", MakeDefault: true,
	}); err == nil {
		t.Error("キー未登録の設定が既定になった")
	}
}

// 表示名は変更でき、利用者 ID は変わらない。
func TestSetDisplayName(t *testing.T) {
	a, _ := newConfiguredAPI(t, &stubAdapter{})

	if err := a.SetDisplayName(" 佐藤（営業） "); err != nil {
		t.Fatalf("変更に失敗: %v", err)
	}
	view, err := a.SettingsView()
	if err != nil {
		t.Fatal(err)
	}
	if view.DisplayName != "佐藤（営業）" {
		t.Errorf("表示名が変わっていない: %q", view.DisplayName)
	}
	if view.AuthorID != "k.sato@example.co.jp" {
		t.Errorf("利用者 ID が変わった: %q", view.AuthorID)
	}
	if err := a.SetDisplayName("  "); err == nil {
		t.Error("空の表示名が受理された")
	}
}

// 手動バックアップ → 復元（新しいフォルダへ）。
func TestBackupGenerationsAndRestore(t *testing.T) {
	a, _ := newConfiguredAPI(t, &stubAdapter{})
	createdProject, err := a.CreateProject(CreateProjectRequest{
		Path: t.TempDir(), TargetSystemName: "在庫管理システム"})
	if err != nil {
		t.Fatal(err)
	}
	projectDir := createdProject.Path

	// 自動退避を 1 世代作る（閉じる操作に相当）
	store, err := a.openProject(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CloseAndBackup(a.paths); err != nil {
		t.Fatal(err)
	}
	gens, err := a.BackupGenerations(projectDir)
	if err != nil {
		t.Fatalf("世代一覧に失敗: %v", err)
	}
	if len(gens) != 1 {
		t.Fatalf("世代数が違う: %d", len(gens))
	}
	if gens[0].CreatedAt == "" || gens[0].SizeBytes == 0 {
		t.Errorf("世代の情報が欠けている: %+v", gens[0])
	}

	// 世代からの復元（同じ project_id → 複製として保持）
	preview, err := a.PreviewBackup(gens[0].Path)
	if err != nil {
		t.Fatalf("確認に失敗: %v", err)
	}
	if preview.DuplicatePath != projectDir {
		t.Errorf("重複が検知されない: %+v", preview)
	}
	if preview.TargetSystemName != "在庫管理システム" {
		t.Errorf("確認内容が違う: %+v", preview)
	}

	copyDir := filepath.Join(t.TempDir(), "restored-copy")
	restored, err := a.RestoreBackup(gens[0].Path, copyDir, true)
	if err != nil {
		t.Fatalf("復元に失敗: %v", err)
	}
	if restored.ProjectID == preview.ProjectID {
		t.Error("複製として保持したのに project_id が同じ")
	}
	if !restored.Available {
		t.Errorf("復元したプロジェクトを読めない: %+v", restored)
	}

	list, err := a.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Errorf("復元後の一覧が違う: %d 件", len(list))
	}

	// 置き換えを選ぶと元のプロジェクトは一覧から外れる
	replaceDir := filepath.Join(t.TempDir(), "restored-replace")
	if _, err := a.RestoreBackup(gens[0].Path, replaceDir, false); err != nil {
		t.Fatalf("置き換え復元に失敗: %v", err)
	}
	list, err = a.Projects()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if p.Path == projectDir {
			t.Error("置き換えたのに元のプロジェクトが一覧に残っている")
		}
	}
}

// 復元先が空でない場合は復元しない（既存データの破壊防止）。
func TestRestoreRefusesNonEmptyDestination(t *testing.T) {
	a, _ := newConfiguredAPI(t, &stubAdapter{})
	projectDir := createTestProject(t, a, t.TempDir(), "在庫管理システム")
	store, err := a.openProject(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	backupPath, err := store.CloseAndBackup(a.paths)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.RestoreBackup(backupPath, projectDir, false); err == nil {
		t.Fatal("既存のプロジェクトフォルダへ復元できてしまった")
	}
	if _, err := a.RestoreBackup(backupPath, "", false); err == nil {
		t.Error("復元先未指定で復元できてしまった")
	}
}

// キー参照名は設定に残るが、キー本体はどこにも書かれない。
func TestSettingsFileHasNoKey(t *testing.T) {
	a, label := newConfiguredAPI(t, &stubAdapter{})
	raw, err := readFile(a.paths.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, dummyKey) {
		t.Error("アプリ設定にキー本体が書かれた")
	}
	ref := keymanager.Ref{ProviderID: "anthropic", Label: label}
	if !strings.Contains(raw, ref.String()) {
		t.Error("キー参照名が保存されていない")
	}
	_ = projectstore.CurrentSettingsVersion
}

// テーマは端末ごとのアプリ設定に保存し、次回読み込みで復元する。
func TestThemeIsPersistedPerDevice(t *testing.T) {
	a, _ := newConfiguredAPI(t, &stubAdapter{})

	theme, err := a.Theme()
	if err != nil {
		t.Fatalf("テーマを取得できません: %v", err)
	}
	if theme != projectstore.ThemeDark {
		t.Errorf("未設定の既定がダークでない: %q", theme)
	}

	if err := a.SetTheme(projectstore.ThemeLight); err != nil {
		t.Fatalf("保存に失敗: %v", err)
	}
	if theme, err = a.Theme(); err != nil || theme != projectstore.ThemeLight {
		t.Errorf("保存したテーマが復元されない: %q %v", theme, err)
	}
	view, err := a.SettingsView()
	if err != nil {
		t.Fatal(err)
	}
	if view.Theme != projectstore.ThemeLight {
		t.Errorf("設定画面へ反映されない: %q", view.Theme)
	}

	// 別インスタンス（アプリ再起動に相当）でも復元される
	restarted := &API{paths: a.paths, keys: a.keys}
	if theme, err = restarted.Theme(); err != nil || theme != projectstore.ThemeLight {
		t.Errorf("再起動後に復元されない: %q %v", theme, err)
	}

	// アプリ設定に保存される（プロジェクトデータには持たせない）
	raw, err := readFile(a.paths.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"theme": "light"`) {
		t.Errorf("アプリ設定に保存されていない:\n%s", raw)
	}

	if err := a.SetTheme("sepia"); err == nil {
		t.Error("値集合外のテーマが受理された")
	}
}

// 旧版の設定は読み込み後に自版へ更新して保存する。
func TestOlderSettingsVersionIsUpgradedOnSave(t *testing.T) {
	a, _ := newConfiguredAPI(t, &stubAdapter{})
	raw, err := readFile(a.paths.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	older := strings.Replace(raw, `"settings_version": "`+projectstore.CurrentSettingsVersion+`"`,
		`"settings_version": "1.0"`, 1)
	if older == raw {
		t.Fatalf("形式バージョンを差し替えられませんでした:\n%s", raw)
	}
	if err := writeFile(a.paths.SettingsFile(), older); err != nil {
		t.Fatal(err)
	}

	if err := a.SetTheme(projectstore.ThemeLight); err != nil {
		t.Fatalf("旧版の設定を保存できない: %v", err)
	}
	updated, err := readFile(a.paths.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated, `"settings_version": "`+projectstore.CurrentSettingsVersion+`"`) {
		t.Errorf("自版へ更新されていない:\n%s", updated)
	}
	if !strings.Contains(updated, `"theme": "light"`) {
		t.Errorf("変更が保存されていない:\n%s", updated)
	}
}

// AI 呼び出しタイムアウトの表示・保存・範囲外の拒否と、
// 保存値がアダプタ生成へ渡ること。
func TestAITimeouts(t *testing.T) {
	stub := &stubAdapter{models: []aiprovider.ModelInfo{
		{ID: "claude-opus-5", DisplayName: "Claude Opus 5", Tier: aiprovider.TierPrimary, DefaultForTier: true},
	}}
	a, label := newConfiguredAPI(t, stub)

	// 未設定のときは既定値（10 秒 / 300 秒）と許容範囲が返る。
	view, err := a.SettingsView()
	if err != nil {
		t.Fatalf("設定画面の取得に失敗: %v", err)
	}
	got := view.AITimeouts
	if got.ConnectSeconds != 10 || got.ResponseSeconds != 300 {
		t.Errorf("既定値が返らない: %+v", got)
	}
	if got.MinConnectSeconds != 5 || got.MaxConnectSeconds != 60 ||
		got.MinResponseSeconds != 60 || got.MaxResponseSeconds != 900 {
		t.Errorf("許容範囲が返らない: %+v", got)
	}

	// 範囲外は保存せず、許容範囲を添えて拒否する。
	for _, tc := range []struct{ connect, response int }{
		{4, 300}, {61, 300}, {10, 59}, {10, 901},
	} {
		if err := a.SetAITimeouts(tc.connect, tc.response); err == nil {
			t.Errorf("範囲外が受理された: connect=%d response=%d", tc.connect, tc.response)
		}
	}
	view, _ = a.SettingsView()
	if view.AITimeouts.ConnectSeconds != 10 {
		t.Errorf("拒否したのに保存されている: %+v", view.AITimeouts)
	}

	// 範囲内は保存され、再読込で復元される。
	if err := a.SetAITimeouts(30, 120); err != nil {
		t.Fatalf("保存に失敗: %v", err)
	}
	view, err = a.SettingsView()
	if err != nil {
		t.Fatalf("設定画面の取得に失敗: %v", err)
	}
	if view.AITimeouts.ConnectSeconds != 30 || view.AITimeouts.ResponseSeconds != 120 {
		t.Errorf("保存値が復元されない: %+v", view.AITimeouts)
	}

	// 保存値がアダプタ生成オプションへ渡る（接続確立のタイムアウト）。
	if _, err := a.VerifyKey("anthropic", label); err != nil {
		t.Fatalf("疎通確認に失敗: %v", err)
	}
	if stub.opts.Timeouts.Connect != 30*time.Second || stub.opts.Timeouts.Response != 120*time.Second {
		t.Errorf("設定値がアダプタへ渡っていない: %+v", stub.opts.Timeouts)
	}
}

// 紹介スライドの初回の自動表示は端末ごとに 1 度だけ。
// 記録は再起動後も残り、二重に保存しない（設定画面からの再表示ではこの API を呼ばない）。
func TestIntroShownIsPersistedPerDevice(t *testing.T) {
	a, _ := newConfiguredAPI(t, &stubAdapter{})

	shown, err := a.IntroShown()
	if err != nil {
		t.Fatalf("紹介スライドの表示状態を取得できません: %v", err)
	}
	if shown {
		t.Error("未設定の既定が「表示済み」になっている（初回に自動表示されない）")
	}

	if err := a.MarkIntroShown(); err != nil {
		t.Fatalf("記録に失敗: %v", err)
	}
	if shown, err = a.IntroShown(); err != nil || !shown {
		t.Errorf("記録が反映されない: %v %v", shown, err)
	}

	// 再度呼んでも壊れない（多重の終了操作を受けても状態は「表示済み」のまま）
	if err := a.MarkIntroShown(); err != nil {
		t.Fatalf("2 回目の記録で失敗: %v", err)
	}

	// 別インスタンス（アプリ再起動に相当）でも復元される
	restarted := &API{paths: a.paths, keys: a.keys}
	if shown, err = restarted.IntroShown(); err != nil || !shown {
		t.Errorf("再起動後に復元されない: %v %v", shown, err)
	}

	// 記録はアプリ設定（端末ごと）にのみ置く
	data, err := os.ReadFile(a.paths.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"intro_shown": true`) {
		t.Errorf("settings.json へ書かれていない:\n%s", data)
	}
}
