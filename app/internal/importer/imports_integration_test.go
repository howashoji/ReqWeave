//go:build integration

// 結合テスト（実ファイル I/O × プロジェクトストア）。実行: make -C app test-integration

package importer

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

func newTestImporter(t *testing.T) (*Importer, *projectstore.Store) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "在庫管理システム")
	s, err := projectstore.CreateProject(root, projectstore.CreateOptions{
		TargetSystemName: "在庫管理システム",
		Author:           projectstore.Author{AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤"},
	})
	if err != nil {
		t.Fatalf("プロジェクト作成に失敗: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return New(s), s
}

func materialInput(name, text string) Input {
	return Input{
		Kind: KindMaterial, SourceName: name, SourceFormat: FormatMD,
		Content: []byte(text), ExtractionStatus: StatusExtracted, ExtractedText: text,
	}
}

// 受け入れ条件1: imports/IMP-nnn/ が原本と import.yaml の構成で作られ、
// IMP-nnn は既存の最大連番 + 1 で採番される。
func TestImportCreatesFolderAndAllocatesSequentialIDs(t *testing.T) {
	im, s := newTestImporter(t)

	first, err := im.Import(materialInput("要件メモ.md", "# 要件メモ\n在庫を Excel で管理している。"))
	if err != nil {
		t.Fatalf("取り込みに失敗: %v", err)
	}
	if first.ID != "IMP-001" {
		t.Fatalf("最初の ID が %q です（期待 IMP-001）", first.ID)
	}
	second, err := im.Import(materialInput("議事録.md", "打合せ記録"))
	if err != nil {
		t.Fatalf("2 件目の取り込みに失敗: %v", err)
	}
	if second.ID != "IMP-002" {
		t.Fatalf("2 件目の ID が %q です（期待 IMP-002）", second.ID)
	}

	dir := filepath.Join(s.Root(), "imports", first.ID)
	for _, name := range []string{MetaFile, "source.md", ExtractedFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s が作られていない: %v", name, err)
		}
	}
	// 原子的書き込みの一時ファイルが残っていない。
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("取り込みフォルダを読めない: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") || strings.HasPrefix(e.Name(), ".") {
			t.Errorf("一時ファイルが残っている: %s", e.Name())
		}
	}
}

// 受け入れ条件2a: 原本のバイト列が取り込み前後で一致する。
func TestImportPreservesSourceBytes(t *testing.T) {
	im, s := newTestImporter(t)

	// テキストに見えないバイト列（PDF の断片を模す）でも 1 バイトも変えない。
	original := []byte{0x25, 0x50, 0x44, 0x46, 0x2D, 0x31, 0x2E, 0x37, 0x00, 0xFF, 0xFE, 0x0A, 0x0D, 0x1A}
	got, err := im.Import(Input{
		Kind: KindMaterial, SourceName: "仕様書.pdf", SourceFormat: FormatPDF,
		Content: original, ExtractionStatus: StatusFailed,
	})
	if err != nil {
		t.Fatalf("取り込みに失敗: %v", err)
	}

	onDisk, err := os.ReadFile(filepath.Join(s.Root(), "imports", got.ID, "source.pdf"))
	if err != nil {
		t.Fatalf("原本を読めない: %v", err)
	}
	if !bytes.Equal(onDisk, original) {
		t.Errorf("原本のバイト列が変わっている:\n書込前 %v\n書込後 %v", original, onDisk)
	}
	viaAPI, err := im.ReadSource(got.ID)
	if err != nil {
		t.Fatalf("ReadSource に失敗: %v", err)
	}
	if !bytes.Equal(viaAPI, original) {
		t.Errorf("ReadSource が返すバイト列が違う: %v", viaAPI)
	}
}

// 受け入れ条件2b: 原本を更新・上書きする公開 API が存在しない。
//
// 「更新 API を設けない」は実装の不在で担保するため、公開メソッド集合そのものを固定する。
// 取り込み以外の書き込み経路を足すとこのテストが落ちる。
func TestImporterHasNoSourceMutationAPI(t *testing.T) {
	// ImportWithExtraction は通常の取り込み経路（抽出 → 保存）。
	// SaveAnalysis / LoadAnalysis / DeleteAnalysis は取り込み分析の
	// 一時状態（analysis.meta.yaml）の読み書きで、原本・抽出テキストには触れない。
	// ResolveRef は根拠の解決（読み出しのみ）。
	// SetClassification / SummarizeFeedback は分類の付与と集計で、
	// 書き換えるのは import.yaml の classification だけ。
	// いずれも原本を書き換えない（Import 系は新規作成のみ）。
	want := []string{"DeleteAnalysis", "Import", "ImportWithExtraction", "List", "Load",
		"LoadAnalysis", "ReadExtracted", "ReadSource", "ResolveRef", "SaveAnalysis",
		"SetClassification", "SummarizeFeedback"}

	typ := reflect.TypeOf(&Importer{})
	var got []string
	for i := 0; i < typ.NumMethod(); i++ {
		got = append(got, typ.Method(i).Name)
	}
	sort.Strings(got)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("公開メソッド集合が変わりました。\n実際: %v\n期待: %v\n"+
			"原本を更新・上書きする API を足していないか確認すること（原本は取り込み後に変更しない）", got, want)
	}
}

// 受け入れ条件3: 列挙外の値は書き込み前に拒否され、フォルダも作られない。
func TestImportRejectsInvalidValuesBeforeWriting(t *testing.T) {
	im, s := newTestImporter(t)

	in := materialInput("要件メモ.md", "本文")
	in.Kind = "document"
	if _, err := im.Import(in); err == nil {
		t.Fatal("列挙外の kind が受理された")
	}

	entries, err := os.ReadDir(filepath.Join(s.Root(), "imports"))
	if err != nil {
		t.Fatalf("imports を読めない: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("拒否されたのにフォルダが作られている: %v", entries)
	}
	// 採番も消費していない（次の正当な取り込みが IMP-001 になる）。
	ok, err := im.Import(materialInput("要件メモ.md", "本文"))
	if err != nil {
		t.Fatalf("取り込みに失敗: %v", err)
	}
	if ok.ID != "IMP-001" {
		t.Errorf("拒否で採番が消費された: %s", ok.ID)
	}
}

// 受け入れ条件4: from_template / classification は dev-ai-feedback 以外へ書き込まれない。
func TestImportWritesFeedbackFieldsOnlyForFeedback(t *testing.T) {
	im, s := newTestImporter(t)

	in := materialInput("要件メモ.md", "本文")
	tr := true
	in.FromTemplate = &tr
	if _, err := im.Import(in); err == nil {
		t.Fatal("資料に from_template が設定できてしまう")
	}

	fb, err := im.Import(Input{
		Kind: KindDevAIFeedback, SourceName: "feedback.md", SourceFormat: FormatMD,
		Content: []byte("実装で困った点"), ExtractionStatus: StatusExtracted, ExtractedText: "実装で困った点",
		FromTemplate: &tr, Classification: ClassRequirementsGap,
	})
	if err != nil {
		t.Fatalf("フィードバックの取り込みに失敗: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(s.Root(), "imports", fb.ID, MetaFile))
	if err != nil {
		t.Fatalf("import.yaml を読めない: %v", err)
	}
	for _, want := range []string{"from_template: true", "classification: requirements-gap"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("import.yaml に %q が無い:\n%s", want, raw)
		}
	}

	// 資料側の import.yaml には両フィールドが現れない。
	material, err := im.Import(materialInput("要件メモ.md", "本文"))
	if err != nil {
		t.Fatalf("資料の取り込みに失敗: %v", err)
	}
	raw, err = os.ReadFile(filepath.Join(s.Root(), "imports", material.ID, MetaFile))
	if err != nil {
		t.Fatalf("import.yaml を読めない: %v", err)
	}
	for _, ng := range []string{"from_template", "classification"} {
		if strings.Contains(string(raw), ng) {
			t.Errorf("資料の import.yaml に %q が書かれている:\n%s", ng, raw)
		}
	}
}

// 受け入れ条件5: クリップボードは source_name が clipboard になり、
// ファイル取り込みでも端末固有の絶対パスが import.yaml に含まれない。
func TestImportDoesNotPersistDeviceSpecificPaths(t *testing.T) {
	im, s := newTestImporter(t)

	clip, err := im.Import(Input{
		Kind: KindMaterial, SourceFormat: FormatClipboard,
		Content: []byte("貼り付けた本文"), ExtractionStatus: StatusExtracted, ExtractedText: "貼り付けた本文",
	})
	if err != nil {
		t.Fatalf("クリップボード取り込みに失敗: %v", err)
	}
	if clip.SourceName != ClipboardSourceName {
		t.Errorf("source_name が %q です（期待 %q）", clip.SourceName, ClipboardSourceName)
	}

	// 絶対パスを渡した取り込みは拒否する（呼び出し側にベース名化を強制する）。
	in := materialInput("/Users/sato/Desktop/要件メモ.md", "本文")
	if _, err := im.Import(in); err == nil {
		t.Error("絶対パスの source_name が受理された")
	}

	raw, err := os.ReadFile(filepath.Join(s.Root(), "imports", clip.ID, MetaFile))
	if err != nil {
		t.Fatalf("import.yaml を読めない: %v", err)
	}
	for _, ng := range []string{"/Users/", `C:\`, s.Root()} {
		if strings.Contains(string(raw), ng) {
			t.Errorf("import.yaml に端末固有のパス %q が含まれる:\n%s", ng, raw)
		}
	}
}

// 受け入れ条件6: 一覧が種別・取り込み日時・抽出状態を返す。
func TestListReturnsKindImportedAtAndStatus(t *testing.T) {
	im, _ := newTestImporter(t)

	if got, err := im.List(); err != nil || len(got) != 0 {
		t.Fatalf("空の一覧が返らない: %v %v", got, err)
	}

	if _, err := im.Import(materialInput("要件メモ.md", "本文")); err != nil {
		t.Fatalf("取り込みに失敗: %v", err)
	}
	if _, err := im.Import(Input{
		Kind: KindMinutes, SourceName: "議事録.pdf", SourceFormat: FormatPDF,
		Content: []byte("%PDF-1.7"), ExtractionStatus: StatusFailed,
	}); err != nil {
		t.Fatalf("議事録の取り込みに失敗: %v", err)
	}

	list, err := im.List()
	if err != nil {
		t.Fatalf("一覧に失敗: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("一覧が %d 件です（期待 2）: %+v", len(list), list)
	}
	if list[0].ID != "IMP-001" || list[1].ID != "IMP-002" {
		t.Errorf("ID 昇順で返っていない: %+v", list)
	}
	if list[0].Kind != KindMaterial || list[1].Kind != KindMinutes {
		t.Errorf("種別が返っていない: %+v", list)
	}
	if list[0].ExtractionStatus != StatusExtracted || list[1].ExtractionStatus != StatusFailed {
		t.Errorf("抽出状態が返っていない: %+v", list)
	}
	for _, m := range list {
		if m.ImportedAt.IsZero() {
			t.Errorf("%s の取り込み日時が空", m.ID)
		}
		if m.ImportedAt.Location() != time.UTC {
			t.Errorf("%s の取り込み日時が UTC ではない: %v", m.ID, m.ImportedAt.Location())
		}
	}
}

// 抽出不能（failed）でも原本は保持し、extracted.md は作らない。
func TestFailedExtractionKeepsSourceWithoutExtractedFile(t *testing.T) {
	im, s := newTestImporter(t)

	got, err := im.Import(Input{
		Kind: KindMaterial, SourceName: "仕様書.pdf", SourceFormat: FormatPDF,
		Content: []byte("%PDF-1.7 binary"), ExtractionStatus: StatusFailed,
	})
	if err != nil {
		t.Fatalf("取り込みに失敗: %v", err)
	}

	if _, err := os.Stat(filepath.Join(s.Root(), "imports", got.ID, "source.pdf")); err != nil {
		t.Errorf("原本が保持されていない: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "imports", got.ID, ExtractedFile)); !os.IsNotExist(err) {
		t.Errorf("failed なのに extracted.md が作られている（err=%v）", err)
	}
	if _, err := im.ReadExtracted(got.ID); err == nil {
		t.Error("failed の資料から抽出テキストが読めてしまう")
	}
}

// 受け入れ条件7: import.yaml の書き込みが保存キューを経由する。
//
// キューを閉じた Store で書き込みが ErrStoreClosed になることで、直接の os.WriteFile ではなく
// キュー経由であることを示す（直書きならストアの状態と無関係に成功してしまう）。
func TestImportGoesThroughSaveQueue(t *testing.T) {
	im, s := newTestImporter(t)

	if err := s.Close(); err != nil {
		t.Fatalf("ストアを閉じられない: %v", err)
	}
	_, err := im.Import(materialInput("要件メモ.md", "本文"))
	if err == nil {
		t.Fatal("キューを閉じたのに取り込みが成功した（保存キューを経由していない）")
	}
	if !errors.Is(err, projectstore.ErrStoreClosed) {
		t.Errorf("ErrStoreClosed ではない: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(s.Root(), "imports", "IMP-001")); !os.IsNotExist(statErr) {
		t.Errorf("失敗したのにフォルダが残っている（err=%v）", statErr)
	}
}

// 抽出の受け入れ条件1: 6 経路それぞれで抽出結果が extracted.md に保存される。
func TestImportWithExtractionSavesExtractedForAllFormats(t *testing.T) {
	im, s := newTestImporter(t)

	for _, tc := range []struct {
		name    string
		format  Format
		source  string
		content []byte
		want    string
	}{
		{"txt", FormatTxt, "現状メモ.txt", []byte("在庫は Excel 台帳で管理している。\n"), "在庫は Excel 台帳で管理している。"},
		{"md", FormatMD, "現状.md", []byte("# 現状\n\n受注は EDI と Web の2経路。\n"), "受注は EDI と Web の2経路。"},
		{"clipboard", FormatClipboard, "", []byte("打合せメモ: 在庫の締めは月末。"), "打合せメモ: 在庫の締めは月末。"},
		{"docx", FormatDocx, "現状.docx", buildDocx(t, "在庫管理システムの現状"), "在庫管理システムの現状"},
		{"xlsx", FormatXlsx, "受注.xlsx", buildXlsx(t, "受注一覧", map[string]string{"A1": "受注番号"}), "A1: 受注番号"},
		{"pptx", FormatPptx, "方針.pptx", buildPptx(t, [][]string{{"刷新方針"}}, map[int]string{1: "現行は Excel 台帳"}), "## スライド 1"},
		{"pdf", FormatPDF, "spec.pdf", buildPDF(t, "Inventory system"), "Inventory system"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := im.ImportWithExtraction(Input{
				Kind: KindMaterial, SourceName: tc.source, SourceFormat: tc.format, Content: tc.content,
			})
			if err != nil {
				t.Fatalf("取り込みに失敗: %v", err)
			}
			if got.ExtractionStatus != StatusExtracted {
				t.Fatalf("extraction_status = %q（期待 extracted）", got.ExtractionStatus)
			}
			raw, err := os.ReadFile(filepath.Join(s.Root(), "imports", got.ID, ExtractedFile))
			if err != nil {
				t.Fatalf("extracted.md を読めない: %v", err)
			}
			if !strings.Contains(string(raw), tc.want) {
				t.Errorf("extracted.md に %q が無い:\n%s", tc.want, raw)
			}
		})
	}
}

// 抽出の受け入れ条件2: extracted.md が抽出結果と無加工で一致し、再読み出しでも同一。
func TestExtractedFileMatchesExtractionAndIsStable(t *testing.T) {
	im, s := newTestImporter(t)

	content := buildXlsx(t, "受注一覧", map[string]string{"A1": "受注番号", "B1": "得意先", "A2": "1001"})
	want, err := Extract(FormatXlsx, content)
	if err != nil {
		t.Fatalf("抽出に失敗: %v", err)
	}

	got, err := im.ImportWithExtraction(Input{
		Kind: KindMaterial, SourceName: "受注.xlsx", SourceFormat: FormatXlsx, Content: content,
	})
	if err != nil {
		t.Fatalf("取り込みに失敗: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(s.Root(), "imports", got.ID, ExtractedFile))
	if err != nil {
		t.Fatalf("extracted.md を読めない: %v", err)
	}
	if string(raw) != want {
		t.Errorf("extracted.md が抽出結果と一致しない（無加工でない）:\n保存: %q\n抽出: %q", raw, want)
	}
	// 再読み出しでも同一（IMP-nnn#Lm-Ln の行参照が安定する）。
	for i := 0; i < 3; i++ {
		again, err := im.ReadExtracted(got.ID)
		if err != nil {
			t.Fatalf("%d 回目の読み出しに失敗: %v", i+1, err)
		}
		if again != want {
			t.Fatalf("%d 回目の読み出しが違う:\n%q\n%q", i+1, again, want)
		}
	}
}

// 抽出の受け入れ条件4: 抽出できない資料は failed になり、extracted.md が作られず、
// 原本が保持され、既存データが変更されない。
func TestImportWithExtractionFallsBackToFailed(t *testing.T) {
	im, s := newTestImporter(t)

	// 先に正常な資料を 1 件入れておき、失敗時に変更されないことを確かめる。
	before, err := im.ImportWithExtraction(Input{
		Kind: KindMaterial, SourceName: "現状.md", SourceFormat: FormatMD, Content: []byte("# 現状\n"),
	})
	if err != nil {
		t.Fatalf("事前の取り込みに失敗: %v", err)
	}
	beforeText, err := im.ReadExtracted(before.ID)
	if err != nil {
		t.Fatalf("事前資料の抽出テキストを読めない: %v", err)
	}

	broken := []byte("%PDF-1.4 中身が壊れている")
	got, err := im.ImportWithExtraction(Input{
		Kind: KindMaterial, SourceName: "壊れた仕様書.pdf", SourceFormat: FormatPDF, Content: broken,
	})
	if err != nil {
		t.Fatalf("抽出不能でも取り込み自体は成功すべき: %v", err)
	}
	if got.ExtractionStatus != StatusFailed {
		t.Errorf("extraction_status = %q（期待 failed）", got.ExtractionStatus)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "imports", got.ID, ExtractedFile)); !os.IsNotExist(err) {
		t.Errorf("failed なのに extracted.md が作られている（err=%v）", err)
	}
	// 原本は 1 バイトも変わらず保持される。
	src, err := im.ReadSource(got.ID)
	if err != nil {
		t.Fatalf("原本を読めない: %v", err)
	}
	if !bytes.Equal(src, broken) {
		t.Errorf("原本が変わっている: %q", src)
	}
	// 既存データが変更されていない。
	afterText, err := im.ReadExtracted(before.ID)
	if err != nil {
		t.Fatalf("既存資料が読めなくなった: %v", err)
	}
	if afterText != beforeText {
		t.Errorf("既存資料の抽出テキストが変わった:\n前: %q\n後: %q", beforeText, afterText)
	}
}
