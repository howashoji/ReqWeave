package dialogue

// 本ファイルはプロジェクト観点（利用者がプロジェクトごとに登録する質問観点）の質問生成への組み込みを担う。
//
// 観点そのものの保存形式・追加/編集/削除は projectstore.Perspective が持ち、
// ここでは「登録済み観点を論点として連結する」読み出し側だけを扱う。
//
// - 論点キーは custom/<PRS-nnn>。既決判定の対象とする。
// - 充足率の分母には加えない。未登録・未消化でも確定をブロックしない
//   （プリセット観点と同じく、必須経路にしない）。
// - システムプロンプトへはプリセット観点に続けて注入する。
// - 論理削除された観点は連結・注入・既決判定のいずれの対象にもしない（削除した観点から質問が出続けないように）。
//
// プロジェクト観点は関連章観点を持たない（ドメインプリセットとの違い）。
// このため章観点の走査では、全章観点を見終えた後に連結する
// （章を推測して割り当てると、根拠のない章へ質問が偏る）。

import (
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// ProjectPerspectiveDomainName はプロンプト・選定理由に出す表示名（領域名の位置に入る）。
const ProjectPerspectiveDomainName = "プロジェクト観点"

// ProjectPerspectives は登録済みのプロジェクト観点を論点キー付きで返す。
//
// 論理削除された観点は含まない（projectstore.ActivePerspectives）。
// 呼び出しごとに読み直すため、追加・編集・削除は以後の質問生成から効く。
func ProjectPerspectives(store *projectstore.Store) ([]SelectedPerspective, error) {
	registered, err := store.ActivePerspectives()
	if err != nil {
		return nil, err
	}
	out := make([]SelectedPerspective, 0, len(registered))
	for _, p := range registered {
		out = append(out, SelectedPerspective{
			DomainID:   projectstore.PerspectiveTopicPrefix,
			DomainName: ProjectPerspectiveDomainName,
			// Chapter は空（関連章観点を持たない = 章観点の走査後に連結する）。
			Perspective: Perspective{ID: p.ID, Name: p.Name, Topics: p.Summary},
			TopicKey:    p.TopicKey(),
		})
	}
	return out, nil
}

// IsProjectTopicKey は論点キーがプロジェクト観点のもの（custom/PRS-nnn）かを返す。
func IsProjectTopicKey(topicKey string) bool {
	return strings.HasPrefix(topicKey, projectstore.PerspectiveTopicPrefix+"/")
}
