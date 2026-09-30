package aiprovider

import (
	"context"
	"testing"
)

type fakeKeys struct{}

func (fakeKeys) SecretKey(context.Context, KeyRef) (string, error) { return "dummy", nil }

type fakeAdapter struct{ id ProviderID }

func (f fakeAdapter) ID() ProviderID                                { return f.id }
func (fakeAdapter) ListModels(context.Context) ([]ModelInfo, error) { return nil, nil }
func (fakeAdapter) VerifyKey(context.Context) error                 { return nil }
func (fakeAdapter) StreamMessage(context.Context, ChatRequest) (<-chan StreamEvent, error) {
	return nil, nil
}

// AIプロバイダの抽象化: プロバイダ追加はレジストリへの登録で完了し、
// 呼び出し側（対話エンジン・ドキュメント生成）の変更を要さない。
func TestRegistry(t *testing.T) {
	const testID ProviderID = "test-provider"
	Register(testID, func(keys KeyProvider, ref KeyRef, opts AdapterOptions) Adapter { return fakeAdapter{id: testID} })
	t.Cleanup(func() {
		registryMu.Lock()
		delete(registry, testID)
		registryMu.Unlock()
	})

	a, err := NewAdapter(testID, fakeKeys{}, "test/ref")
	if err != nil {
		t.Fatalf("生成に失敗: %v", err)
	}
	if a.ID() != testID {
		t.Errorf("ID が違う: %q", a.ID())
	}
	found := false
	for _, id := range RegisteredProviders() {
		if id == testID {
			found = true
		}
	}
	if !found {
		t.Error("登録済み一覧に現れない")
	}

	if _, err := NewAdapter("no-such-provider", fakeKeys{}, "x/y"); err == nil {
		t.Error("未登録のプロバイダでアダプタが生成された")
	}
	if _, err := NewAdapter(testID, nil, "x/y"); err == nil {
		t.Error("キーの取得先なしでアダプタが生成された")
	}

	defer func() {
		if recover() == nil {
			t.Error("二重登録が検知されない")
		}
	}()
	Register(testID, func(keys KeyProvider, ref KeyRef, opts AdapterOptions) Adapter { return fakeAdapter{id: testID} })
}
