//go:build integration

// 結合テスト（実ファイル I/O）。実行: make -C app test-integration

package exchange

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readAllOrFail(t *testing.T, r io.Reader) []byte {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const (
	questionText   = "在庫の引き当ては、注文を受けた時点で行いますか。"
	backgroundText = "取り消しの扱いが変わるため確認します。"
	addresseeText  = "佐藤（営業部）"
)

func issuePayload() Payload {
	return Payload{
		"questionnaire.md": []byte("---\nid: QS-001\naddressee: " + addresseeText + "\n---\n\n### q-01\n\n#### 質問\n" +
			questionText + "\n#### 背景説明\n" + backgroundText + "\n"),
		"terms.yaml":   []byte("terms:\n  - name: 在庫引当\n    definition: 受注に対して在庫を確保すること\n"),
		"content.yaml": []byte("content_hash: abc123\nreturn_key: c29tZS1rZXk=\n"),
	}
}

// 平文の manifest に業務情報を含めない。
func TestWriteIssueFileKeepsBusinessDataEncrypted(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "QS-001"+ExtIssue)
	passcode, err := NewPasscode()
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteIssueFile(dst, sampleManifest(KindIssue), issuePayload(), passcode); err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}

	raw, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	secrets := []string{questionText, backgroundText, addresseeText, "在庫引当", passcode}
	// 平文メタデータ（manifest.yaml を展開したもの）に業務情報が無いこと。
	manifestYAML := manifestYAMLOf(t, dst)
	for _, secret := range secrets {
		if strings.Contains(manifestYAML, secret) {
			t.Fatalf("manifest.yaml に業務情報またはパスコードが現れました（%q）:\n%s", secret, manifestYAML)
		}
	}
	// ファイル全体のバイト列にも現れない（ペイロードは暗号化済み）。
	for _, secret := range secrets {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("ファイルのバイト列に業務情報またはパスコードが現れました: %q", secret)
		}
	}

	// 平文で読めるのは manifest だけ（payload.enc は暗号化済み）。
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("コンテナを開けません: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names[manifestEntry] || !names[payloadEntry] || len(names) != 2 {
		t.Fatalf("コンテナの構成が違います: %v", names)
	}
}

// 発行 → 読み → 復号の往復。
func TestIssueFileRoundTrip(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "QS-001"+ExtIssue)
	const passcode = "AbCdEfGh2345"
	want := issuePayload()
	if err := WriteIssueFile(dst, sampleManifest(KindIssue), want, passcode); err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}

	c, err := ReadContainer(dst)
	if err != nil {
		t.Fatalf("読み込みに失敗: %v", err)
	}
	if c.Manifest.Kind != KindIssue || c.Manifest.QuestionnaireID != "QS-001" {
		t.Fatalf("manifest が違います: %+v", c.Manifest)
	}
	if c.Manifest.KDF == nil {
		t.Fatalf("発行用ファイルに暗号設定がありません")
	}

	if _, err := c.OpenIssuePayload("AbCdEfGh2346"); !errors.Is(err, ErrPasscodeMismatch) {
		t.Fatalf("誤ったパスコードで復号できてしまいました: %v", err)
	}

	got, err := c.OpenIssuePayload(passcode)
	if err != nil {
		t.Fatalf("復号に失敗: %v", err)
	}
	for name, body := range want {
		if string(got[name]) != string(body) {
			t.Fatalf("内容物が変化しました（%s）", name)
		}
	}
}

// 返送 → 読み → 復号の往復（パスコード不要）。
func TestReturnFileRoundTrip(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "QS-001"+ExtReturn)
	want := Payload{
		"questionnaire.md": issuePayload()["questionnaire.md"],
		"content.yaml":     []byte("content_hash: abc123\n"),
		"answers.md":       []byte("---\nquestionnaire_id: QS-001\n---\n\n### q-01\n- kind: answered\n"),
	}
	if err := WriteReturnFile(dst, sampleManifest(KindReturn), want, kp.Public); err != nil {
		t.Fatalf("返送ファイルの出力に失敗: %v", err)
	}

	raw, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(questionText)) {
		t.Fatalf("返送ファイルの平文に業務情報が現れました")
	}

	c, err := ReadContainer(dst)
	if err != nil {
		t.Fatalf("読み込みに失敗: %v", err)
	}
	if c.Manifest.KDF != nil {
		t.Fatalf("返送用ファイルに暗号設定が入っています（パスコードは担当者側で保持しない）")
	}

	other, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.OpenReturnPayload(other); !errors.Is(err, ErrReturnDecrypt) {
		t.Fatalf("別プロジェクトの鍵で復号できてしまいました: %v", err)
	}

	got, err := c.OpenReturnPayload(kp)
	if err != nil {
		t.Fatalf("復号に失敗: %v", err)
	}
	if string(got["answers.md"]) != string(want["answers.md"]) {
		t.Fatalf("回答が変化しました: %q", string(got["answers.md"]))
	}
}

// 発行用ファイルを取込経路へ渡したとき、内容を展開せず拒否する。
func TestOpenReturnPayloadRejectsIssueFile(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "QS-001"+ExtIssue)
	const passcode = "AbCdEfGh2345"
	if err := WriteIssueFile(dst, sampleManifest(KindIssue), issuePayload(), passcode); err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	c, err := ReadContainer(dst)
	if err != nil {
		t.Fatalf("読み込みに失敗: %v", err)
	}
	got, err := c.OpenReturnPayload(kp)
	if err == nil {
		t.Fatalf("発行用ファイルが取込経路で展開されました: %v", got.Names())
	}
	if !strings.Contains(err.Error(), "発行用ファイルです") {
		t.Fatalf("拒否の理由が示されていません: %v", err)
	}

	// 逆に、返送用ファイルを回答モードの経路へ渡しても展開しない。
	ret := filepath.Join(t.TempDir(), "QS-001"+ExtReturn)
	if err := WriteReturnFile(ret, sampleManifest(KindReturn), issuePayload(), kp.Public); err != nil {
		t.Fatal(err)
	}
	rc, err := ReadContainer(ret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rc.OpenIssuePayload(passcode); err == nil {
		t.Fatalf("返送用ファイルが回答モードの経路で展開されました")
	}
}

// 自版より新しい major は読み込まず、書き込みもしない。
func TestFormatVersionGate(t *testing.T) {
	dir := t.TempDir()
	// 書き込み: 新しい major の manifest では出力しない。
	m := sampleManifest(KindIssue)
	m.ExchangeFormatVersion = "2.0"
	dst := filepath.Join(dir, "QS-001"+ExtIssue)
	if err := WriteIssueFile(dst, m, issuePayload(), "AbCdEfGh2345"); !errors.Is(err, ErrTooNewFormat) {
		t.Fatalf("新しい major で書き込めてしまいました: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("拒否したのにファイルが作られています: %v", err)
	}

	// 読み込み: 自版で書いたファイルの manifest を改変して新しい major にする。
	valid := filepath.Join(dir, "valid"+ExtIssue)
	if err := WriteIssueFile(valid, sampleManifest(KindIssue), issuePayload(), "AbCdEfGh2345"); err != nil {
		t.Fatal(err)
	}
	tooNew := rewriteManifest(t, valid, func(y string) string {
		return replaceFormatVersion(t, y, `"2.0"`)
	})
	if _, err := ParseContainer(tooNew); !errors.Is(err, ErrTooNewFormat) {
		t.Fatalf("新しい major を読み込んでしまいました: %v", err)
	}
}

// minor 差の未知フィールドは読み書きで失われない。
func TestManifestKeepsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "QS-001"+ExtIssue)
	if err := WriteIssueFile(src, sampleManifest(KindIssue), issuePayload(), "AbCdEfGh2345"); err != nil {
		t.Fatal(err)
	}
	newer := rewriteManifest(t, src, func(y string) string {
		return replaceFormatVersion(t, y, "\"1.9\"\nfuture_field: 将来の値")
	})

	c, err := ParseContainer(newer)
	if err != nil {
		t.Fatalf("新しい minor の読み込みに失敗: %v", err)
	}
	if c.Manifest.ExchangeFormatVersion != "1.9" {
		t.Fatalf("形式バージョンが違います: %q", c.Manifest.ExchangeFormatVersion)
	}

	// 読み込んだ manifest をそのまま書き戻すと未知フィールドが残る。
	out := filepath.Join(dir, "again"+ExtReturn)
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	m := c.Manifest
	m.Kind = KindReturn
	if err := WriteReturnFile(out, m, issuePayload(), kp.Public); err != nil {
		t.Fatalf("書き戻しに失敗: %v", err)
	}
	if !strings.Contains(manifestYAMLOf(t, out), "future_field: 将来の値") {
		t.Fatalf("未知フィールドが失われました:\n%s", manifestYAMLOf(t, out))
	}
}

func TestReadContainerRejectsBrokenFile(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken"+ExtIssue)
	if err := os.WriteFile(broken, []byte("これは受け渡しファイルではありません"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadContainer(broken); err == nil {
		t.Fatalf("受け渡しファイルでないファイルが受理されました")
	}
	if _, err := ReadContainer(filepath.Join(dir, "missing"+ExtIssue)); err == nil {
		t.Fatalf("存在しないファイルが受理されました")
	}

	// payload.enc を欠いたコンテナ。
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(manifestEntry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("kind: issue\n")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseContainer(buf.Bytes()); err == nil {
		t.Fatalf("内容の欠けたコンテナが受理されました")
	}
}

// replaceFormatVersion は manifest YAML の形式バージョン行を置き換える。
func replaceFormatVersion(t *testing.T, y, value string) string {
	t.Helper()
	const key = "exchange_format_version: "
	i := strings.Index(y, key)
	if i < 0 {
		t.Fatalf("形式バージョンの行がありません:\n%s", y)
	}
	end := strings.Index(y[i:], "\n")
	if end < 0 {
		t.Fatalf("形式バージョンの行が閉じていません:\n%s", y)
	}
	return y[:i] + key + value + y[i+end:]
}

// rewriteManifest はコンテナ内の manifest.yaml だけを書き換えた新しいコンテナを返す。
func rewriteManifest(t *testing.T, src string, edit func(string) string) []byte {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body := readAllOrFail(t, rc)
		_ = rc.Close()
		if f.Name == manifestEntry {
			body = []byte(edit(string(body)))
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: f.Method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func manifestYAMLOf(t *testing.T, src string) string {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name != manifestEntry {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		return string(readAllOrFail(t, rc))
	}
	t.Fatalf("manifest がありません")
	return ""
}
