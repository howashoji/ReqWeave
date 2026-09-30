package binding

// 単体テスト: 回答モードの AI 対話回答の送信範囲の事前表示。
//
// 回答モードには送信記録が無い（送信記録は担当者モードのプロジェクトにだけ残す）。この表示が「何を送るか」を知る唯一の機会であり、
// **表示の中身がバックエンド側にあること**（画面に一覧を作らないこと）も併せて固定する。

import (
	"errors"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/exchange"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

func TestRespondAIScopeListsWhatIsSentAndNotSent(t *testing.T) {
	a := &API{}
	view := a.RespondAIScope("codex")

	if len(view.Sent) == 0 || len(view.NotSent) == 0 {
		t.Fatalf("送信範囲の一覧が空: sent=%d notSent=%d", len(view.Sent), len(view.NotSent))
	}
	// 送るもの（質問・用語・回答・やり取り）の説明があること（利用者の言葉で）。
	for _, want := range []string{"全ての質問", "用語", "入力した回答", "やり取り"} {
		if !containsSubstring(view.Sent, want) {
			t.Errorf("送るものの説明に %q が無い: %v", want, view.Sent)
		}
	}
	// 送らないもの（氏名・管理番号・合言葉・キー・置き場所・担当者側の情報）の説明があること。
	for _, want := range []string{"氏名", "管理番号", "合言葉", "キー", "ファイルの置き場所", "担当者側"} {
		if !containsSubstring(view.NotSent, want) {
			t.Errorf("送らないものの説明に %q が無い: %v", want, view.NotSent)
		}
	}
	// 送信記録が残らないことの説明。
	if view.NoRecordNotice == "" {
		t.Error("送信記録が残らないことの説明が無い")
	}
	if !strings.Contains(view.NoRecordNotice, "記録は残りません") {
		t.Errorf("説明が意図と違う: %q", view.NoRecordNotice)
	}
}

func TestRespondAIScopeCarriesProviderNotice(t *testing.T) {
	a := &API{}
	// Codex は注意喚起とデータ利用ポリシーの参照先を持つ（担当者モードの設定画面と同じ内容）。
	codex := a.RespondAIScope("codex")
	if codex.Notice == "" {
		t.Error("Codex の注意喚起が添えられていない（担当者モードと同じ内容を出す）")
	}
	if codex.PolicyURL == "" {
		t.Error("データ利用ポリシーの参照先が無い")
	}
	// 未知のプロバイダでも落ちない（注意喚起は空になるだけ）。
	unknown := a.RespondAIScope("no-such-provider")
	if unknown.Notice != "" || unknown.PolicyURL != "" {
		t.Errorf("未知のプロバイダに注意喚起が付いた: %+v", unknown)
	}
	if len(unknown.Sent) == 0 {
		t.Error("未知のプロバイダで送信範囲の一覧まで消えている（範囲はプロバイダに依らない）")
	}
}

// 送信範囲の一覧を画面側が書き換えられないこと（返すのは複製）。
func TestRespondAIScopeReturnsCopy(t *testing.T) {
	a := &API{}
	first := a.RespondAIScope("codex")
	first.Sent[0] = "書き換え"
	second := a.RespondAIScope("codex")
	if second.Sent[0] == "書き換え" {
		t.Error("返した一覧を書き換えると次の呼び出しに影響する（複製を返すこと）")
	}
}

func TestRespondSpeakerLabelHidesCodeValues(t *testing.T) {
	for speaker, want := range map[string]string{"agent": "AI", "user": "あなた"} {
		if got := respondSpeakerLabel(speaker); got != want {
			t.Errorf("話者 %q の表示が違う: %q（期待 %q）", speaker, got, want)
		}
	}
	// 未知の値で生のコード値を出さない（利用者向けの画面に内部の値を出さない）。
	if got := respondSpeakerLabel("moderator"); strings.Contains(got, "moderator") {
		t.Errorf("未知の話者で生のコード値が出た: %q", got)
	}
}

func containsSubstring(list []string, want string) bool {
	for _, v := range list {
		if strings.Contains(v, want) {
			return true
		}
	}
	return false
}

// --- 有効化の前に AI を 1 回も呼ばないこと（送信範囲を確認するまで何も送らない）-------------

// countingAdapterAPI は「アダプタを 1 度でも作ったら分かる」API を返す。
//
// 疎通確認・サインイン・対話のいずれも、アダプタの生成を必ず経る。生成回数が 0 であることは
// **AI プロバイダへの呼び出しが 1 回も起きていない**ことの実証になる。
func countingAdapterAPI() (*API, *int) {
	made := 0
	a := &API{}
	a.newAdapter = func(aiprovider.ProviderID, aiprovider.KeyProvider, aiprovider.KeyRef,
		aiprovider.AdapterOptions) (aiprovider.Adapter, error) {
		made++
		return nil, errors.New("このテストではアダプタを作らせない")
	}
	return a, &made
}

func TestRespondAIRefusesEveryEntryBeforeScopeConfirmation(t *testing.T) {
	cases := map[string]func(a *API) error{
		"有効化": func(a *API) error {
			_, err := a.EnableRespondAI("anthropic", "secret_key", false)
			return err
		},
		"キー登録": func(a *API) error {
			_, err := a.RegisterRespondAIKey("anthropic", "sk-dummy-value", false)
			return err
		},
		"サインイン": func(a *API) error {
			_, err := a.StartRespondAISignIn(false)
			return err
		},
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			a, made := countingAdapterAPI()
			err := call(a)
			if err == nil {
				t.Fatal("確認前なのに通った（確認操作の前に AI を呼べてしまう）")
			}
			if !strings.Contains(err.Error(), "確認") {
				t.Errorf("何を求められているか分からない文言: %v", err)
			}
			if *made != 0 {
				t.Errorf("確認前に AI プロバイダのアダプタを %d 回作った（呼び出し 0 回でないといけない）", *made)
			}
		})
	}
}

// --- モデルの決め方（推奨 → 既知一覧の既定。利用者には選ばせない）-----------------

func TestRecommendedModelPrefersProviderDefault(t *testing.T) {
	primary := func(id string, def bool) aiprovider.ModelInfo {
		return aiprovider.ModelInfo{ID: id, Tier: aiprovider.TierPrimary, DefaultForTier: def}
	}
	other := aiprovider.ModelInfo{ID: "mini", Tier: aiprovider.TierOther}

	got, ok := recommendedModel([]aiprovider.ModelInfo{other, primary("a", false), primary("b", true)})
	if !ok || got.ID != "b" {
		t.Errorf("推奨（主系統の既定）を選んでいない: %+v ok=%v", got, ok)
	}
	// 推奨が無い一覧では主系統の先頭へ倒す（黙って空を返さない）。
	got, ok = recommendedModel([]aiprovider.ModelInfo{other, primary("a", false)})
	if !ok || got.ID != "a" {
		t.Errorf("主系統へ倒していない: %+v ok=%v", got, ok)
	}
	// 主系統が無ければ先頭。
	got, ok = recommendedModel([]aiprovider.ModelInfo{other})
	if !ok || got.ID != "mini" {
		t.Errorf("先頭へ倒していない: %+v ok=%v", got, ok)
	}
	// 空の一覧では「決められない」と返す（勝手な既定を作らない）。
	if _, ok := recommendedModel(nil); ok {
		t.Error("空の一覧からモデルを決めてしまった")
	}
}

// --- エラー文言（回答モードの様式 = 担当者へ連絡。サインインの操作に関するものだけは本人が対処する）-------------

func TestRespondAIErrorMessageUsesRespondWording(t *testing.T) {
	perr := func(code string) *aiprovider.ProviderError {
		return &aiprovider.ProviderError{Class: aiprovider.ErrClassTransient, Code: code,
			Message: "internal detail: HTTP 503 from upstream"}
	}
	// 既定は「担当者へ連絡」へ倒す（ステークホルダーは設定を直せない）。
	// 残量切れも同じ（回答モードでは残量を表示しないため、回復時刻を出しても行動につながらない）。
	for _, code := range []string{"", "rate_limited", "unauthorized", codePlanUsageExausted} {
		if got := respondAIErrorMessage(perr(code), false); got != msgRespondAIUnavailable {
			t.Errorf("code=%q で回答モードの様式になっていない: %q", code, got)
		}
	}
	// サインインの操作に関するものは本人が対処できる（設定画面ではなく相談の導線を示す）。
	if got := respondAIErrorMessage(perr(codeUnauthorized), true); got != msgRespondSignInExpired {
		t.Errorf("サインインの失効の文言が違う: %q", got)
	}
	if strings.Contains(msgRespondSignInExpired, "設定画面") {
		t.Error("回答モードに無い「設定画面」へ誘導している")
	}
	for _, code := range []string{codeSignInTimedOut, codeSignInUnavailable} {
		if got := respondAIErrorMessage(perr(code), true); got == msgRespondAIUnavailable {
			t.Errorf("code=%q が担当者連絡へ倒れている（本人が対処できる）", code)
		}
	}
	// 内部の詳細を画面へ出さない。
	for _, signedIn := range []bool{true, false} {
		for _, code := range []string{"", codeUnauthorized, "rpc_-32603"} {
			got := respondAIErrorMessage(perr(code), signedIn)
			if strings.Contains(got, "HTTP") || strings.Contains(got, code) && code != "" {
				t.Errorf("生のコード値・内部の詳細が文言に出た: %q", got)
			}
		}
	}
}

// --- 発話履歴の写し（話者 → プロバイダのロール）--------------------------------------------------------

func TestRespondAIMessagesMapSpeakersToRoles(t *testing.T) {
	sess := &exchange.RespondSession{Utterances: []projectstore.Utterance{
		{ID: "utt-00001", Speaker: projectstore.SpeakerUser, Body: "1 つめの質問の意味が分かりません。"},
		{ID: "utt-00002", Speaker: projectstore.SpeakerAgent, Body: "在庫を数える頻度を聞いています。"},
	}}
	got := respondAIMessages(sess)
	if len(got) != 2 {
		t.Fatalf("発話の数が合わない: %d", len(got))
	}
	if got[0].Role != aiprovider.RoleUser || got[1].Role != aiprovider.RoleAssistant {
		t.Errorf("話者の写しが違う: %+v", got)
	}
	if got[0].Content != sess.Utterances[0].Body {
		t.Errorf("本文が変わっている: %q", got[0].Content)
	}
}

// --- AI の応答を回答欄へ移す------------------------------------------------

func draftAPI() *API {
	a := &API{}
	a.respond.session = &exchange.RespondSession{
		Questionnaire: &projectstore.Questionnaire{
			ID: "QS-007",
			Questions: []projectstore.Question{
				{ID: "q-01", AnswerFormat: projectstore.AnswerFormatFree, Text: "連絡経路を教えてください。"},
				{ID: "q-02", AnswerFormat: projectstore.AnswerFormatChoice,
					Choices: []string{"ある", "ない"}, Text: "基準は文書化されていますか。"},
				{ID: "q-03", AnswerFormat: projectstore.AnswerFormatChoiceWithFree,
					Choices: []string{"毎日", "月次"}, Text: "頻度を教えてください。"},
			},
		},
		Answers: &projectstore.Answers{Answers: []projectstore.Answer{
			{QuestionID: "q-01", Kind: projectstore.AnswerKindAnswered, FreeText: "既に書いた内容。"},
		}},
		Utterances: []projectstore.Utterance{
			{ID: "utt-00001", Speaker: projectstore.SpeakerUser, Body: "本人の発話。"},
			{ID: "utt-00002", Speaker: projectstore.SpeakerAgent, Body: "AI がまとめた下書き。"},
		},
	}
	return a
}

func TestRespondAIAnswerDraftReplacesAndAppends(t *testing.T) {
	a := draftAPI()

	got, err := a.RespondAIAnswerDraft("q-01", "utt-00002", RespondDraftReplace)
	if err != nil {
		t.Fatal(err)
	}
	if got.FreeText != "AI がまとめた下書き。" {
		t.Errorf("置き換えになっていない: %q", got.FreeText)
	}
	if got.Notice == "" || !strings.Contains(got.Notice, "下書き") {
		t.Errorf("下書きである旨の案内が無い: %q", got.Notice)
	}

	got, err = a.RespondAIAnswerDraft("q-01", "utt-00002", RespondDraftAppend)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.FreeText, "既に書いた内容。") ||
		!strings.HasSuffix(got.FreeText, "AI がまとめた下書き。") {
		t.Errorf("末尾へ足していない: %q", got.FreeText)
	}

	// **保存はしない**（回答欄で本人が確認・編集して初めて回答になる）。
	saved := a.respond.session.Answers.Answers[0].FreeText
	if saved != "既に書いた内容。" {
		t.Errorf("アプリが勝手に回答を書き換えた: %q", saved)
	}
}

func TestRespondAIAnswerDraftRefusesUnsafeTargets(t *testing.T) {
	a := draftAPI()
	cases := map[string]struct {
		questionID, utteranceID, mode, want string
	}{
		"選択肢形式には入れない":  {"q-02", "utt-00002", RespondDraftReplace, "ご自身で"},
		"入れ方を選ばせる":     {"q-01", "utt-00002", "", "入れ方"},
		"本人の発話は移さない":   {"q-01", "utt-00001", RespondDraftReplace, "見つかりません"},
		"知らない質問へは書かない": {"q-99", "utt-00002", RespondDraftReplace, "見つかりません"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := a.RespondAIAnswerDraft(c.questionID, c.utteranceID, c.mode)
			if err == nil {
				t.Fatalf("通ってしまった: %+v", got)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("文言が意図と違う: %v", err)
			}
		})
	}
	// 選択肢＋自由記述は入れられる（自由記述欄を持つため）。
	if _, err := a.RespondAIAnswerDraft("q-03", "utt-00002", RespondDraftReplace); err != nil {
		t.Errorf("選択肢＋自由記述へ入れられない: %v", err)
	}
}

// --- サインアウト（AI 対話の画面の導線）-----------------------------------

// signOutAPI はサインイン方式で AI 対話回答を有効にした回答モードの API を返す。
func signOutAPI(stub *fakeAccountAdapter) *API {
	a := &API{}
	a.newAdapter = func(aiprovider.ProviderID, aiprovider.KeyProvider, aiprovider.KeyRef,
		aiprovider.AdapterOptions) (aiprovider.Adapter, error) {
		return stub, nil
	}
	a.respond.session = &exchange.RespondSession{
		Questionnaire: &projectstore.Questionnaire{ID: "QS-007"},
		Answers:       &projectstore.Answers{},
	}
	a.respond.aiEnabled = true
	a.respond.aiProvider = providerCodexID
	a.respond.aiAuthMethod = string(aiprovider.AuthChatGPTSignin)
	return a
}

func TestSignOutRespondAISignsOutAndDisables(t *testing.T) {
	stub := &fakeAccountAdapter{}
	a := signOutAPI(stub)

	// 有効化した状態ではサインアウトの導線を出す（認証方式の判定は画面にさせない）。
	view, err := a.RespondAIDialogue()
	if err != nil {
		t.Fatal(err)
	}
	if !view.CanSignOut {
		t.Fatal("サインイン方式なのにサインアウトの導線が出ない")
	}

	view, err = a.SignOutRespondAI()
	if err != nil {
		t.Fatalf("サインアウトできない: %v", err)
	}
	if _, _, _, signOut, _ := stub.counts(); signOut != 1 {
		t.Errorf("AI プロバイダ側のサインアウトが %d 回（1 回のはず。認証情報を消すのは相手側）", signOut)
	}
	if view.Enabled || view.CanSignOut {
		t.Errorf("サインアウト後も有効なまま: %+v", view)
	}
}

func TestSignOutRespondAIRefusesWhenNotSignedIn(t *testing.T) {
	stub := &fakeAccountAdapter{}
	a := signOutAPI(stub)
	a.respond.aiAuthMethod = string(aiprovider.AuthSecretKey) // シークレットキー方式

	view, err := a.RespondAIDialogue()
	if err != nil {
		t.Fatal(err)
	}
	if view.CanSignOut {
		t.Error("シークレットキー方式でサインアウトの導線が出た")
	}
	if _, err := a.SignOutRespondAI(); err == nil {
		t.Fatal("サインインしていないのにサインアウトできた")
	}
	if _, _, _, signOut, _ := stub.counts(); signOut != 0 {
		t.Errorf("サインインしていないのに相手側を呼んだ: %d 回", signOut)
	}
}

// --- 担当者からのキー供給・サインイン代行の経路が存在しないこと-----------
//
// 回答モードの AI は**ステークホルダー自身の資格**でだけ動く。担当者のアプリ設定に登録された
// プロバイダ設定（キーへの参照名・認証方式）へ手が届くと、**担当者のキーで動かせてしまう**。
// 文言や運用ではなく、呼び出し関係で「届かない」ことを固定する（go/ast の走査は usage_guard_test.go と同じ部品）。
func TestRespondAINeverReachesOwnerProviderSettings(t *testing.T) {
	calls, exported := funcCalls(t, ".")

	// 担当者モードの資格へ到達する口（ここへ届いたら担当者のキーを使えてしまう）。
	ownerCredentials := map[string]bool{
		"adapterFor":                      true, // 既定のプロバイダ設定からアダプタとキー参照名を組む
		"settings.DefaultProviderSetting": true, // 既定のプロバイダ設定そのもの
		"newEngine":                       true, // 担当者モードの対話エンジン（Store 必須）
		"storedAuthMethod":                true, // 保存済みの認証方式（回答モードには設定が無い）
	}
	// 走査が空振りしていないこと（担当者モード側からは到達するはず）。
	if !reachesAny(calls, "OpenDialogueProject", ownerCredentials) {
		t.Fatal("担当者モードの経路からも到達しない（走査が壊れている）")
	}

	entries := []string{
		"RespondAIScope", "RespondAIProviders", "RespondAIDialogue",
		"RegisterRespondAIKey", "StartRespondAISignIn", "EnableRespondAI", "DisableRespondAI",
		"SendRespondAIMessage", "CancelRespondAIMessage", "RespondAIAnswerDraft", "SignOutRespondAI",
	}
	for _, name := range entries {
		if !exported[name] {
			t.Errorf("回答モードの公開バインディング %s が存在しない（名前が変わったら本テストも更新する）", name)
			continue
		}
		if reachesAny(calls, name, ownerCredentials) {
			t.Errorf("%s が担当者のプロバイダ設定へ到達している"+
				"（回答モードは本人の資格でだけ動く）", name)
		}
	}
}
