package exchange

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/nacl/box"
)

// ErrPasscodeMismatch はパスコードでの復号に失敗したこと（= パスコード不一致、
// またはファイル破損）を表す（復号成功が一致判定を兼ねる）。
//
// エラー文にパスコード・導出鍵・試行値を含めない（パスコードはどこにも残さない方針のため）。
var ErrPasscodeMismatch = errors.New("パスコードが違います。システム担当者へ連絡してください（担当者は新しいパスコードで再発行できます）")

// ErrReturnDecrypt は返送ファイルのペイロードを復号できなかったことを表す（取り込みの最初の手順）。
var ErrReturnDecrypt = errors.New("返送ファイルを読み取れません（別プロジェクトの鍵、またはファイル破損）")

// KDFAlgorithmArgon2id はパスコード導出鍵のアルゴリズム名（manifest の kdf に自己記述する）。
const KDFAlgorithmArgon2id = "argon2id"

// Argon2id の既定パラメータ（許容範囲: メモリ 64MiB 以上・反復 3 以上）。
const (
	defaultKDFMemoryKiB   = 64 * 1024
	defaultKDFIterations  = 3
	defaultKDFParallelism = 4
	kdfSaltLen            = 16
	kdfKeyLen             = chacha20poly1305.KeySize
)

// 読み込むファイルが自己記述するパラメータの受理範囲。
// 下限は設計の要求（強化方向の版更新は受理する）、上限は過大なパラメータによる
// 資源枯渇を防ぐための受理上限。
const (
	maxKDFMemoryKiB   = 1024 * 1024 // 1 GiB
	maxKDFIterations  = 16
	maxKDFParallelism = 16
)

// KDFParams はパスコードから共通鍵を導出するパラメータ。
// 受け渡しファイルの manifest.yaml に平文で自己記述する（発行用のみ）。
type KDFParams struct {
	Algorithm   string `yaml:"algorithm"`
	MemoryKiB   uint32 `yaml:"memory_kib"`
	Iterations  uint32 `yaml:"iterations"`
	Parallelism uint8  `yaml:"parallelism"`
	Salt        string `yaml:"salt"` // base64（標準符号・パディングあり）
}

// NewKDFParams は質問票ごとの salt を無作為生成した既定パラメータを返す。
func NewKDFParams() (KDFParams, error) {
	salt := make([]byte, kdfSaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return KDFParams{}, fmt.Errorf("暗号化の準備に失敗しました: %w", err)
	}
	return KDFParams{
		Algorithm:   KDFAlgorithmArgon2id,
		MemoryKiB:   defaultKDFMemoryKiB,
		Iterations:  defaultKDFIterations,
		Parallelism: defaultKDFParallelism,
		Salt:        base64.StdEncoding.EncodeToString(salt),
	}, nil
}

// Validate は自己記述されたパラメータが受理範囲かを検証する。
func (p KDFParams) Validate() error {
	if p.Algorithm != KDFAlgorithmArgon2id {
		return fmt.Errorf("このファイルの暗号方式に対応していません。新しい版の本システムが必要です")
	}
	if p.MemoryKiB < defaultKDFMemoryKiB || p.MemoryKiB > maxKDFMemoryKiB {
		return fmt.Errorf("このファイルの暗号設定を扱えません（メモリ量が範囲外）")
	}
	if p.Iterations < defaultKDFIterations || p.Iterations > maxKDFIterations {
		return fmt.Errorf("このファイルの暗号設定を扱えません（反復回数が範囲外）")
	}
	if p.Parallelism < 1 || p.Parallelism > maxKDFParallelism {
		return fmt.Errorf("このファイルの暗号設定を扱えません（並列度が範囲外）")
	}
	salt, err := base64.StdEncoding.DecodeString(p.Salt)
	if err != nil || len(salt) < kdfSaltLen {
		return fmt.Errorf("このファイルの暗号設定を扱えません（salt が不正）")
	}
	return nil
}

// deriveKey はパスコードと salt から共通鍵を導出する。
// 導出鍵は呼び出しの内側だけで使い、保存も出力もしない。
func deriveKey(passcode string, p KDFParams) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	salt, err := base64.StdEncoding.DecodeString(p.Salt)
	if err != nil {
		return nil, fmt.Errorf("このファイルの暗号設定を扱えません（salt が不正）")
	}
	return argon2.IDKey([]byte(passcode), salt, p.Iterations, p.MemoryKiB, p.Parallelism, kdfKeyLen), nil
}

// SealWithPasscode はパスコード導出鍵でペイロードを暗号化する（発行方向）。
// 戻り値の KDFParams を manifest.yaml へ格納する。
func SealWithPasscode(plaintext []byte, passcode string) ([]byte, KDFParams, error) {
	params, err := NewKDFParams()
	if err != nil {
		return nil, KDFParams{}, err
	}
	sealed, err := SealWithPasscodeParams(plaintext, passcode, params)
	if err != nil {
		return nil, KDFParams{}, err
	}
	return sealed, params, nil
}

// SealWithPasscodeParams は指定したパラメータでペイロードを暗号化する（再発行時に
// 新しい salt のパラメータを渡す用途）。
func SealWithPasscodeParams(plaintext []byte, passcode string, params KDFParams) ([]byte, error) {
	key, err := deriveKey(passcode, params)
	if err != nil {
		return nil, err
	}
	return sealWithKey(plaintext, key)
}

// sealWithKey は導出済みの共通鍵で暗号化する。
//
// 鍵導出（Argon2id）は意図的に重いため、回答モードの自動保存のように
// 繰り返し暗号化する経路では導出済みの鍵を使い回す。鍵は保存も出力もしない。
func sealWithKey(plaintext, key []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("暗号化に失敗しました: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("暗号化に失敗しました: %w", err)
	}
	return aead.Seal(nonce, nonce, plaintext, nil), nil
}

// openWithKey は導出済みの共通鍵で復号する。鍵違い・破損はいずれも復号失敗になる。
func openWithKey(sealed, key []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("復号に失敗しました: %w", err)
	}
	if len(sealed) < aead.NonceSize() {
		return nil, ErrPasscodeMismatch
	}
	nonce, ct := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrPasscodeMismatch
	}
	return plaintext, nil
}

// OpenWithPasscode はパスコード導出鍵でペイロードを復号する（回答モードで開くとき）。
// 復号の成否がパスコード一致判定を兼ねる（独立の検証値を持たない。総当たりの手掛かりを増やさない）。
func OpenWithPasscode(sealed []byte, passcode string, params KDFParams) ([]byte, error) {
	key, err := deriveKey(passcode, params)
	if err != nil {
		return nil, err
	}
	return openWithKey(sealed, key)
}

// ---- 返送方向: プロジェクト交換鍵（X25519 sealed box）-------------------

// KeyPairSize は X25519 の鍵長。
const KeyPairSize = 32

// KeyPair はプロジェクトの交換鍵ペア（exchange-keys.yaml に保存する）。
type KeyPair struct {
	Public  []byte
	Private []byte
}

// GenerateKeyPair は新しい X25519 鍵ペアを生成する。
func GenerateKeyPair() (KeyPair, error) {
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("交換鍵を生成できません: %w", err)
	}
	return KeyPair{Public: pub[:], Private: priv[:]}, nil
}

// SealForRecipient は発行ファイルに同梱された公開鍵で返送ペイロードを暗号化する
// （sealed box = 一時鍵ペア + AEAD。libsodium の crypto_box_seal 互換）。
func SealForRecipient(plaintext, recipientPublic []byte) ([]byte, error) {
	if len(recipientPublic) != KeyPairSize {
		return nil, fmt.Errorf("返送先の鍵が不正です。担当者へ質問票の再発行を依頼してください")
	}
	var pub [KeyPairSize]byte
	copy(pub[:], recipientPublic)
	sealed, err := box.SealAnonymous(nil, plaintext, &pub, rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("返送ファイルの暗号化に失敗しました: %w", err)
	}
	return sealed, nil
}

// OpenSealed はプロジェクトの秘密鍵で返送ペイロードを復号する（取り込みの最初の手順）。
func OpenSealed(sealed []byte, kp KeyPair) ([]byte, error) {
	if len(kp.Public) != KeyPairSize || len(kp.Private) != KeyPairSize {
		return nil, ErrReturnDecrypt
	}
	var pub, priv [KeyPairSize]byte
	copy(pub[:], kp.Public)
	copy(priv[:], kp.Private)
	plaintext, ok := box.OpenAnonymous(nil, sealed, &pub, &priv)
	if !ok {
		return nil, ErrReturnDecrypt
	}
	return plaintext, nil
}
