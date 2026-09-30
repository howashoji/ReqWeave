package projectstore

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ExchangeKeyLen は交換鍵（X25519）の鍵長。
const ExchangeKeyLen = 32

// ExchangeKeys は exchange-keys.yaml（質問票返送の復号鍵）。
//
// 質問票の返送ファイルを復号するためのプロジェクト固有の鍵ペア。
// シークレットキー（AIプロバイダ）とは無関係で、OS セキュアストレージには置かない
// （プロジェクトのメンバー全員が取込に使うため端末非依存のプロジェクトデータとする）。
// 受け渡しファイル・エクスポートには含めない（含めると返送の保護が無効化する）。
// バックアップ・自動退避には含める（復元後に取込を継続できるため）。
type ExchangeKeys struct {
	PublicKey  string `yaml:"public_key"`  // base64（標準符号・パディングあり）
	PrivateKey string `yaml:"private_key"` // base64（同上）
}

// NewExchangeKeys は鍵バイト列から exchange-keys.yaml の内容を組み立てる。
func NewExchangeKeys(public, private []byte) (*ExchangeKeys, error) {
	if len(public) != ExchangeKeyLen || len(private) != ExchangeKeyLen {
		return nil, fmt.Errorf("交換鍵の長さが不正です")
	}
	return &ExchangeKeys{
		PublicKey:  base64.StdEncoding.EncodeToString(public),
		PrivateKey: base64.StdEncoding.EncodeToString(private),
	}, nil
}

// Keys は鍵バイト列を返す。
func (k *ExchangeKeys) Keys() (public, private []byte, err error) {
	public, err = decodeExchangeKey(k.PublicKey, "公開鍵")
	if err != nil {
		return nil, nil, err
	}
	private, err = decodeExchangeKey(k.PrivateKey, "秘密鍵")
	if err != nil {
		return nil, nil, err
	}
	return public, private, nil
}

func decodeExchangeKey(v, label string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return nil, fmt.Errorf("%s の %s を読み取れません", FileExchangeKeys, label)
	}
	if len(b) != ExchangeKeyLen {
		return nil, fmt.Errorf("%s の %s の長さが不正です", FileExchangeKeys, label)
	}
	return b, nil
}

// Marshal は exchange-keys.yaml のバイト列を組み立てる。
func (k *ExchangeKeys) Marshal() ([]byte, error) {
	if _, _, err := k.Keys(); err != nil {
		return nil, err
	}
	out, err := yaml.Marshal(k)
	if err != nil {
		return nil, fmt.Errorf("%s を組み立てられません: %w", FileExchangeKeys, err)
	}
	return out, nil
}

// UnmarshalExchangeKeys は exchange-keys.yaml のバイト列を解釈する。
func UnmarshalExchangeKeys(data []byte) (*ExchangeKeys, error) {
	var k ExchangeKeys
	if err := yaml.Unmarshal(data, &k); err != nil {
		return nil, fmt.Errorf("%s を解釈できません: %w", FileExchangeKeys, err)
	}
	if _, _, err := k.Keys(); err != nil {
		return nil, err
	}
	return &k, nil
}

// ReadExchangeKeys は交換鍵を読む。ファイルが無い場合は (nil, nil) を返す
// （交換鍵を導入する前に作成したプロジェクトには存在しない = 遅延生成の対象）。
func (s *Store) ReadExchangeKeys() (*ExchangeKeys, error) {
	data, err := os.ReadFile(filepath.Join(s.root, FileExchangeKeys))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s を読み込めません: %w", FileExchangeKeys, err)
	}
	return UnmarshalExchangeKeys(data)
}

// CreateExchangeKeysIfAbsent は交換鍵が無いときだけ keys を書き込み、保存された鍵を返す。
//
// 既に存在する場合は既存の鍵をそのまま返す（再生成すると発行済み質問票の返送を
// 復号できなくなるため、上書き経路を持たない）。複数端末が同時に遅延生成しても
// 排他作成で先着だけが書き込む。
func (s *Store) CreateExchangeKeysIfAbsent(keys *ExchangeKeys) (*ExchangeKeys, error) {
	if existing, err := s.ReadExchangeKeys(); err != nil || existing != nil {
		return existing, err
	}
	data, err := keys.Marshal()
	if err != nil {
		return nil, err
	}
	if err := writeFileExclusive(filepath.Join(s.root, FileExchangeKeys), data); err != nil {
		return nil, err
	}
	// 先着が別端末だった場合も含め、保存された内容を読み直して返す。
	saved, err := s.ReadExchangeKeys()
	if err != nil {
		return nil, err
	}
	if saved == nil {
		return nil, fmt.Errorf("%s を保存できませんでした", FileExchangeKeys)
	}
	return saved, nil
}

// writeFileExclusive は「既存ファイルがあれば何もしない」書き込みを行う。
//
// 同一ディレクトリの一時ファイルへ全文を書いて fsync してからハードリンクで
// 名前を付ける（リンクは既存名があれば失敗するため、部分内容が見えることも
// 先着を上書きすることもない = 原子的書き込みと同じ性質を満たす）。
// ハードリンクに対応しない置き場では O_EXCL の直接作成へ退避する。
func writeFileExclusive(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp")
	if err != nil {
		return fmt.Errorf("一時ファイルを作成できません（%s）: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("一時ファイルへ書き込めません（%s）: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("一時ファイルを同期できません（%s）: %w", tmpName, err)
	}
	if err := tmp.Chmod(dataFileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("一時ファイルの権限を設定できません（%s）: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("一時ファイルを閉じられません（%s）: %w", tmpName, err)
	}

	switch err := os.Link(tmpName, path); {
	case err == nil:
		return syncDir(dir)
	case errors.Is(err, os.ErrExist):
		return nil // 先着がいる。既存を尊重する。
	}
	// ハードリンク非対応の置き場（一部のネットワーク共有・FAT 系）への退避。
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, dataFileMode)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("保存先を作成できません（%s）: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("保存先へ書き込めません（%s）: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("保存先を同期できません（%s）: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("保存先を閉じられません（%s）: %w", path, err)
	}
	return syncDir(dir)
}

// ---- 受け渡しモジュールから使う最小の入口（exchange.ProjectKeyStore の実装）----

// ReadExchangeKeysBytes は保存済みの交換鍵をバイト列で返す。未生成のときは (nil, nil, nil)。
func (s *Store) ReadExchangeKeysBytes() (public, private []byte, err error) {
	keys, err := s.ReadExchangeKeys()
	if err != nil || keys == nil {
		return nil, nil, err
	}
	return keys.Keys()
}

// CreateExchangeKeysIfAbsentBytes は交換鍵が無いときだけ書き込み、保存された鍵を返す。
func (s *Store) CreateExchangeKeysIfAbsentBytes(public, private []byte) (savedPublic, savedPrivate []byte, err error) {
	keys, err := NewExchangeKeys(public, private)
	if err != nil {
		return nil, nil, err
	}
	saved, err := s.CreateExchangeKeysIfAbsent(keys)
	if err != nil {
		return nil, nil, err
	}
	return saved.Keys()
}
