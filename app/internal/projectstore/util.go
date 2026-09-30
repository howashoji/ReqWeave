package projectstore

import "sort"

// sortedKeys は map のキーを昇順で返す（書き出し内容を再現可能にするため）。
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
