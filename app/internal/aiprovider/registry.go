package aiprovider

import (
	"fmt"
	"sort"
	"sync"
)

// Factory はアダプタを生成する関数。プロバイダ追加時は、アダプタパッケージの init から
// Register を呼び、aiprovider/adapters/adapters.go へ空白 import を 1 行足す（プロバイダの追加で上位を変えずに済むように）。
type Factory func(keys KeyProvider, ref KeyRef, opts AdapterOptions) Adapter

var (
	registryMu sync.RWMutex
	registry   = map[ProviderID]Factory{}
)

// Register はアダプタを登録する（各アダプタパッケージの init から呼ぶ）。
func Register(id ProviderID, f Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[id]; dup {
		panic("aiprovider: プロバイダが二重登録されています: " + string(id))
	}
	registry[id] = f
}

// NewAdapter は登録済みアダプタを既定のタイムアウトで生成する。
func NewAdapter(id ProviderID, keys KeyProvider, ref KeyRef) (Adapter, error) {
	return NewAdapterWithOptions(id, keys, ref, AdapterOptions{})
}

// NewAdapterWithOptions は登録済みアダプタをタイムアウト指定つきで生成する。
func NewAdapterWithOptions(id ProviderID, keys KeyProvider, ref KeyRef, opts AdapterOptions) (Adapter, error) {
	registryMu.RLock()
	f, ok := registry[id]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("対応していない AI プロバイダです: %q", id)
	}
	if keys == nil {
		return nil, fmt.Errorf("キーの取得先が設定されていません")
	}
	return f(keys, ref, opts), nil
}

// RegisteredProviders は登録済みプロバイダの一覧を返す（設定画面のプロバイダ一覧に使う）。
func RegisteredProviders() []ProviderID {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]ProviderID, 0, len(registry))
	for id := range registry {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
