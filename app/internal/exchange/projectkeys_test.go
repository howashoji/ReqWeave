package exchange

import (
	"bytes"
	"errors"
	"testing"
)

// fakeKeyStore は ProjectKeyStore のテスト実体（実ファイル I/O は結合テストで確認する）。
type fakeKeyStore struct {
	public   []byte
	private  []byte
	readErr  error
	writeErr error
	// racePublic / racePrivate が非 nil なら「先着が別端末だった」状況を模す。
	racePublic  []byte
	racePrivate []byte
	writes      int
}

func (f *fakeKeyStore) ReadExchangeKeysBytes() ([]byte, []byte, error) {
	return f.public, f.private, f.readErr
}

func (f *fakeKeyStore) CreateExchangeKeysIfAbsentBytes(public, private []byte) ([]byte, []byte, error) {
	f.writes++
	if f.writeErr != nil {
		return nil, nil, f.writeErr
	}
	if f.racePublic != nil {
		f.public, f.private = f.racePublic, f.racePrivate
		return f.racePublic, f.racePrivate, nil
	}
	f.public, f.private = public, private
	return public, private, nil
}

func TestEnsureProjectKeyPairGeneratesWhenAbsent(t *testing.T) {
	ks := &fakeKeyStore{}
	kp, err := EnsureProjectKeyPair(ks)
	if err != nil {
		t.Fatalf("遅延生成に失敗: %v", err)
	}
	if len(kp.Public) != KeyPairSize || len(kp.Private) != KeyPairSize {
		t.Fatalf("鍵長が %d / %d です", len(kp.Public), len(kp.Private))
	}
	if ks.writes != 1 {
		t.Fatalf("書き込み回数が %d です（期待 1）", ks.writes)
	}
	// 生成した鍵で往復できる。
	sealed, err := SealForRecipient([]byte(samplePayload), kp.Public)
	if err != nil {
		t.Fatalf("生成鍵で暗号化できません: %v", err)
	}
	if _, err := OpenSealed(sealed, kp); err != nil {
		t.Fatalf("生成鍵で復号できません: %v", err)
	}
}

func TestEnsureProjectKeyPairKeepsExistingKeys(t *testing.T) {
	existing, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("鍵ペア生成に失敗: %v", err)
	}
	ks := &fakeKeyStore{public: existing.Public, private: existing.Private}

	kp, err := EnsureProjectKeyPair(ks)
	if err != nil {
		t.Fatalf("既存鍵の取得に失敗: %v", err)
	}
	if !bytes.Equal(kp.Public, existing.Public) || !bytes.Equal(kp.Private, existing.Private) {
		t.Fatalf("既存の鍵が返りません（再生成されています）")
	}
	if ks.writes != 0 {
		t.Fatalf("既存鍵があるのに書き込みが %d 回発生しました", ks.writes)
	}
}

// 別端末が先に遅延生成した場合、保存された側の鍵を使う（自分の生成鍵を使わない）。
func TestEnsureProjectKeyPairUsesFirstWriterKeys(t *testing.T) {
	winner, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("鍵ペア生成に失敗: %v", err)
	}
	ks := &fakeKeyStore{racePublic: winner.Public, racePrivate: winner.Private}

	kp, err := EnsureProjectKeyPair(ks)
	if err != nil {
		t.Fatalf("遅延生成に失敗: %v", err)
	}
	if !bytes.Equal(kp.Public, winner.Public) || !bytes.Equal(kp.Private, winner.Private) {
		t.Fatalf("先着の鍵ではなく自分の生成鍵が使われています")
	}
}

func TestEnsureProjectKeyPairRejectsBrokenKeys(t *testing.T) {
	cases := map[string]*fakeKeyStore{
		"公開鍵だけある": {public: make([]byte, KeyPairSize)},
		"鍵長が足りない": {public: make([]byte, 8), private: make([]byte, 8)},
		"読み取りエラー": {readErr: errors.New("読み取り失敗")},
		"書き込みエラー": {writeErr: errors.New("書き込み失敗")},
	}
	for name, ks := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := EnsureProjectKeyPair(ks); err == nil {
				t.Fatalf("エラーを期待しましたが nil でした")
			}
		})
	}
}
