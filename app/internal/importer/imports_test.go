package importer

// 単体テスト（ファイル I/O を伴わない検証部分）。実行: make -C app test-unit

import (
	"testing"
	"time"
)

func boolPtr(b bool) *bool { return &b }

// 受け入れ条件: kind / source_format / extraction_status は列挙値以外を書き込み前に拒否する。
func TestValidateRejectsUnknownEnumValues(t *testing.T) {
	base := func() Meta {
		return Meta{
			ID: "IMP-001", Kind: KindMaterial, ImportedAt: fixedTime(),
			SourceName: "要件メモ.md", SourceFormat: FormatMD, ExtractionStatus: StatusExtracted,
		}
	}
	valid := base()
	if err := valid.Validate(); err != nil {
		t.Fatalf("正当なメタデータが弾かれた: %v", err)
	}

	for _, tc := range []struct {
		name   string
		break_ func(*Meta)
	}{
		{"kind が列挙外", func(m *Meta) { m.Kind = "document" }},
		{"kind が空", func(m *Meta) { m.Kind = "" }},
		{"source_format が列挙外", func(m *Meta) { m.SourceFormat = "csv" }},
		{"source_format が空", func(m *Meta) { m.SourceFormat = "" }},
		{"extraction_status が列挙外", func(m *Meta) { m.ExtractionStatus = "pending" }},
		{"extraction_status が空", func(m *Meta) { m.ExtractionStatus = "" }},
		{"id の形式が違う", func(m *Meta) { m.ID = "IMP-1" }},
		{"取り込み日時が無い", func(m *Meta) { m.ImportedAt = zeroTime() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base()
			tc.break_(&m)
			if err := m.Validate(); err == nil {
				t.Errorf("不正な値が受理された: %+v", m)
			}
		})
	}
}

// 受け入れ条件: from_template・classification は kind: dev-ai-feedback 以外へ書き込まない。
func TestValidateRestrictsFeedbackOnlyFields(t *testing.T) {
	for _, kind := range []Kind{KindMaterial, KindMinutes} {
		m := Meta{ID: "IMP-001", Kind: kind, ImportedAt: fixedTime(),
			SourceName: "議事録.docx", SourceFormat: FormatDocx, ExtractionStatus: StatusExtracted,
			FromTemplate: boolPtr(true)}
		if err := m.Validate(); err == nil {
			t.Errorf("%s に from_template が設定できてしまう", kind)
		}

		m = Meta{ID: "IMP-001", Kind: kind, ImportedAt: fixedTime(),
			SourceName: "議事録.docx", SourceFormat: FormatDocx, ExtractionStatus: StatusExtracted,
			Classification: ClassNewRequest}
		if err := m.Validate(); err == nil {
			t.Errorf("%s に classification が設定できてしまう", kind)
		}
	}

	// dev-ai-feedback では両方とも設定できる。
	ok := Meta{ID: "IMP-001", Kind: KindDevAIFeedback, ImportedAt: fixedTime(),
		SourceName: "feedback.md", SourceFormat: FormatMD, ExtractionStatus: StatusExtracted,
		FromTemplate: boolPtr(true), Classification: ClassRequirementsGap}
	if err := ok.Validate(); err != nil {
		t.Errorf("開発AIフィードバックの正当なメタデータが弾かれた: %v", err)
	}

	// 分類の値集合も検証する。
	ng := ok
	ng.Classification = "unknown"
	if err := ng.Validate(); err == nil {
		t.Error("列挙外の分類が受理された")
	}
}

// 受け入れ条件: source_name に端末固有のパスを含めない。
func TestValidateRejectsDeviceSpecificPaths(t *testing.T) {
	for _, name := range []string{
		"/Users/sato/Desktop/要件.md",
		`C:\Users\sato\要件.md`,
		"../要件.md",
		"docs/要件.md",
		"",
		"  ",
		" 要件.md",
		".",
		"..",
	} {
		m := Meta{ID: "IMP-001", Kind: KindMaterial, ImportedAt: fixedTime(),
			SourceName: name, SourceFormat: FormatMD, ExtractionStatus: StatusExtracted}
		if err := m.Validate(); err == nil {
			t.Errorf("端末固有のパスが受理された: %q", name)
		}
	}
}

// 受け入れ条件: クリップボード貼り付けの source_name は clipboard 固定。
func TestValidateClipboardSourceName(t *testing.T) {
	m := Meta{ID: "IMP-001", Kind: KindMaterial, ImportedAt: fixedTime(),
		SourceName: ClipboardSourceName, SourceFormat: FormatClipboard, ExtractionStatus: StatusExtracted}
	if err := m.Validate(); err != nil {
		t.Fatalf("clipboard が弾かれた: %v", err)
	}

	m.SourceName = "貼り付け.txt"
	if err := m.Validate(); err == nil {
		t.Error("クリップボードなのに別の source_name が受理された")
	}
}

// SourcePath は形式ごとの拡張子を返す（クリップボードは txt）。
func TestSourcePathExtension(t *testing.T) {
	for _, tc := range []struct {
		format Format
		want   string
	}{
		{FormatTxt, "imports/IMP-001/source.txt"},
		{FormatMD, "imports/IMP-001/source.md"},
		{FormatDocx, "imports/IMP-001/source.docx"},
		{FormatXlsx, "imports/IMP-001/source.xlsx"},
		{FormatPptx, "imports/IMP-001/source.pptx"},
		{FormatPDF, "imports/IMP-001/source.pdf"},
		{FormatClipboard, "imports/IMP-001/source.txt"},
	} {
		if got := SourcePath("IMP-001", tc.format); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.format, got, tc.want)
		}
	}
}

// fixedTime / zeroTime はテスト用の固定時刻（TZ 依存にしない）。
func fixedTime() time.Time { return time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC) }
func zeroTime() time.Time  { return time.Time{} }
