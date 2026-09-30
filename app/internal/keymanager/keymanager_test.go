package keymanager

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// fakeBackend はテスト用の記憶（OS セキュアストレージの代わり）。
type fakeBackend struct {
	items   map[string]string
	failSet bool
	failGet bool
}

func newFake() *fakeBackend { return &fakeBackend{items: map[string]string{}} }

func (f *fakeBackend) Set(service, user, password string) error {
	if f.failSet {
		return errors.New("backend failure")
	}
	f.items[service+"\x00"+user] = password
	return nil
}

func (f *fakeBackend) Get(service, user string) (string, error) {
	if f.failGet {
		return "", errors.New("backend failure")
	}
	v, ok := f.items[service+"\x00"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (f *fakeBackend) Delete(service, user string) error {
	k := service + "\x00" + user
	if _, ok := f.items[k]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.items, k)
	return nil
}

func newTestManager() (*Manager, *fakeBackend) {
	f := newFake()
	return &Manager{service: ServiceName, backend: f}, f
}

const dummyKey = "dummy-key-value-1234"

// アカウント名は `<ProviderID>/<参照名>`。
func TestRefFormatAndParse(t *testing.T) {
	r := Ref{ProviderID: "anthropic", Label: "本番用"}
	if got := r.String(); got != "anthropic/本番用" {
		t.Errorf("参照名の形式が違う: %q", got)
	}
	parsed, err := ParseRef("anthropic/本番用")
	if err != nil || parsed != r {
		t.Errorf("解釈結果が違う: %+v %v", parsed, err)
	}
	for _, s := range []string{"", "anthropic", "/本番用", "anthropic/", " /x"} {
		if _, err := ParseRef(s); err == nil {
			t.Errorf("%q は不正だが受理された", s)
		}
	}
}

// 公開操作は Register / SecretKey / Delete / Exists の 4 つ。
func TestRegisterSecretKeyDeleteExists(t *testing.T) {
	m, _ := newTestManager()
	ref := Ref{ProviderID: "anthropic", Label: "本番用"}

	if ok, err := m.Exists(ref); err != nil || ok {
		t.Errorf("未登録なのに存在した: %v %v", ok, err)
	}
	if err := m.Register(ref, "  "+dummyKey+"\n"); err != nil {
		t.Fatalf("登録に失敗: %v", err)
	}
	got, err := m.SecretKey(context.Background(), ref)
	if err != nil {
		t.Fatalf("読み出しに失敗: %v", err)
	}
	if got != dummyKey {
		t.Errorf("前後の空白が除去されていない: %q", got)
	}
	if ok, err := m.Exists(ref); err != nil || !ok {
		t.Errorf("登録済みなのに存在しない: %v %v", ok, err)
	}

	// プロバイダごとに別のキーを登録できる
	other := Ref{ProviderID: "openai", Label: "本番用"}
	if err := m.Register(other, dummyKey+"-openai"); err != nil {
		t.Fatal(err)
	}
	if v, _ := m.SecretKey(context.Background(), other); v != dummyKey+"-openai" {
		t.Errorf("プロバイダ別の登録が混ざった: %q", v)
	}

	if err := m.Delete(ref); err != nil {
		t.Fatalf("削除に失敗: %v", err)
	}
	if _, err := m.SecretKey(context.Background(), ref); !errors.Is(err, ErrKeyNotSet) {
		t.Errorf("削除後が「キー未設定」でない: %v", err)
	}
	if err := m.Delete(ref); err != nil {
		t.Errorf("二重削除がエラーになった: %v", err)
	}
}

// OS 側で消えていた場合も「キー未設定」へ正規化する。
func TestSecretKeyNormalizesNotFound(t *testing.T) {
	m, f := newTestManager()
	ref := Ref{ProviderID: "google", Label: "検証用"}
	if err := m.Register(ref, dummyKey); err != nil {
		t.Fatal(err)
	}
	delete(f.items, ServiceName+"\x00"+ref.String()) // OS 側の削除を模す
	if _, err := m.SecretKey(context.Background(), ref); !errors.Is(err, ErrKeyNotSet) {
		t.Errorf("外部削除が「キー未設定」に正規化されない: %v", err)
	}
	if ok, err := m.Exists(ref); err != nil || ok {
		t.Errorf("外部削除後に存在と判定された: %v %v", ok, err)
	}
}

func TestRegisterRejectsEmptyKey(t *testing.T) {
	m, _ := newTestManager()
	ref := Ref{ProviderID: "anthropic", Label: "本番用"}
	if err := m.Register(ref, "   \n"); err == nil {
		t.Error("空のキーが受理された")
	}
	if err := m.Register(Ref{ProviderID: "", Label: "x"}, dummyKey); err == nil {
		t.Error("プロバイダ空の参照名が受理された")
	}
}

// エラーメッセージにキー本体を含めない。
func TestErrorsNeverContainKey(t *testing.T) {
	m, f := newTestManager()
	ref := Ref{ProviderID: "anthropic", Label: "本番用"}
	f.failSet = true
	err := m.Register(ref, dummyKey)
	if err == nil {
		t.Fatal("失敗するはずの登録が成功した")
	}
	if strings.Contains(err.Error(), dummyKey) {
		t.Errorf("エラーメッセージにキー本体が含まれる: %v", err)
	}
	f.failSet = false
	f.failGet = true
	if _, err := m.SecretKey(context.Background(), ref); err == nil {
		t.Fatal("失敗するはずの読み出しが成功した")
	} else if strings.Contains(err.Error(), dummyKey) {
		t.Errorf("エラーメッセージにキー本体が含まれる: %v", err)
	}
}

// 突き合わせは末尾 4 文字のみ。
func TestMaskKey(t *testing.T) {
	if got := MaskKey("sk-ant-abcdefgh1234"); got != "••••1234" {
		t.Errorf("マスク表記が違う: %q", got)
	}
	if got := MaskKey("abcd"); got != "••••" {
		t.Errorf("短いキーが伏せられていない: %q", got)
	}
	for _, k := range []string{"sk-ant-abcdefgh1234", "abcd", "  x  "} {
		masked := MaskKey(k)
		if strings.Contains(masked, strings.TrimSpace(k)) && len(strings.TrimSpace(k)) > 4 {
			t.Errorf("マスク結果にキー全体が含まれる: %q", masked)
		}
	}
}

// 読み出しは context を尊重する（キャンセル済みなら OS へ問い合わせない）。
func TestSecretKeyRespectsContext(t *testing.T) {
	m, _ := newTestManager()
	ref := Ref{ProviderID: "anthropic", Label: "本番用"}
	if err := m.Register(ref, dummyKey); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.SecretKey(ctx, ref); err == nil {
		t.Error("キャンセル済みの context で読み出せてしまった")
	}
}
