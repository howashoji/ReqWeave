package binding

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 対応プロバイダは 3 社と Codex App Server。
// いずれもデータ利用ポリシーの参照先を持つ。
func TestProviderOptions(t *testing.T) {
	if len(providerOptions) != 4 {
		t.Fatalf("対応プロバイダ数が違う: %d", len(providerOptions))
	}
	want := map[string]bool{"anthropic": true, "openai": true, "google": true, "codex": true}
	for _, p := range providerOptions {
		if !want[p.ID] {
			t.Errorf("想定外のプロバイダ: %q", p.ID)
		}
		delete(want, p.ID)
		if p.DisplayName == "" {
			t.Errorf("%s: 表示名が空", p.ID)
		}
		if !strings.HasPrefix(p.PolicyURL, "https://") {
			t.Errorf("%s: ポリシー参照先が https でない: %q", p.ID, p.PolicyURL)
		}
	}
	if len(want) != 0 {
		t.Errorf("欠けているプロバイダ: %v", want)
	}
	// 既知プロバイダ判定は「その OS で選べるもの」と一致する。
	for _, p := range availableProviderOptions(runtime.GOOS) {
		if !isKnownProvider(p.ID) {
			t.Errorf("%s: 既知プロバイダ判定に漏れている", p.ID)
		}
	}
	if isKnownProvider("bedrock") {
		t.Error("対応外のプロバイダが受理された")
	}
}

// Codex App Server を選べるのは macOS 版だけ
// （Windows 版では、Windows 実機での検証が済むまで提供しない）。
func TestCodexIsOfferedOnMacOSOnly(t *testing.T) {
	has := func(list []ProviderOption, id string) bool {
		for _, p := range list {
			if p.ID == id {
				return true
			}
		}
		return false
	}
	if !has(availableProviderOptions("darwin"), "codex") {
		t.Error("macOS で Codex App Server を選べない")
	}
	if has(availableProviderOptions("windows"), "codex") {
		t.Error("Windows で Codex App Server が選べてしまう（Windows 実機での検証前）")
	}
	// 他のプロバイダは OS で変わらない。
	for _, id := range []string{"anthropic", "openai", "google"} {
		if !has(availableProviderOptions("windows"), id) {
			t.Errorf("%s が Windows の一覧から落ちている", id)
		}
	}
}

// Codex App Server は付け足される送信内容を開示する。
func TestCodexProviderOptionDisclosesAddedContent(t *testing.T) {
	var codex ProviderOption
	for _, p := range providerOptions {
		if p.ID == "codex" {
			codex = p
		}
	}
	if codex.ID == "" {
		t.Fatal("Codex App Server の選択肢が無い")
	}
	for _, want := range []string{"道具の定義文", "OS の版", "CPU の種類", "識別子"} {
		if !strings.Contains(codex.Notice, want) {
			t.Errorf("注意喚起に %q が無い: %q", want, codex.Notice)
		}
	}
}

// 設定画面のプロバイダ一覧と、登録済みアダプタの一覧が一致すること。
// 片方だけを足した状態（アダプタはあるのに画面に出ない / 画面には出るが実行できない）を検知する。
func TestProviderOptionsMatchRegisteredAdapters(t *testing.T) {
	registered := map[string]bool{}
	for _, id := range aiprovider.RegisteredProviders() {
		registered[string(id)] = true
	}
	if len(registered) == 0 {
		t.Fatal("アダプタが 1 つも登録されていない（adapters の空白 import が外れている）")
	}
	options := map[string]bool{}
	for _, p := range providerOptions {
		options[p.ID] = true
		if !registered[p.ID] {
			t.Errorf("設定画面のプロバイダ %q に対応するアダプタが登録されていない", p.ID)
		}
	}
	for id := range registered {
		if !options[id] {
			t.Errorf("アダプタ %q が設定画面のプロバイダ一覧に無い（providerOptions へ 1 行足す）", id)
		}
	}
}

// エフォートは 3 段階・既定は標準・各段階に効果説明。
func TestEffortOptions(t *testing.T) {
	opts := effortOptions()
	if len(opts) != 3 {
		t.Fatalf("段階数が違う: %d", len(opts))
	}
	defaults := 0
	for _, o := range opts {
		if o.Description == "" {
			t.Errorf("%s: 効果説明が無い", o.ID)
		}
		if o.Default {
			defaults++
			if o.ID != string(aiprovider.EffortStandard) {
				t.Errorf("既定が標準でない: %q", o.ID)
			}
		}
	}
	if defaults != 1 {
		t.Errorf("既定の段階が %d 件（1 件であること）", defaults)
	}
}

// 失敗理由は認証 / ネットワーク / その他に区別し、原因＋次の行動の 1 文で示す。
// 内部用語・生のコード値を画面に出さない。
func TestClassifyVerifyError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantReason string
	}{
		{"認証エラー", &aiprovider.ProviderError{Class: aiprovider.ErrClassConfig, HTTPStatus: 401, Code: "invalid_api_key"}, "認証エラー"},
		{"ネットワークエラー", &aiprovider.ProviderError{Class: aiprovider.ErrClassTransient, Message: "dial tcp"}, "ネットワークエラー"},
		{"恒久的エラー", &aiprovider.ProviderError{Class: aiprovider.ErrClassPermanent, HTTPStatus: 400}, "その他"},
		{"キー未設定", keymanager.ErrKeyNotSet, "キー未設定"},
		{"未分類", errors.New("boom"), "その他"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason, detail := classifyVerifyError(c.err)
			if reason != c.wantReason {
				t.Errorf("区分が違う: got %q, want %q", reason, c.wantReason)
			}
			if detail == "" {
				t.Fatal("利用者向けの説明が空")
			}
			if !strings.HasSuffix(detail, "。") {
				t.Errorf("1 文になっていない: %q", detail)
			}
			for _, leak := range []string{"invalid_api_key", "dial tcp", "401", "ErrClass", "boom"} {
				if strings.Contains(detail, leak) {
					t.Errorf("内部用語・コード値が漏れている（%q）: %q", leak, detail)
				}
			}
		})
	}
}

// キー参照名は <ProviderID>/<参照名>。参照名の既定は「既定」。
func TestKeyRef(t *testing.T) {
	ref, err := keyRef("anthropic", "本番用")
	if err != nil || ref.String() != "anthropic/本番用" {
		t.Errorf("参照名が違う: %q %v", ref.String(), err)
	}
	ref, err = keyRef("openai", "")
	if err != nil || ref.String() != "openai/既定" {
		t.Errorf("既定の参照名が違う: %q %v", ref.String(), err)
	}
	if _, err := keyRef("", "x"); err == nil {
		t.Error("プロバイダ空の参照名が受理された")
	}
}

func TestUpsertProvider(t *testing.T) {
	list := []projectstore.ProviderSetting{
		{Label: "A", Provider: "anthropic", Model: "m1", Effort: "standard", KeyRef: "anthropic/A"},
	}
	list = upsertProvider(list, projectstore.ProviderSetting{Label: "B", Provider: "openai", Model: "m2", Effort: "low", KeyRef: "openai/B"})
	if len(list) != 2 {
		t.Fatalf("追加されていない: %+v", list)
	}
	list = upsertProvider(list, projectstore.ProviderSetting{Label: "A", Provider: "anthropic", Model: "m9", Effort: "high", KeyRef: "anthropic/A"})
	if len(list) != 2 {
		t.Fatalf("同じ表示名で重複追加された: %+v", list)
	}
	if list[0].Model != "m9" || list[0].Effort != "high" {
		t.Errorf("置き換わっていない: %+v", list[0])
	}
}

func TestToModelOptions(t *testing.T) {
	got := toModelOptions([]aiprovider.ModelInfo{
		{ID: "m-primary", DisplayName: "Primary", ContextWindow: 100, MaxOutput: 10, Tier: aiprovider.TierPrimary, DefaultForTier: true},
		{ID: "m-other", Tier: aiprovider.TierOther},
	})
	if len(got) != 2 {
		t.Fatalf("件数が違う: %d", len(got))
	}
	if !got[0].Recommended {
		t.Error("上位の既定モデルが推奨として示されない")
	}
	if got[1].DisplayName != "m-other" {
		t.Errorf("表示名が無いモデルで ID にフォールバックしない: %q", got[1].DisplayName)
	}
	if got[1].Recommended {
		t.Error("既定でないモデルが推奨になっている")
	}
}
