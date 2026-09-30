package binding

import (
	"reflect"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/keymanager"
)

// 認証情報の値をフロントエンドへ渡す型を持たない（型の形で画面へ出ないことを担保する）。
// 画面へ返す型に「値」「利用者名」を運ぶフィールドや keymanager.SyncCredential を含めないことを固定する。
func TestSyncCredentialViewCarriesNoSecret(t *testing.T) {
	forbiddenTypes := []reflect.Type{reflect.TypeOf(keymanager.SyncCredential{})}
	forbiddenNames := []string{"secret", "username", "password", "token", "key"}

	var check func(t *testing.T, typ reflect.Type, path string)
	check = func(t *testing.T, typ reflect.Type, path string) {
		for typ.Kind() == reflect.Ptr || typ.Kind() == reflect.Slice {
			typ = typ.Elem()
		}
		for _, f := range forbiddenTypes {
			if typ == f {
				t.Errorf("%s が認証情報の値の型 %s を含む", path, f)
			}
		}
		if typ.Kind() != reflect.Struct {
			return
		}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			lower := strings.ToLower(field.Name)
			for _, n := range forbiddenNames {
				// "KindLabel" 等は許容。フィールド名が禁止語そのもの（または末尾に持つ）場合のみ検出する
				if lower == n || strings.HasSuffix(lower, n) {
					t.Errorf("%s.%s は認証情報を運ぶフィールド名（%s）", path, field.Name, n)
				}
			}
			check(t, field.Type, path+"."+field.Name)
		}
	}
	check(t, reflect.TypeOf(SyncCredentialView{}), "SyncCredentialView")
}

// 認証方式のラベルは値集合を網羅する（生のコード値を画面へ出さない）。
func TestSyncCredentialKindLabelsAreExhaustive(t *testing.T) {
	for _, k := range []keymanager.CredentialKind{keymanager.CredentialSSHKey, keymanager.CredentialToken} {
		if syncCredentialKindLabel[k] == "" {
			t.Errorf("認証方式 %q のラベルが無い", k)
		}
		found := false
		for _, o := range syncCredentialKindOptions {
			if o.Kind == string(k) {
				found = true
			}
		}
		if !found {
			t.Errorf("認証方式 %q が選択肢に無い", k)
		}
	}
	for _, o := range syncCredentialKindOptions {
		if err := keymanager.CredentialKind(o.Kind).Validate(); err != nil {
			t.Errorf("選択肢 %q が値集合外: %v", o.Kind, err)
		}
	}
}

// 入力不備は保存前に日本語 1 文で拒否する。保管庫・設定には触れない。
func TestRegisterSyncCredentialRejectsIncompleteInput(t *testing.T) {
	a := &API{}
	const secret = "dummy-secret-value-0000"
	cases := map[string]RegisterSyncCredentialRequest{
		"プロジェクト未指定": {Kind: "token", Secret: secret},
		"方式未指定":     {ProjectID: "p1", Secret: secret},
		"方式が値集合外":   {ProjectID: "p1", Kind: "password", Secret: secret},
		"値が空":       {ProjectID: "p1", Kind: "token", Secret: "  "},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := a.RegisterSyncCredential(req)
			if err == nil {
				t.Fatal("不備のある入力が受理された")
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("エラーに値が含まれる: %v", err)
			}
			if !strings.HasSuffix(err.Error(), "。") {
				t.Errorf("利用者向けの 1 文になっていない: %v", err)
			}
		})
	}
	if _, err := a.SyncCredentialView(" "); err == nil {
		t.Error("プロジェクト未指定の参照が受理された")
	}
	if err := a.DeleteSyncCredential(""); err == nil {
		t.Error("プロジェクト未指定の削除が受理された")
	}
}
