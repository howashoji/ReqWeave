package exchange

import "fmt"

// ProjectKeyStore は交換鍵（exchange-keys.yaml）の保持先。
//
// 受け渡しモジュールはプロジェクトデータの物理構成に直接触れず、この最小の
// 入口だけを通す（モジュール境界を保つため）。実体は projectstore.Store。
type ProjectKeyStore interface {
	// ReadExchangeKeys は保存済みの鍵を返す。未生成のときは (nil, nil, nil)。
	ReadExchangeKeysBytes() (public, private []byte, err error)
	// CreateExchangeKeysIfAbsent は鍵が無いときだけ書き込み、保存された鍵を返す。
	// 既に存在する場合は既存の鍵を返す（上書きしない）。
	CreateExchangeKeysIfAbsentBytes(public, private []byte) (savedPublic, savedPrivate []byte, err error)
}

// EnsureProjectKeyPair はプロジェクトの交換鍵ペアを返す。未生成なら生成して保存する。
//
// 鍵はプロジェクト作成時に生成するが、交換鍵を導入する前の版で作成した既存プロジェクトには
// 存在しないため、受け渡し操作（発行・取込）の前にここで遅延生成する。
// 既に鍵がある場合は再生成しない（再生成すると発行済み質問票の返送を復号できなくなる）。
func EnsureProjectKeyPair(ks ProjectKeyStore) (KeyPair, error) {
	pub, priv, err := ks.ReadExchangeKeysBytes()
	if err != nil {
		return KeyPair{}, err
	}
	if len(pub) == KeyPairSize && len(priv) == KeyPairSize {
		return KeyPair{Public: pub, Private: priv}, nil
	}
	if pub != nil || priv != nil {
		return KeyPair{}, fmt.Errorf("プロジェクトの交換鍵が壊れています。バックアップから復元してください")
	}

	generated, err := GenerateKeyPair()
	if err != nil {
		return KeyPair{}, err
	}
	savedPub, savedPriv, err := ks.CreateExchangeKeysIfAbsentBytes(generated.Public, generated.Private)
	if err != nil {
		return KeyPair{}, err
	}
	if len(savedPub) != KeyPairSize || len(savedPriv) != KeyPairSize {
		return KeyPair{}, fmt.Errorf("プロジェクトの交換鍵を保存できませんでした")
	}
	return KeyPair{Public: savedPub, Private: savedPriv}, nil
}
