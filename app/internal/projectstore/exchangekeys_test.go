package projectstore

import (
	"bytes"
	"strings"
	"testing"
)

func sampleKeyBytes(fill byte) []byte {
	b := make([]byte, ExchangeKeyLen)
	for i := range b {
		b[i] = fill + byte(i)
	}
	return b
}

func TestExchangeKeysRoundTrip(t *testing.T) {
	pub, priv := sampleKeyBytes(1), sampleKeyBytes(100)
	keys, err := NewExchangeKeys(pub, priv)
	if err != nil {
		t.Fatalf("組み立てに失敗: %v", err)
	}
	data, err := keys.Marshal()
	if err != nil {
		t.Fatalf("Marshal に失敗: %v", err)
	}
	// 鍵はバイナリなので base64 で保持する（YAML に生バイトを書かない）。
	if bytes.Contains(data, pub) || bytes.Contains(data, priv) {
		t.Fatalf("YAML に鍵の生バイト列が含まれています")
	}

	restored, err := UnmarshalExchangeKeys(data)
	if err != nil {
		t.Fatalf("Unmarshal に失敗: %v", err)
	}
	gotPub, gotPriv, err := restored.Keys()
	if err != nil {
		t.Fatalf("鍵を取り出せません: %v", err)
	}
	if !bytes.Equal(gotPub, pub) || !bytes.Equal(gotPriv, priv) {
		t.Fatalf("往復で鍵が変化しました")
	}
}

func TestNewExchangeKeysRejectsBadLength(t *testing.T) {
	cases := map[string][2][]byte{
		"公開鍵が短い": {sampleKeyBytes(1)[:16], sampleKeyBytes(100)},
		"秘密鍵が短い": {sampleKeyBytes(1), sampleKeyBytes(100)[:31]},
		"どちらも空":  {nil, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewExchangeKeys(c[0], c[1]); err == nil {
				t.Fatalf("不正な鍵長が受理されました")
			}
		})
	}
}

func TestUnmarshalExchangeKeysRejectsBrokenFile(t *testing.T) {
	cases := map[string]string{
		"base64 でない":    "public_key: \"not base64!!\"\nprivate_key: \"AAAA\"\n",
		"鍵長が足りない":       "public_key: \"c2hvcnQ=\"\nprivate_key: \"c2hvcnQ=\"\n",
		"キーがない":         "foo: bar\n",
		"YAML として壊れている": "public_key: [\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := UnmarshalExchangeKeys([]byte(content)); err == nil {
				t.Fatalf("壊れた %s が受理されました", FileExchangeKeys)
			}
		})
	}
}

// 交換鍵はパスコードと無関係であり、エラー文に鍵の値を出さない。
func TestExchangeKeysErrorDoesNotLeakKey(t *testing.T) {
	const secret = "c2VjcmV0LWtleS12YWx1ZS1kby1ub3QtbGVhaw=="
	_, err := UnmarshalExchangeKeys([]byte("public_key: \"" + secret + "\"\nprivate_key: \"" + secret + "\"\n"))
	if err == nil {
		t.Fatalf("鍵長不正が受理されました")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("エラー文に鍵の値が含まれています: %q", err.Error())
	}
}
