package exchange

import (
	"testing"
	"time"
)

func sampleManifest(kind string) Manifest {
	m := Manifest{
		ExchangeFormatVersion: CurrentFormatVersion,
		Kind:                  kind,
		ProjectID:             "3f2a5b6c-0000-4000-8000-000000000001",
		QuestionnaireID:       "QS-001",
		IssuedAt:              time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC),
	}
	if kind == KindIssue {
		params, err := NewKDFParams()
		if err != nil {
			panic(err)
		}
		m.KDF = &params
	}
	return m
}

func TestManifestValidate(t *testing.T) {
	cases := map[string]func(m *Manifest){
		"形式バージョンが不正":    func(m *Manifest) { m.ExchangeFormatVersion = "いち" },
		"種別が列挙外":        func(m *Manifest) { m.Kind = "draft" },
		"プロジェクト ID がない": func(m *Manifest) { m.ProjectID = "" },
		"質問票 ID が不正":    func(m *Manifest) { m.QuestionnaireID = "QS-1" },
		"発行日時がない":       func(m *Manifest) { m.IssuedAt = time.Time{} },
		"発行用に暗号設定がない":   func(m *Manifest) { m.KDF = nil },
		"暗号設定が受理範囲外":    func(m *Manifest) { m.KDF.Iterations = 1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := sampleManifest(KindIssue)
			mutate(&m)
			if err := m.Validate(); err == nil {
				t.Fatalf("不正な manifest が受理されました")
			}
		})
	}
	issue := sampleManifest(KindIssue)
	if err := issue.Validate(); err != nil {
		t.Fatalf("正しい発行用 manifest が拒否されました: %v", err)
	}
	ret := sampleManifest(KindReturn)
	if err := ret.Validate(); err != nil {
		t.Fatalf("正しい返送用 manifest が拒否されました: %v", err)
	}
}

// 版照合: 自版より新しい major は読み書きしない。旧版・新しい minor は読む。
func TestCheckFormatVersion(t *testing.T) {
	cases := map[string]struct {
		version string
		wantErr error
	}{
		"自版":           {CurrentFormatVersion, nil},
		"新しい minor":    {"1.9", nil},
		"旧版の minor":    {"1.0", nil},
		"新しい major":    {"2.0", ErrTooNewFormat},
		"さらに新しい major": {"9.3", ErrTooNewFormat},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := checkFormatVersion(c.version)
			if c.wantErr == nil && err != nil {
				t.Fatalf("読めるはずの版が拒否されました: %v", err)
			}
			if c.wantErr != nil && err == nil {
				t.Fatalf("新しい major が受理されました")
			}
		})
	}
	if err := checkFormatVersion("こわれた"); err == nil {
		t.Fatalf("不正な版表記が受理されました")
	}
}

func TestValidatePayloadName(t *testing.T) {
	ok := []string{"questionnaire.md", "terms.yaml", "sessions/S-0001.md"}
	for _, name := range ok {
		if err := validatePayloadName(name); err != nil {
			t.Fatalf("正しい名前が拒否されました（%s）: %v", name, err)
		}
	}
	ng := []string{"", "/etc/passwd", "../escape.md", "sessions/../../escape.md", `sessions\S-0001.md`, "./questionnaire.md"}
	for _, name := range ng {
		if err := validatePayloadName(name); err == nil {
			t.Fatalf("展開先を逸脱する名前が受理されました: %q", name)
		}
	}
}

func TestPackUnpackPayload(t *testing.T) {
	p := Payload{
		"questionnaire.md":   []byte("### q-01\n在庫の引き当てはいつ行いますか。"),
		"terms.yaml":         []byte("terms:\n  - name: 在庫引当\n"),
		"sessions/S-0001.md": []byte("### utt-00001\n"),
	}
	packed, err := packPayload(p)
	if err != nil {
		t.Fatalf("zip 化に失敗: %v", err)
	}
	got, err := unpackPayload(packed)
	if err != nil {
		t.Fatalf("展開に失敗: %v", err)
	}
	if len(got) != len(p) {
		t.Fatalf("内容物の数が %d です（期待 %d）", len(got), len(p))
	}
	for name, want := range p {
		if string(got[name]) != string(want) {
			t.Fatalf("内容物が変化しました（%s）: %q", name, string(got[name]))
		}
	}
	if names := got.Names(); len(names) != 3 || names[0] != "questionnaire.md" {
		t.Fatalf("名前の一覧が違います: %v", names)
	}

	if _, err := packPayload(Payload{}); err == nil {
		t.Fatalf("空のペイロードが受理されました")
	}
	if _, err := packPayload(Payload{"../escape.md": []byte("x")}); err == nil {
		t.Fatalf("逸脱する名前が受理されました")
	}
	if _, err := unpackPayload([]byte("これは zip ではない")); err == nil {
		t.Fatalf("zip でないデータが展開されました")
	}
}
