//go:build integration

// 結合テスト（ドメインプリセット観点 × 実ファイル）。

package dialogue

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// newPresetEngine は業務領域を選択した状態のプロジェクトと対話エンジンを作る。
func newPresetEngine(t *testing.T, stub *stubAdapter, domains []string) (*Engine, *projectstore.Store) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "在庫管理システム")
	store, err := projectstore.CreateProject(root, projectstore.CreateOptions{
		TargetSystemName: "在庫管理システム",
		Author:           projectstore.Author{AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤"},
		DomainPresets:    domains,
	})
	if err != nil {
		t.Fatalf("プロジェクトを作成できない: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	e, err := New(Config{Store: store, Adapter: stub,
		Model:  aiprovider.ModelInfo{ID: "claude-test", ContextWindow: 200000, MaxOutput: 8192},
		Effort: aiprovider.EffortStandard})
	if err != nil {
		t.Fatalf("対話エンジンを作れない: %v", err)
	}
	return e, store
}

// seedDecisions は指定の章観点までの必須項目を決定事項で埋める。
func seedDecisions(t *testing.T, store *projectstore.Store, lastChapter string) {
	t.Helper()
	m, err := LoadMetaModel()
	if err != nil {
		t.Fatal(err)
	}
	phase, err := m.Phase(PhaseRequirements)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range phase.Chapters {
		for _, item := range c.Items {
			if _, err := store.CreateDecision(projectstore.Decision{
				TopicKey: c.TopicKey(item.ID), Body: "決めた。",
				Evidence: []string{"S-0001#utt-00001"}}); err != nil {
				t.Fatalf("決定事項を作れない: %v", err)
			}
		}
		if c.ID == lastChapter {
			return
		}
	}
}

// 選択した領域の観点が質問生成の対象論点に加わり、システムプロンプトへ注入される。
func TestGenerateQuestionUsesDomainPresets(t *testing.T) {
	stub := &stubAdapter{scripts: []string{
		"論点キー: preset/inventory/stocktaking\n質問: 棚卸はどの単位で行いますか。\n背景: 差異処理の設計に必要です。"}}
	e, store := newPresetEngine(t, stub, []string{"inventory"})
	seedDecisions(t, store, "business-flow")

	sess, err := e.StartSession(PhaseRequirements)
	if err != nil {
		t.Fatalf("セッションを開始できない: %v", err)
	}
	ch, err := e.GenerateQuestion(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("質問生成に失敗: %v", err)
	}
	events := collectEvents(t, ch)
	last := events[len(events)-1]
	if last.Kind != EventDone {
		t.Fatalf("完了しない: %+v", events)
	}

	if len(stub.reqs) != 1 {
		t.Fatalf("送信回数が違う: %d", len(stub.reqs))
	}
	system := stub.reqs[0].System
	if !strings.Contains(system, "追加の質問観点") {
		t.Errorf("プリセット観点がシステムプロンプトへ注入されていない:\n%s", system)
	}
	if !strings.Contains(system, "preset/inventory/stocktaking") ||
		!strings.Contains(system, "棚卸と差異処理") {
		t.Errorf("観点の論点キー・名称が注入されていない:\n%s", system)
	}
	// 選択していない領域の観点は載せない（送信範囲を広げない）。
	if strings.Contains(system, "preset/sales/") {
		t.Errorf("選択していない領域の観点が注入された:\n%s", system)
	}
	// 指示文の論点キーがプリセット観点になっている（対象論点に加わっている）。
	content := stub.reqs[0].Messages[0].Content
	if !strings.Contains(content, "論点キー: preset/inventory/") {
		t.Errorf("プリセット観点が対象論点として指示されていない:\n%s", content)
	}
	if last.TopicKey != "preset/inventory/stocktaking" {
		t.Errorf("提示した論点キーが違う: %q", last.TopicKey)
	}
}

// 未選択のプロジェクトは既定の章観点だけで対話が成立する。
func TestGenerateQuestionWithoutDomainPresets(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript}}
	e, store := newPresetEngine(t, stub, nil)
	seedDecisions(t, store, "business-flow")

	sess, err := e.StartSession(PhaseRequirements)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := e.GenerateQuestion(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("未選択で質問生成に失敗: %v", err)
	}
	events := collectEvents(t, ch)
	last := events[len(events)-1]
	if last.Kind != EventDone {
		t.Fatalf("未選択で対話が成立しない: %+v", events)
	}
	if IsPresetTopicKey(last.TopicKey) {
		t.Errorf("未選択なのにプリセット観点が選ばれた: %q", last.TopicKey)
	}
	if strings.Contains(stub.reqs[0].System, "追加の質問観点") {
		t.Errorf("未選択なのに観点が注入された:\n%s", stub.reqs[0].System)
	}
}

// 設定で業務領域を変更すると、以後の質問生成から反映される。
func TestDomainPresetChangeAppliesToNextQuestion(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript}}
	e, store := newPresetEngine(t, stub, nil)
	seedDecisions(t, store, "business-flow")

	sess, err := e.StartSession(PhaseRequirements)
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.GenerateQuestion(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	collectEvents(t, first) // イベントを読み切ってから次へ進む（チャネルの契約）
	// 1 回目は未選択のため観点は載らない（ここまでは前のテストと同じ）。

	if err := store.UpdateProject(func(p *projectstore.Project) error {
		p.DomainPresets = []string{"workflow"}
		return nil
	}); err != nil {
		t.Fatalf("業務領域を変更できない: %v", err)
	}

	// 次の質問生成（新しいセッション）から反映される。
	stub.scripts = append(stub.scripts, questionScript)
	next, err := e.StartSession(PhaseRequirements)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.GenerateQuestion(context.Background(), next.ID)
	if err != nil {
		t.Fatalf("変更後の質問生成に失敗: %v", err)
	}
	collectEvents(t, second)
	if len(stub.reqs) < 2 {
		t.Fatalf("2 回目の送信が無い: %d", len(stub.reqs))
	}
	// 既決論点の作り直しで送信が増えることがあるため、最後の送信を見る。
	latest := stub.reqs[len(stub.reqs)-1].System
	if !strings.Contains(latest, "preset/workflow/") {
		t.Errorf("変更した業務領域が次の質問生成へ反映されていない:\n%s", latest)
	}
	if strings.Contains(stub.reqs[0].System, "preset/workflow/") {
		t.Errorf("変更前の送信にも観点が載っている（前提が崩れている）")
	}
}
