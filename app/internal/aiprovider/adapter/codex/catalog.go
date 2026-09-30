package codex

// 本ファイルは手元のモデル定義。
//
// 同梱する版の Codex に埋め込まれた既定の定義から、**全モデルの入力を「文字のみ」にした**もの。
// 生成は `go run ./tools/codexcatalog`（版を上げるたびに作り直す）。
// この定義を渡すと Codex は `view_image` を**ファイルを開く前に**拒否し、サーバからモデル一覧を
// 取りに行かない（実測）。この定義が既知一覧（API から取れないときの縮退先）も兼ねる。

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

//go:embed model_catalog.json
var modelCatalogJSON []byte

// catalogModel は手元のモデル定義の 1 モデル（照合に使う項目だけを読む）。
type catalogModel struct {
	Slug            string   `json:"slug"`
	DisplayName     string   `json:"display_name"`
	ContextWindow   int      `json:"context_window"`
	InputModalities []string `json:"input_modalities"`
	Visibility      string   `json:"visibility"`
}

var (
	catalogOnce   sync.Once
	catalogParsed []catalogModel
	catalogErr    error
)

func catalogModels() ([]catalogModel, error) {
	catalogOnce.Do(func() {
		var doc struct {
			Models []catalogModel `json:"models"`
		}
		if err := json.Unmarshal(modelCatalogJSON, &doc); err != nil {
			catalogErr = fmt.Errorf("手元のモデル定義を解釈できません: %w", err)
			return
		}
		if len(doc.Models) == 0 {
			catalogErr = fmt.Errorf("手元のモデル定義にモデルがありません")
			return
		}
		catalogParsed = doc.Models
	})
	return catalogParsed, catalogErr
}

// catalogContextWindow は当該モデルのコンテキスト長を返す（定義に無ければ 0 = 不明）。
func catalogContextWindow(id string) int {
	models, err := catalogModels()
	if err != nil {
		return 0
	}
	for _, m := range models {
		if m.Slug == id {
			return m.ContextWindow
		}
	}
	return 0
}
