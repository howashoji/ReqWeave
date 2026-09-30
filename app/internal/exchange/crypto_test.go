package exchange

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

const samplePayload = "questionnaire.md の中身に相当する平文。在庫引当のタイミングを確認する。"

// 復号成功がパスコード一致判定を兼ねる（独立の検証値を持たない）。
func TestSealOpenWithPasscode(t *testing.T) {
	passcode, err := NewPasscode()
	if err != nil {
		t.Fatalf("パスコード生成に失敗: %v", err)
	}
	sealed, params, err := SealWithPasscode([]byte(samplePayload), passcode)
	if err != nil {
		t.Fatalf("暗号化に失敗: %v", err)
	}
	if bytes.Contains(sealed, []byte(samplePayload)) {
		t.Fatalf("暗号文に平文がそのまま含まれています")
	}
	if bytes.Contains(sealed, []byte(passcode)) {
		t.Fatalf("暗号文にパスコードが含まれています")
	}

	got, err := OpenWithPasscode(sealed, passcode, params)
	if err != nil {
		t.Fatalf("正しいパスコードで復号できません: %v", err)
	}
	if string(got) != samplePayload {
		t.Fatalf("復号結果が一致しません: %q", string(got))
	}
}

func TestOpenWithPasscodeRejectsWrongPasscode(t *testing.T) {
	const passcode = "AbCdEfGh2345"
	sealed, params, err := SealWithPasscode([]byte(samplePayload), passcode)
	if err != nil {
		t.Fatalf("暗号化に失敗: %v", err)
	}

	cases := map[string]string{
		"末尾 1 文字違い": "AbCdEfGh2346",
		"先頭 1 文字違い": "BbCdEfGh2345",
		"大文字小文字違い":  "abCdEfGh2345",
		"1 文字短い":    "AbCdEfGh234",
		"空":         "",
	}
	for name, wrong := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := OpenWithPasscode(sealed, wrong, params)
			if err == nil {
				t.Fatalf("誤ったパスコードで復号に成功しました: %q", string(got))
			}
			if !errors.Is(err, ErrPasscodeMismatch) {
				t.Fatalf("ErrPasscodeMismatch を期待しました: %v", err)
			}
			// エラー文に試行値・正解値を含めない。
			if wrong != "" && strings.Contains(err.Error(), wrong) {
				t.Fatalf("エラー文に試行したパスコードが含まれています: %q", err.Error())
			}
			if strings.Contains(err.Error(), passcode) {
				t.Fatalf("エラー文に正しいパスコードが含まれています: %q", err.Error())
			}
		})
	}
}

func TestOpenWithPasscodeRejectsTamperedCiphertext(t *testing.T) {
	const passcode = "AbCdEfGh2345"
	sealed, params, err := SealWithPasscode([]byte(samplePayload), passcode)
	if err != nil {
		t.Fatalf("暗号化に失敗: %v", err)
	}
	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := OpenWithPasscode(tampered, passcode, params); !errors.Is(err, ErrPasscodeMismatch) {
		t.Fatalf("改変された暗号文の復号が拒否されていません: %v", err)
	}
	if _, err := OpenWithPasscode([]byte("短い"), passcode, params); !errors.Is(err, ErrPasscodeMismatch) {
		t.Fatalf("長さ不足の暗号文の復号が拒否されていません: %v", err)
	}
}

// salt は質問票ごとに無作為生成する（再発行で新しい salt を使う）。
func TestSealWithPasscodeUsesFreshSalt(t *testing.T) {
	const passcode = "AbCdEfGh2345"
	sealed1, params1, err := SealWithPasscode([]byte(samplePayload), passcode)
	if err != nil {
		t.Fatalf("1 回目の暗号化に失敗: %v", err)
	}
	sealed2, params2, err := SealWithPasscode([]byte(samplePayload), passcode)
	if err != nil {
		t.Fatalf("2 回目の暗号化に失敗: %v", err)
	}
	if params1.Salt == params2.Salt {
		t.Fatalf("同じ salt が再利用されています")
	}
	if bytes.Equal(sealed1, sealed2) {
		t.Fatalf("同じパスコード・同じ平文で同一の暗号文が生成されています")
	}
	// 片方の salt で他方の暗号文は復号できない。
	if _, err := OpenWithPasscode(sealed2, passcode, params1); !errors.Is(err, ErrPasscodeMismatch) {
		t.Fatalf("別 salt のパラメータで復号できてしまいました: %v", err)
	}
}

// Argon2id（メモリ 64MiB 以上・反復 3 以上相当）。
func TestNewKDFParamsMeetsDesignFloor(t *testing.T) {
	params, err := NewKDFParams()
	if err != nil {
		t.Fatalf("パラメータ生成に失敗: %v", err)
	}
	if params.Algorithm != KDFAlgorithmArgon2id {
		t.Fatalf("アルゴリズムが %q です（期待 %q）", params.Algorithm, KDFAlgorithmArgon2id)
	}
	if params.MemoryKiB < 64*1024 {
		t.Fatalf("メモリが %d KiB です（要求 64MiB 以上）", params.MemoryKiB)
	}
	if params.Iterations < 3 {
		t.Fatalf("反復が %d 回です（要求 3 以上）", params.Iterations)
	}
	salt, err := base64.StdEncoding.DecodeString(params.Salt)
	if err != nil {
		t.Fatalf("salt を復号できません: %v", err)
	}
	if len(salt) < kdfSaltLen {
		t.Fatalf("salt が %d バイトです（要求 %d 以上）", len(salt), kdfSaltLen)
	}
	if err := params.Validate(); err != nil {
		t.Fatalf("既定パラメータが受理範囲外です: %v", err)
	}
}

func TestKDFParamsValidate(t *testing.T) {
	base, err := NewKDFParams()
	if err != nil {
		t.Fatalf("パラメータ生成に失敗: %v", err)
	}
	cases := map[string]func(p *KDFParams){
		"未知のアルゴリズム":         func(p *KDFParams) { p.Algorithm = "scrypt" },
		"メモリが下限未満":          func(p *KDFParams) { p.MemoryKiB = 1024 },
		"メモリが受理上限超":         func(p *KDFParams) { p.MemoryKiB = maxKDFMemoryKiB + 1 },
		"反復が下限未満":           func(p *KDFParams) { p.Iterations = 1 },
		"反復が受理上限超":          func(p *KDFParams) { p.Iterations = maxKDFIterations + 1 },
		"並列度 0":             func(p *KDFParams) { p.Parallelism = 0 },
		"salt が短い":          func(p *KDFParams) { p.Salt = base64.StdEncoding.EncodeToString([]byte("short")) },
		"salt が base64 でない": func(p *KDFParams) { p.Salt = "not base64!!" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := base
			mutate(&p)
			if err := p.Validate(); err == nil {
				t.Fatalf("受理されてはいけないパラメータが受理されました")
			}
			if _, err := OpenWithPasscode([]byte("dummy"), "AbCdEfGh2345", p); err == nil {
				t.Fatalf("不正なパラメータで復号処理が走りました")
			}
		})
	}
}

// 返送方向: 発行ファイル同梱の公開鍵で暗号化し、プロジェクトの秘密鍵でのみ復号できる。
func TestSealForRecipientAndOpenSealed(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("鍵ペア生成に失敗: %v", err)
	}
	if len(kp.Public) != KeyPairSize || len(kp.Private) != KeyPairSize {
		t.Fatalf("鍵長が %d / %d です（期待 %d）", len(kp.Public), len(kp.Private), KeyPairSize)
	}

	sealed, err := SealForRecipient([]byte(samplePayload), kp.Public)
	if err != nil {
		t.Fatalf("返送暗号化に失敗: %v", err)
	}
	if bytes.Contains(sealed, []byte(samplePayload)) {
		t.Fatalf("返送暗号文に平文がそのまま含まれています")
	}

	got, err := OpenSealed(sealed, kp)
	if err != nil {
		t.Fatalf("自プロジェクトの鍵で復号できません: %v", err)
	}
	if string(got) != samplePayload {
		t.Fatalf("復号結果が一致しません: %q", string(got))
	}

	// 別プロジェクトの鍵では復号できない（取り込み時の宛先違い検出）。
	other, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("別鍵ペア生成に失敗: %v", err)
	}
	if _, err := OpenSealed(sealed, other); !errors.Is(err, ErrReturnDecrypt) {
		t.Fatalf("別プロジェクトの鍵で復号できてしまいました: %v", err)
	}

	// 公開鍵だけでは復号できない（秘密鍵の欠落・長さ不正）。
	if _, err := OpenSealed(sealed, KeyPair{Public: kp.Public}); !errors.Is(err, ErrReturnDecrypt) {
		t.Fatalf("秘密鍵なしで復号できてしまいました: %v", err)
	}

	// 改変された返送ファイルは復号できない。
	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := OpenSealed(tampered, kp); !errors.Is(err, ErrReturnDecrypt) {
		t.Fatalf("改変された返送暗号文の復号が拒否されていません: %v", err)
	}
}

func TestSealForRecipientRejectsBadKey(t *testing.T) {
	if _, err := SealForRecipient([]byte(samplePayload), []byte("短い鍵")); err == nil {
		t.Fatalf("不正な長さの公開鍵が受理されました")
	}
}

func TestGenerateKeyPairIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 20 {
		kp, err := GenerateKeyPair()
		if err != nil {
			t.Fatalf("鍵ペア生成に失敗: %v", err)
		}
		k := string(kp.Public)
		if seen[k] {
			t.Fatalf("同じ公開鍵が 2 回生成されました")
		}
		seen[k] = true
	}
}
