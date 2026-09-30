//go:build !darwin

package projectstore

// macOS 以外では、フォルダを 1 個のファイルに見せる仕組みが OS に無い。
//
// Windows の Explorer にパッケージの概念は無く、実現する手立て（コンテナ化・隠し属性・
// シェル名前空間拡張・ProjFS）はいずれも同期モデル・保守性・管理者権限なしの要件
// と両立しない。**見え方が OS で異なることを許容する**方針のため、
// ここでは何もしない（呼び出し側で OS 判定を分岐させない）。
func markAsPackage(string) error { return nil }
