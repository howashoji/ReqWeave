//go:build integration

// 結合テスト（実ファイル I/O）。実行: make -C app test-integration

package projectstore

import (
	"archive/zip"
	"bytes"
	"os"
	"path"
	"path/filepath"
	"testing"
)

func TestReadExchangeKeysAbsent(t *testing.T) {
	// 交換鍵を導入する前に作成した既存プロジェクト（exchange-keys.yaml なし）を模す。
	root := t.TempDir()
	s, err := CreateProject(root, CreateOptions{TargetSystemName: "在庫管理システム", Author: testAuthor()})
	if err != nil {
		t.Fatalf("プロジェクト作成に失敗: %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(filepath.Join(root, FileExchangeKeys)); !os.IsNotExist(err) {
		t.Fatalf("交換鍵を渡していないのにファイルが作られています: %v", err)
	}
	keys, err := s.ReadExchangeKeys()
	if err != nil {
		t.Fatalf("読み取りでエラー: %v", err)
	}
	if keys != nil {
		t.Fatalf("未生成のとき nil を期待しましたが %+v", keys)
	}
}

func TestCreateProjectWritesExchangeKeys(t *testing.T) {
	root := t.TempDir()
	pub, priv := sampleKeyBytes(7), sampleKeyBytes(70)
	given, err := NewExchangeKeys(pub, priv)
	if err != nil {
		t.Fatalf("鍵の組み立てに失敗: %v", err)
	}
	s, err := CreateProject(root, CreateOptions{
		TargetSystemName: "在庫管理システム",
		Author:           testAuthor(),
		ExchangeKeys:     given,
	})
	if err != nil {
		t.Fatalf("プロジェクト作成に失敗: %v", err)
	}
	defer s.Close()

	gotPub, gotPriv, err := s.ReadExchangeKeysBytes()
	if err != nil {
		t.Fatalf("読み取りに失敗: %v", err)
	}
	if !bytes.Equal(gotPub, pub) || !bytes.Equal(gotPriv, priv) {
		t.Fatalf("保存した鍵と読み出した鍵が一致しません")
	}
}

func TestCreateExchangeKeysIfAbsentKeepsFirstWriter(t *testing.T) {
	root := t.TempDir()
	s, err := CreateProject(root, CreateOptions{TargetSystemName: "在庫管理システム", Author: testAuthor()})
	if err != nil {
		t.Fatalf("プロジェクト作成に失敗: %v", err)
	}
	defer s.Close()

	first, err := NewExchangeKeys(sampleKeyBytes(1), sampleKeyBytes(101))
	if err != nil {
		t.Fatalf("鍵の組み立てに失敗: %v", err)
	}
	saved, err := s.CreateExchangeKeysIfAbsent(first)
	if err != nil {
		t.Fatalf("遅延生成に失敗: %v", err)
	}
	if saved.PublicKey != first.PublicKey {
		t.Fatalf("書き込んだ鍵が返りません")
	}

	// 2 回目（別端末の遅延生成に相当）は既存を尊重し、上書きしない。
	second, err := NewExchangeKeys(sampleKeyBytes(9), sampleKeyBytes(200))
	if err != nil {
		t.Fatalf("鍵の組み立てに失敗: %v", err)
	}
	again, err := s.CreateExchangeKeysIfAbsent(second)
	if err != nil {
		t.Fatalf("2 回目の呼び出しでエラー: %v", err)
	}
	if again.PublicKey != first.PublicKey || again.PrivateKey != first.PrivateKey {
		t.Fatalf("既存の鍵が上書きされました")
	}

	onDisk, err := os.ReadFile(filepath.Join(root, FileExchangeKeys))
	if err != nil {
		t.Fatalf("ファイルを読めません: %v", err)
	}
	stored, err := UnmarshalExchangeKeys(onDisk)
	if err != nil {
		t.Fatalf("保存内容を解釈できません: %v", err)
	}
	if stored.PublicKey != first.PublicKey {
		t.Fatalf("ディスク上の鍵が上書きされています")
	}
}

// バックアップには交換鍵を含める（復元後に取込を継続できるため）。
func TestBackupIncludesExchangeKeys(t *testing.T) {
	root := t.TempDir()
	keys, err := NewExchangeKeys(sampleKeyBytes(3), sampleKeyBytes(33))
	if err != nil {
		t.Fatalf("鍵の組み立てに失敗: %v", err)
	}
	s, err := CreateProject(root, CreateOptions{
		TargetSystemName: "在庫管理システム",
		Author:           testAuthor(),
		ExchangeKeys:     keys,
	})
	if err != nil {
		t.Fatalf("プロジェクト作成に失敗: %v", err)
	}
	defer s.Close()

	dest := filepath.Join(t.TempDir(), "backup.zip")
	if err := WriteManualBackupZip(root, dest); err != nil {
		t.Fatalf("バックアップに失敗: %v", err)
	}
	zr, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatalf("バックアップを読めません: %v", err)
	}
	defer zr.Close()
	found := false
	for _, f := range zr.File {
		if path.Base(f.Name) == FileExchangeKeys {
			found = true
		}
	}
	if !found {
		t.Fatalf("バックアップに %s が含まれていません", FileExchangeKeys)
	}

	// 復元後も同じ鍵で取込を継続できる。
	restored := filepath.Join(t.TempDir(), "restored")
	if _, err := RestoreZip(dest, restored); err != nil {
		t.Fatalf("復元に失敗: %v", err)
	}
	rs, err := Open(restored, testAuthor())
	if err != nil {
		t.Fatalf("復元先を開けません: %v", err)
	}
	defer rs.Close()
	pub, priv, err := rs.ReadExchangeKeysBytes()
	if err != nil {
		t.Fatalf("復元先の鍵を読めません: %v", err)
	}
	if !bytes.Equal(pub, sampleKeyBytes(3)) || !bytes.Equal(priv, sampleKeyBytes(33)) {
		t.Fatalf("復元後の鍵が一致しません")
	}
}
