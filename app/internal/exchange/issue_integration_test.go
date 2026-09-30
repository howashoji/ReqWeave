//go:build integration

// 結合テスト（実ファイル I/O）。実行: make -C app test-integration

package exchange_test

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/exchange"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 秘匿すべき内容（発行用ファイルへ入れてはならないもの = ExcludedFromIssue の列挙）。
const (
	secretUtterance   = "対話履歴の秘密の発話です"
	secretRequirement = "要件項目の全文です"
	secretDecision    = "決定事項の本文です"
	secretOpenIssue   = "未決事項の本文です"
	secretDocument    = "成果物ドキュメントの本文です"
	secretImport      = "取り込んだ既存資料の抽出テキストです"
	secretAudit       = "監査データの中身です"
	secretOtherMember = "other.member@example.co.jp"
	secretOtherOrg    = "名簿の別の宛先の所属です"
)

func newIssueProject(t *testing.T) (*projectstore.Store, *projectstore.Questionnaire) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proj")
	// 交換鍵はプロジェクト作成時に生成する（binding.CreateProject と同じ経路）。
	kp, err := exchange.GenerateKeyPair()
	if err != nil {
		t.Fatalf("交換鍵の生成に失敗: %v", err)
	}
	keys, err := projectstore.NewExchangeKeys(kp.Public, kp.Private)
	if err != nil {
		t.Fatalf("交換鍵の組み立てに失敗: %v", err)
	}
	store, err := projectstore.CreateProject(root, projectstore.CreateOptions{
		TargetSystemName: "在庫管理システム",
		Author:           projectstore.Author{AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤"},
		ExchangeKeys:     keys,
	})
	if err != nil {
		t.Fatalf("プロジェクト作成に失敗: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// 名簿（宛先と、発行対象でない別の宛先）。
	addressee, err := store.AddStakeholder("佐藤", "営業部")
	if err != nil {
		t.Fatalf("名簿の登録に失敗: %v", err)
	}
	if _, err := store.AddStakeholder("鈴木", secretOtherOrg); err != nil {
		t.Fatalf("名簿の登録に失敗: %v", err)
	}

	// 用語（同梱対象・1 段先・無関係の 3 種）。
	terms := &projectstore.Terms{Terms: []projectstore.Term{
		{Name: "在庫引当", NameEn: "stock-allocation", Definition: "受注に対して在庫を確保すること。引当単位で行う。"},
		{Name: "引当単位", NameEn: "allocation-unit", Definition: "在庫を確保する数量の単位。"},
		{Name: "棚卸", NameEn: "stocktaking", Definition: "在庫の実数を数える作業。"},
	}}
	if err := store.SaveTerms(terms); err != nil {
		t.Fatalf("用語の保存に失敗: %v", err)
	}

	// 発行用ファイルへ入ってはならないデータを置く。
	writeSecret(t, root, "sessions/S-0001.md", secretUtterance)
	writeSecret(t, root, "requirements/FR-INV-001.md", secretRequirement)
	writeSecret(t, root, "decisions/DEC-001.md", secretDecision)
	writeSecret(t, root, "open-issues/ISS-003.md", secretOpenIssue)
	writeSecret(t, root, "documents/requirements/draft/01.md", secretDocument)
	writeSecret(t, root, "imports/IMP-001/extracted.md", secretImport)
	writeSecret(t, root, "audit/history/2026-08.k%2esato.ndjson", secretAudit)
	appendSecretMember(t, root)

	q, err := store.CreateQuestionnaire(projectstore.Questionnaire{
		AddresseeRef: addressee.ID,
		Addressee:    "佐藤（営業部）",
		IssuedBy:     "k.sato@example.co.jp",
		Questions: []projectstore.Question{
			{ID: "q-01", SourceIssue: "ISS-003", AnswerFormat: projectstore.AnswerFormatChoice,
				Choices: []string{"即時", "日次バッチ"}, Terms: []string{"在庫引当"},
				Text: "在庫の引き当ては、注文を受けた時点で行いますか。", Background: "取り消しの扱いが変わります。"},
			{ID: "q-02", SourceIssue: "ISS-004", AnswerFormat: projectstore.AnswerFormatFree,
				Text: "月末の締めで困っていることを教えてください。", Background: "対象範囲の判断に使います。"},
		},
	})
	if err != nil {
		t.Fatalf("質問票の作成に失敗: %v", err)
	}
	return store, q
}

func writeSecret(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendSecretMember(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, projectstore.FileMembers)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	members, err := projectstore.UnmarshalMembers(data)
	if err != nil {
		t.Fatal(err)
	}
	members.Members = append(members.Members, projectstore.Member{
		AuthorID: secretOtherMember, DisplayName: "別の作業者",
		Role: projectstore.RoleEditor, AddedAt: time.Now().UTC(), AddedBy: "k.sato@example.co.jp",
	})
	out, err := members.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildContent(t *testing.T, store *projectstore.Store, q *projectstore.Questionnaire) *exchange.IssueContent {
	t.Helper()
	kp, err := exchange.EnsureProjectKeyPair(store)
	if err != nil {
		t.Fatalf("交換鍵の取得に失敗: %v", err)
	}
	terms, err := store.LoadTerms()
	if err != nil {
		t.Fatal(err)
	}
	c, err := exchange.BuildIssueContent(q, terms, kp.Public)
	if err != nil {
		t.Fatalf("内容の組み立てに失敗: %v", err)
	}
	return c
}

// 発行用ファイルに入れてよいもの以外は一切含めない（列挙による全数検査）。
func TestIssuePayloadContainsOnlyAllowedEntries(t *testing.T) {
	store, q := newIssueProject(t)
	c := buildContent(t, store, q)

	dst := filepath.Join(t.TempDir(), "QS-001")
	result, err := exchange.WriteIssue(store, c, dst)
	if err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}

	container, err := exchange.ReadContainer(result.Path)
	if err != nil {
		t.Fatalf("発行ファイルを読めません: %v", err)
	}
	payload, err := container.OpenIssuePayload(result.Passcode)
	if err != nil {
		t.Fatalf("復号に失敗: %v", err)
	}
	got := payload.Names()
	want := []string{exchange.EntryContent, exchange.EntryQuestionnaire, exchange.EntryTerms}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("内容物が %v です（期待 %v）", got, want)
	}

	// 除外対象の文字列がコンテナのどこにも現れない（復号後の内容物・生バイト列の両方）。
	secrets := []string{
		secretUtterance, secretRequirement, secretDecision, secretOpenIssue,
		secretDocument, secretImport, secretAudit, secretOtherMember, secretOtherOrg,
		"棚卸",                   // 質問が参照していない用語
		"k.sato@example.co.jp", // 発行者の利用者 ID（宛先は社外を含み得る）
		"issued_by",            // 同上（フィールド名も含めない）
		"status",               // 質問票の状態
	}
	joined := strings.Join([]string{
		string(payload[exchange.EntryQuestionnaire]),
		string(payload[exchange.EntryTerms]),
		string(payload[exchange.EntryContent]),
	}, "\n")
	for _, secret := range secrets {
		if strings.Contains(joined, secret) {
			t.Fatalf("発行ファイルに含めてはならない内容が入っています: %q", secret)
		}
	}
	raw, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range append(secrets, result.Passcode) {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("発行ファイルのバイト列に %q が現れました", secret)
		}
	}

	// 秘密鍵は含めず、公開鍵だけを同梱する。
	kp, err := exchange.EnsureProjectKeyPair(store)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, kp.Private) {
		t.Fatalf("発行ファイルに交換鍵の秘密鍵が含まれています")
	}
	if !strings.Contains(string(payload[exchange.EntryContent]), "return_key") {
		t.Fatalf("content.yaml に返送用の公開鍵がありません: %s", string(payload[exchange.EntryContent]))
	}
}

// 用語は直接参照 + 1 段の閉包のみ（全用語を同梱しない）。
func TestIssueTermsClosure(t *testing.T) {
	store, q := newIssueProject(t)
	c := buildContent(t, store, q)

	var names []string
	for _, term := range c.Terms {
		names = append(names, term.Name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "在庫引当,引当単位" {
		t.Fatalf("同梱する用語が %v です（期待: 在庫引当〔直接参照〕と引当単位〔定義が参照〕のみ）", names)
	}
	if strings.Contains(string(c.TermsYAML), "棚卸") {
		t.Fatalf("参照されていない用語が同梱されています:\n%s", string(c.TermsYAML))
	}
}

// プレビューは出力対象データそのものから生成する（表示と実内容が一致する）。
func TestIssuePreviewMatchesOutput(t *testing.T) {
	store, q := newIssueProject(t)
	c := buildContent(t, store, q)

	// プレビューに使う値と、コンテナへ入る値が同一。
	payload := c.Payload()
	if !bytes.Equal(payload[exchange.EntryQuestionnaire], c.QuestionnaireMarkdown) {
		t.Fatalf("プレビューの質問票本文と出力内容が一致しません")
	}
	if !bytes.Equal(payload[exchange.EntryTerms], c.TermsYAML) {
		t.Fatalf("プレビューの用語一覧と出力内容が一致しません")
	}
	if !strings.Contains(string(c.QuestionnaireMarkdown), "在庫の引き当ては、注文を受けた時点で行いますか。") {
		t.Fatalf("プレビューに質問文がありません")
	}
	if issues := c.SourceIssues(); strings.Join(issues, ",") != "ISS-003,ISS-004" {
		t.Fatalf("発行元未決事項の一覧が違います: %v", issues)
	}
	if len(exchange.ExcludedFromIssue) == 0 {
		t.Fatalf("「含まれないもの」の定型表示がありません")
	}

	// 出力したファイルの内容がプレビューと一致する。
	dst := filepath.Join(t.TempDir(), "QS-001")
	result, err := exchange.WriteIssue(store, c, dst)
	if err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}
	container, err := exchange.ReadContainer(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := container.OpenIssuePayload(result.Passcode)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got[exchange.EntryQuestionnaire], c.QuestionnaireMarkdown) {
		t.Fatalf("出力された質問票本文がプレビューと違います")
	}
	if !bytes.Equal(got[exchange.EntryTerms], c.TermsYAML) {
		t.Fatalf("出力された用語一覧がプレビューと違います")
	}
}

// 確認操作（= WriteIssue の呼び出し）を経ずに出力しない。
func TestBuildIssueContentDoesNotWrite(t *testing.T) {
	store, q := newIssueProject(t)
	before := snapshotDir(t, store.Root())

	c := buildContent(t, store, q)
	if c == nil {
		t.Fatalf("内容が組み立てられていません")
	}
	after := snapshotDir(t, store.Root())
	if before != after {
		t.Fatalf("プレビューの組み立てでプロジェクトデータが変化しました:\n--- 前\n%s\n--- 後\n%s", before, after)
	}
}

// 発行で控えが保存され、状態は issued。再発行では新しいパスコードになる。
func TestIssueArchiveAndReissue(t *testing.T) {
	store, q := newIssueProject(t)
	c := buildContent(t, store, q)

	dir := t.TempDir()
	first, err := exchange.WriteIssue(store, c, filepath.Join(dir, "QS-001"))
	if err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}
	if !strings.HasSuffix(first.Path, exchange.ExtIssue) {
		t.Fatalf("拡張子が違います: %s", first.Path)
	}
	reloaded, err := store.LoadQuestionnaire(q.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != projectstore.QuestionnaireIssued {
		t.Fatalf("発行後の状態が %q です（期待 issued）", reloaded.Status)
	}
	archive, err := os.ReadFile(store.ExchangeArtifactPath(q.ID, first.ArchiveName))
	if err != nil {
		t.Fatalf("発行控えがありません: %v", err)
	}
	issued, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(archive, issued) {
		t.Fatalf("発行控えと出力ファイルの内容が違います")
	}

	// 再発行（新しいパスコード・新しい salt）。
	second, err := exchange.WriteIssue(store, c, filepath.Join(dir, "QS-001-again"))
	if err != nil {
		t.Fatalf("再発行に失敗: %v", err)
	}
	if second.Passcode == first.Passcode {
		t.Fatalf("再発行で同じパスコードが使われました")
	}
	again, err := exchange.ReadContainer(second.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.OpenIssuePayload(first.Passcode); err == nil {
		t.Fatalf("旧パスコードで再発行後のファイルが復号できてしまいました")
	}
	if _, err := again.OpenIssuePayload(second.Passcode); err != nil {
		t.Fatalf("新しいパスコードで復号できません: %v", err)
	}

	// 発行控えは最新の発行内容で更新される（再発行）。
	updated, err := os.ReadFile(store.ExchangeArtifactPath(q.ID, second.ArchiveName))
	if err != nil {
		t.Fatal(err)
	}
	latest, err := os.ReadFile(second.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(updated, latest) {
		t.Fatalf("再発行後の控えが更新されていません")
	}
}

// 取り込み時の突合基準（発行控えメタデータ）が保存され、パスコードを含まない。
func TestIssueRecordIsSavedWithoutPasscode(t *testing.T) {
	store, q := newIssueProject(t)
	c := buildContent(t, store, q)
	result, err := exchange.WriteIssue(store, c, filepath.Join(t.TempDir(), "QS-001"))
	if err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}

	rec, err := exchange.LoadIssueRecord(store, q.ID)
	if err != nil {
		t.Fatalf("発行控えメタデータを読めません: %v", err)
	}
	if rec == nil {
		t.Fatalf("発行控えメタデータが保存されていません")
	}
	if rec.ContentHash != c.Content.ContentHash {
		t.Fatalf("突合基準のハッシュが違います: %q（期待 %q）", rec.ContentHash, c.Content.ContentHash)
	}
	if rec.AddresseeRef != q.AddresseeRef || rec.Addressee != q.Addressee {
		t.Fatalf("宛先の基準が違います: %+v", rec)
	}

	raw, err := os.ReadFile(store.ExchangeArtifactPath(q.ID, exchange.IssueRecordName(q.ID)))
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{result.Passcode, "passcode", "kdf"} {
		if strings.Contains(string(raw), banned) {
			t.Fatalf("発行控えメタデータに %q が含まれています:\n%s", banned, string(raw))
		}
	}

	// 再発行では基準も更新される（新しい質問部・宛先に追随する）。
	q2, err := store.LoadQuestionnaire(q.ID)
	if err != nil {
		t.Fatal(err)
	}
	q2.Addressee = "佐藤 一郎（営業第一部）"
	if err := store.SaveQuestionnaire(q2); err != nil {
		t.Fatal(err)
	}
	c2 := buildContent(t, store, q2)
	if _, err := exchange.WriteIssue(store, c2, filepath.Join(t.TempDir(), "QS-001")); err != nil {
		t.Fatalf("再発行に失敗: %v", err)
	}
	updated, err := exchange.LoadIssueRecord(store, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Addressee != "佐藤 一郎（営業第一部）" || updated.ContentHash == rec.ContentHash {
		t.Fatalf("再発行で基準が更新されていません: %+v", updated)
	}
}

// snapshotDir はフォルダ内のファイル一覧と内容ハッシュを 1 つの文字列にする（変化の検出用）。
func snapshotDir(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		lines = append(lines, rel+" "+exchange.ContentHash(body))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
