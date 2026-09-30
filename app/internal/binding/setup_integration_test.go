//go:build integration

// 結合テスト（バインディング層 × 実ファイル・実 OS セキュアストレージ）。
// AIプロバイダのアダプタのみ差し替える（外部 API を呼ばない）。実キーは使わない。

package binding

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/keymanager/keytest"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

const dummyKey = "dummy-key-value-not-real-4321"

// stubAdapter は疎通確認・モデル一覧の応答を固定するアダプタ。
type stubAdapter struct {
	opts aiprovider.AdapterOptions

	id         aiprovider.ProviderID
	verifyErr  error
	models     []aiprovider.ModelInfo
	modelsErr  error
	keys       aiprovider.KeyProvider
	ref        aiprovider.KeyRef
	verifyCall int
}

func (s *stubAdapter) ID() aiprovider.ProviderID { return s.id }

func (s *stubAdapter) ListModels(ctx context.Context) ([]aiprovider.ModelInfo, error) {
	if s.modelsErr != nil {
		return nil, s.modelsErr
	}
	return s.models, nil
}

// StreamMessage はストリーミング対話。本テスト群では呼ばない。
func (s *stubAdapter) StreamMessage(context.Context, aiprovider.ChatRequest) (<-chan aiprovider.StreamEvent, error) {
	return nil, errors.New("このテストではストリーミングを使わない")
}

func (s *stubAdapter) VerifyKey(ctx context.Context) error {
	s.verifyCall++
	// キーが読み出せることも確認する（キーは呼び出しのたびに取得する）
	if _, err := s.keys.SecretKey(ctx, s.ref); err != nil {
		return err
	}
	return s.verifyErr
}

// newTestAPI は保存先を一時フォルダにし、アダプタを差し替えた API を返す。
// キーは実際の OS セキュアストレージを使う（利用者の実エントリと衝突しない参照名）。
func newTestAPI(t *testing.T, stub *stubAdapter) (*API, string) {
	t.Helper()
	paths := projectstore.AppPaths{Base: filepath.Join(t.TempDir(), projectstore.AppID)}
	label := keytest.Marker + uuid.NewString()
	a := &API{
		paths: paths,
		keys:  keymanager.New(),
		newAdapter: func(id aiprovider.ProviderID, keys aiprovider.KeyProvider, ref aiprovider.KeyRef, opts aiprovider.AdapterOptions) (aiprovider.Adapter, error) {
			stub.id = id
			stub.keys = keys
			stub.ref = ref
			stub.opts = opts
			return stub, nil
		},
	}
	// テストが中断されてもキーチェーンに残骸を残さない（次回の実行開始時にも掃除される）。
	keytest.TrackKey(t, keymanager.Ref{ProviderID: "anthropic", Label: label})
	return a, label
}

// 初期設定の一連（未完了 → キー登録＋疎通確認 → モデル選択 → 確定）。
func TestInitialSetupFlow(t *testing.T) {
	stub := &stubAdapter{models: []aiprovider.ModelInfo{
		{ID: "claude-opus-5", DisplayName: "Claude Opus 5", ContextWindow: 1000000, MaxOutput: 128000,
			Tier: aiprovider.TierPrimary, DefaultForTier: true},
	}}
	a, label := newTestAPI(t, stub)

	state, err := a.SetupState()
	if err != nil {
		t.Fatalf("初期状態を取得できません: %v", err)
	}
	if state.Complete {
		t.Error("未設定なのに初期設定完了と判定された")
	}
	// プロバイダの選択肢は「その OS で選べるもの」（macOS は 3 社 + Codex App Server）。
	if len(state.Providers) != len(availableProviderOptions(runtime.GOOS)) || len(state.Efforts) != 3 {
		t.Errorf("選択肢が揃っていない: %+v", state)
	}
	if state.DataPolicyNotice == "" {
		t.Error("データ利用ポリシーの注意喚起が無い")
	}

	// キー登録 + 疎通確認
	result, err := a.RegisterKey("anthropic", label, dummyKey)
	if err != nil {
		t.Fatalf("キー登録に失敗: %v", err)
	}
	if !result.OK {
		t.Fatalf("疎通確認に失敗: %+v", result)
	}
	if stub.verifyCall != 1 {
		t.Errorf("疎通確認が呼ばれた回数が違う: %d", stub.verifyCall)
	}
	ref := keymanager.Ref{ProviderID: "anthropic", Label: label}
	if ok, err := a.keys.Exists(ref); err != nil || !ok {
		t.Errorf("キーが OS セキュアストレージに登録されていない: %v %v", ok, err)
	}

	// モデル一覧
	models, err := a.Models("anthropic", label, "")
	if err != nil {
		t.Fatalf("モデル一覧に失敗: %v", err)
	}
	if models.FromKnownList {
		t.Error("取得できたのに既知一覧へ縮退した")
	}
	if len(models.Models) != 1 || !models.Models[0].Recommended {
		t.Errorf("モデル一覧が違う: %+v", models.Models)
	}

	// 確定（エフォート未指定なら標準）
	if err := a.CompleteSetup(SetupRequest{
		ProviderID: "anthropic", Label: label, Model: "claude-opus-5",
		AuthorID: " K.Sato@Example.co.jp ", DisplayName: "佐藤",
	}); err != nil {
		t.Fatalf("確定に失敗: %v", err)
	}

	state, err = a.SetupState()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Complete {
		t.Error("確定後に初期設定完了と判定されない")
	}
	if state.AuthorID != "k.sato@example.co.jp" {
		t.Errorf("利用者 ID が正規化されていない: %q", state.AuthorID)
	}

	settings, err := projectstore.LoadSettings(a.paths)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := settings.DefaultProviderSetting()
	if !ok {
		t.Fatalf("既定プロバイダが保存されていない: %+v", settings)
	}
	if p.Effort != string(aiprovider.EffortStandard) {
		t.Errorf("エフォート未指定時の既定が標準でない: %q", p.Effort)
	}
	if p.KeyRef != ref.String() {
		t.Errorf("キー参照名が保存されていない: %q", p.KeyRef)
	}
	// アプリ設定にキー本体を書き込まない（キーは OS セキュアストレージにだけ置く）
	raw, err := readFile(a.paths.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, dummyKey) {
		t.Error("アプリ設定にキー本体が書き込まれた")
	}
}

// 疎通確認に失敗したキーは保存せず、変更前の登録を壊さない。
func TestRegisterKeyDoesNotStoreUnverifiedKey(t *testing.T) {
	stub := &stubAdapter{verifyErr: &aiprovider.ProviderError{
		Class: aiprovider.ErrClassConfig, Provider: aiprovider.ProviderAnthropic, HTTPStatus: 401,
		Code: "authentication_error", Message: `{"error":"invalid x-api-key"}`,
	}}
	a, label := newTestAPI(t, stub)

	result, err := a.RegisterKey("anthropic", label, dummyKey)
	if err != nil {
		t.Fatalf("登録処理でエラー: %v", err)
	}
	if result.OK {
		t.Fatal("疎通確認に失敗したのに成功として返った")
	}
	if result.Reason != "認証エラー" {
		t.Errorf("失敗理由が違う: %+v", result)
	}
	if strings.Contains(result.Detail, dummyKey) || strings.Contains(result.Detail, "401") {
		t.Errorf("利用者向け文言にキー・コード値が含まれる: %q", result.Detail)
	}
	ref := keymanager.Ref{ProviderID: "anthropic", Label: label}
	if ok, err := a.keys.Exists(ref); err != nil || ok {
		t.Errorf("確認に失敗したキーが保存された: %v %v", ok, err)
	}

	// キー未登録のままでは確定できない（キー未設定で AI 機能を止めるのと同じ扱い）
	if err := a.CompleteSetup(SetupRequest{ProviderID: "anthropic", Label: label, Model: "m",
		AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤"}); err == nil {
		t.Error("キー未登録で初期設定を確定できてしまった")
	}
}

// 変更時に疎通確認が失敗したら、変更前のキーがそのまま残ること。
func TestRegisterKeyKeepsPreviousKeyOnFailure(t *testing.T) {
	stub := &stubAdapter{}
	a, label := newTestAPI(t, stub)
	ref := keymanager.Ref{ProviderID: "anthropic", Label: label}

	// 変更前のキーを正常に登録する
	if result, err := a.RegisterKey("anthropic", label, dummyKey); err != nil || !result.OK {
		t.Fatalf("事前の登録に失敗: %+v %v", result, err)
	}

	// 変更が失敗する状況をつくる
	stub.verifyErr = &aiprovider.ProviderError{Class: aiprovider.ErrClassConfig, HTTPStatus: 401}
	result, err := a.RegisterKey("anthropic", label, "another-dummy-key-0000")
	if err != nil {
		t.Fatalf("エラー: %v", err)
	}
	if result.OK {
		t.Fatal("失敗するはずの疎通確認が成功した")
	}

	// 変更前のキーが残っていること
	stub.verifyErr = nil
	got, err := a.keys.SecretKey(context.Background(), ref)
	if err != nil {
		t.Fatalf("変更前のキーが失われた: %v", err)
	}
	if got != dummyKey {
		t.Errorf("変更前のキーが上書きされた（マスク表示）: %q", keymanager.MaskKey(got))
	}
}

// モデル一覧を取得できないときは既知一覧へ縮退し、その旨を示す（黙って縮退しない）。
func TestModelsFallsBackToKnownList(t *testing.T) {
	stub := &stubAdapter{modelsErr: &aiprovider.ProviderError{
		Class: aiprovider.ErrClassTransient, Provider: aiprovider.ProviderAnthropic, Message: "network"}}
	a, label := newTestAPI(t, stub)
	if err := a.keys.Register(keymanager.Ref{ProviderID: "anthropic", Label: label}, dummyKey); err != nil {
		t.Fatal(err)
	}

	models, err := a.Models("anthropic", label, "")
	if err != nil {
		t.Fatalf("縮退できていない: %v", err)
	}
	if !models.FromKnownList {
		t.Error("既知一覧への縮退が示されない")
	}
	if models.Notice == "" {
		t.Error("縮退した旨の説明が無い")
	}
	if len(models.Models) == 0 {
		t.Error("既知一覧が空")
	}
}

// 対応外のプロバイダ・モデル未選択は確定できない。
func TestCompleteSetupValidates(t *testing.T) {
	stub := &stubAdapter{}
	a, label := newTestAPI(t, stub)
	if err := a.keys.Register(keymanager.Ref{ProviderID: "anthropic", Label: label}, dummyKey); err != nil {
		t.Fatal(err)
	}
	cases := map[string]SetupRequest{
		"対応外のプロバイダ":  {ProviderID: "bedrock", Label: label, Model: "m", AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤"},
		"モデル未選択":     {ProviderID: "anthropic", Label: label, AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤"},
		"利用者 ID が不正": {ProviderID: "anthropic", Label: label, Model: "m", AuthorID: "ksato", DisplayName: "佐藤"},
		"表示名が空":      {ProviderID: "anthropic", Label: label, Model: "m", AuthorID: "k.sato@example.co.jp"},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if err := a.CompleteSetup(req); err == nil {
				t.Error("不正な入力で確定できてしまった")
			}
		})
	}
}

// キー未設定のまま疎通確認するとキー未設定として返る。
func TestVerifyKeyWithoutRegistration(t *testing.T) {
	stub := &stubAdapter{}
	a, label := newTestAPI(t, stub)
	result, err := a.VerifyKey("anthropic", label)
	if err != nil {
		t.Fatalf("エラー: %v", err)
	}
	if result.OK {
		t.Fatal("未登録なのに成功した")
	}
	if result.Reason != "キー未設定" {
		t.Errorf("区分が違う: %+v", result)
	}
	var notSet error = keymanager.ErrKeyNotSet
	if !errors.Is(notSet, keymanager.ErrKeyNotSet) {
		t.Fatal("前提が壊れている")
	}
}
