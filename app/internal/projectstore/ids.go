package projectstore

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// IDKind はプロジェクト内連番の ID 種別。
//
// 採番はカウンタを持たず「既存 ID の最大値 + 1」で行う（実体との二重管理を避ける）。
// 欠番は再利用しない。
//
// このため ID を持つレコードの削除は論理削除（tombstone）とし、実体を消さずに
// deleted_at / deleted_by を立てる。
// 走査は id を持つ全エントリを対象にするため、論理削除されたレコードも最大値に数える。
// 物理削除すると最大値が下がって削除済み ID が再採番され、既存レコードに残る参照
// （決定事項の論点キー custom/PRS-nnn 等）が別のレコードを指してしまう。
// 削除操作を追加する際は必ず論理削除で実装すること。
type IDKind struct {
	Prefix string
	Digits int
	// RangeKey は番号帯（idranges.go）の対象種別キー。
	RangeKey string
	// scanUsed はプロジェクトフォルダ内の実体を走査して使用済みの連番を返す（順序は問わない）。
	scanUsed func(root string, kind IDKind) ([]int, error)
}

// scanMax は使用済みの最大連番を返す（0 = 未採番）。
func (k IDKind) scanMax(root string, kind IDKind) (int, error) {
	nums, err := k.scanUsed(root, kind)
	if err != nil {
		return 0, err
	}
	max := 0
	for _, n := range nums {
		if n > max {
			max = n
		}
	}
	return max, nil
}

// プロジェクト内連番の ID 種別（採番の対象の一覧）。
var (
	IDSession       = IDKind{Prefix: "S", Digits: 4, RangeKey: RangeSession, scanUsed: scanSessionUsed}
	IDQuestionnaire = IDKind{Prefix: "QS", Digits: 3, RangeKey: RangeQuestionnaire, scanUsed: scanQuestionnaireUsed}
	IDImport        = IDKind{Prefix: "IMP", Digits: 3, RangeKey: RangeImport, scanUsed: scanImportUsed}
	IDStakeholder   = IDKind{Prefix: "STK", Digits: 3, RangeKey: RangeStakeholder, scanUsed: scanStakeholderUsed}
	IDPerspective   = IDKind{Prefix: "PRS", Digits: 3, RangeKey: RangePerspective, scanUsed: scanPerspectiveUsed}
	IDDecision      = IDKind{Prefix: "DEC", Digits: 3, RangeKey: RangeDecision, scanUsed: scanDecisionUsed}
	IDOpenIssue     = IDKind{Prefix: "ISS", Digits: 3, RangeKey: RangeOpenIssue, scanUsed: scanOpenIssueUsed}
)

// 番号帯の対象種別キーの一覧。要件項目は FR / NFR の連番部をまとめて `requirement`。
const (
	RangeSession       = "session"
	RangeQuestionnaire = "questionnaire"
	RangeImport        = "import"
	RangeStakeholder   = "stakeholder"
	RangePerspective   = "perspective"
	RangeRequirement   = "requirement"
	RangeDecision      = "decision"
	RangeOpenIssue     = "open-issue"
)

// RangeKeys は番号帯の対象種別を定義順で返す（id-ranges.yaml の確保・残量表示の対象）。
func RangeKeys() []string {
	return []string{RangeSession, RangeQuestionnaire, RangeImport, RangeStakeholder,
		RangePerspective, RangeRequirement, RangeDecision, RangeOpenIssue}
}

func validRangeKey(key string) bool {
	for _, k := range RangeKeys() {
		if k == key {
			return true
		}
	}
	return false
}

// rangeKeyLabels は対象種別の日本語表示（利用者向けの文言に内部キーを出さない）。
var rangeKeyLabels = map[string]string{
	RangeSession:       "対話セッション",
	RangeQuestionnaire: "質問票",
	RangeImport:        "取り込み資料",
	RangeStakeholder:   "ステークホルダー",
	RangePerspective:   "ヒアリング観点",
	RangeRequirement:   "要件項目",
	RangeDecision:      "決定事項",
	RangeOpenIssue:     "未決事項",
}

func rangeKeyLabel(key string) string {
	if label, ok := rangeKeyLabels[key]; ok {
		return label
	}
	return "この種別"
}

// idKindForRange は対象種別キーに対応する IDKind を返す（`requirement` は IDKind を持たない）。
func idKindForRange(key string) (IDKind, bool) {
	for _, k := range []IDKind{IDSession, IDQuestionnaire, IDImport, IDStakeholder,
		IDPerspective, IDDecision, IDOpenIssue} {
		if k.RangeKey == key {
			return k, true
		}
	}
	return IDKind{}, false
}

// Format は連番から ID 文字列を作る（例: S-0003）。
func (k IDKind) Format(n int) string {
	return fmt.Sprintf("%s-%0*d", k.Prefix, k.Digits, n)
}

// Parse は ID 文字列から連番を取り出す。種別が一致しない場合は ok = false。
//
// 桁あふれ（番号帯の採番で連番が形式の桁数を超え、ゼロ埋め桁が増えた場合）を受け入れるため、
// 桁数は「規定の桁数以上」とする。規定より長い場合に先頭のゼロは認めない（`QS-0001` を
// `QS-001` と別物として扱わない＝表記ゆれを作らない）。
func (k IDKind) Parse(id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, k.Prefix+"-")
	if !ok || len(rest) < k.Digits {
		return 0, false
	}
	if len(rest) > k.Digits && rest[0] == '0' {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// AllocateID は records ロック内で次の ID を採番し、create に渡して実体を作らせる。
//
// ロック取得後に実体を再走査して最大値を確定するため、同一端末の複数プロセスの同時採番でも衝突しない
// （以前の ids ロックは廃止。端末間の衝突回避は番号帯が担う）。
// create が失敗した場合、ID は消費されない（実体が無いため次回同じ番号が採番される）。
//
// 保存キューの中から呼んではならない（キューの完了を待つため停止する）。
func (s *Store) AllocateID(kind IDKind, create func(id string) error) (string, error) {
	var id string
	err := s.WithShortLock(LockRecords, func() error {
		var err error
		id, err = s.allocateIDLocked(kind, create)
		return err
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// allocateIDLocked はロックを取らずに採番する。**呼び出し側が短時間ロックを保持していること**が前提
// （records / roster など、対象の実体を守るロック）。
//
// 短時間ロックは O_EXCL のファイルロックで**再入できない**ため、ロックを保持したまま AllocateID を
// 呼ぶと自分自身の解放を待って必ずタイムアウトする（以前に実際に発生した）。ロック内から採番する
// 経路は本メソッドを使う。
func (s *Store) allocateIDLocked(kind IDKind, create func(id string) error) (string, error) {
	n, err := s.nextNumberLocked(kind)
	if err != nil {
		return "", err
	}
	id := kind.Format(n)
	if err := create(id); err != nil {
		return "", err
	}
	return id, nil
}

// nextNumberLocked は次に使う連番を返す。番号帯を使うプロジェクトでは自分の区間の未使用最小値、
// 単独利用（`id-ranges.yaml` が無い）では既存 ID の最大値 + 1。
func (s *Store) nextNumberLocked(kind IDKind) (int, error) {
	if !s.UsesIDRanges() {
		max, err := kind.scanMax(s.root, kind)
		if err != nil {
			return 0, err
		}
		return max + 1, nil
	}
	nums, err := kind.scanUsed(s.root, kind)
	if err != nil {
		return 0, err
	}
	used := make(map[int]bool, len(nums))
	for _, n := range nums {
		used[n] = true
	}
	return s.nextInRanges(kind.RangeKey, used)
}

// NextID は採番される予定の ID を返す（実体は作らない。表示・確認用）。
func (s *Store) NextID(kind IDKind) (string, error) {
	n, err := s.nextNumberLocked(kind)
	if err != nil {
		return "", err
	}
	return kind.Format(n), nil
}

// ---- 実体の走査 ---------------------------------------------------------

func scanSessionUsed(root string, kind IDKind) ([]int, error) {
	return scanDirEntryUsed(root, "sessions", kind, false)
}

func scanQuestionnaireUsed(root string, kind IDKind) ([]int, error) {
	return scanDirEntryUsed(root, "questionnaires", kind, true)
}

func scanImportUsed(root string, kind IDKind) ([]int, error) {
	return scanDirEntryUsed(root, "imports", kind, true)
}

func scanDecisionUsed(root string, kind IDKind) ([]int, error) {
	return scanDirEntryUsed(root, dirDecisions, kind, false)
}

func scanOpenIssueUsed(root string, kind IDKind) ([]int, error) {
	return scanDirEntryUsed(root, dirOpenIssues, kind, false)
}

func scanStakeholderUsed(root string, kind IDKind) ([]int, error) {
	return scanYAMLListUsed(root, FileRoster, "stakeholders", kind)
}

func scanPerspectiveUsed(root string, kind IDKind) ([]int, error) {
	return scanYAMLListUsed(root, FilePerspectives, "perspectives", kind)
}

// idHeadRe は名前の先頭にある ID 部分（拡張子・付随文字列の前まで）を取り出す。
var idHeadRe = regexp.MustCompile(`^[A-Z]+-[0-9]+`)

// scanDirEntryUsed はディレクトリ内の項目名から使用済みの連番を集める。
// dirOnly = true のときはディレクトリのみを対象にする（1 件 = 1 フォルダの ID）。
func scanDirEntryUsed(root, sub string, kind IDKind, dirOnly bool) ([]int, error) {
	dir := filepath.Join(root, sub)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s を走査できません: %w", sub, err)
	}
	var out []int
	for _, e := range entries {
		if dirOnly && !e.IsDir() {
			continue
		}
		head := idHeadRe.FindString(e.Name())
		if head == "" {
			continue
		}
		n, ok := kind.Parse(head)
		if !ok {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}

// scanYAMLListUsed は YAML の `<topKey>: [{id: ...}]` から使用済みの連番を集める。
func scanYAMLListUsed(root, file, topKey string, kind IDKind) ([]int, error) {
	b, err := os.ReadFile(filepath.Join(root, file))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s を読み込めません: %w", file, err)
	}
	var doc map[string][]struct {
		ID string `yaml:"id"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s を解釈できません: %w", file, err)
	}
	var out []int
	for _, item := range doc[topKey] {
		n, ok := kind.Parse(item.ID)
		if !ok {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}
