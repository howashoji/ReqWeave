package exchange

// 本ファイルは質問票の発行（内容の組み立て・プレビュー・暗号化・出力）を担う。
//
// 発行用ファイルへ入れてよいものは Payload が写す内容物だけであり、それ以外は一切含めない
// （ホワイトリスト方式。誤送付したときの被害範囲を限定する）。
// プレビューは出力対象データそのものから都度生成し、別管理の説明文を持たない。

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 発行用ファイルの内容物のパス。
const (
	EntryQuestionnaire = "questionnaire.md"
	EntryTerms         = "terms.yaml"
	EntryContent       = "content.yaml"
	EntryAnswers       = "answers.md"
	EntrySessionsDir   = "sessions/"
)

// ContentMeta は payload 内 content.yaml。
type ContentMeta struct {
	// ContentHash は質問部一式の SHA-256（取り込み時の改変検出用）。
	ContentHash string `yaml:"content_hash"`
	// ReturnKey は返送暗号化用の X25519 公開鍵（base64）。発行用のみ。
	ReturnKey string `yaml:"return_key,omitempty"`
}

// ContentHash は questionnaire.md のバイト列そのままを対象に SHA-256 を求める。
func ContentHash(questionnaireMarkdown []byte) string {
	sum := sha256.Sum256(questionnaireMarkdown)
	return hex.EncodeToString(sum[:])
}

// ExcludedFromIssue は発行用ファイルに「含まれないもの」の定型表示（明示的に除外するもの）。
//
// 誤送付時の被害範囲を担当者が判断できるようにするため、発行のプレビューに出す。
var ExcludedFromIssue = []string{
	"担当者とAIの対話履歴",
	"要件項目の全文",
	"決定事項・未決事項の本文",
	"成果物ドキュメント",
	"取り込んだ既存資料の原本・抽出テキスト",
	"監査データ（AI送信記録・変更履歴）",
	"プロジェクトのメンバー一覧・ステークホルダー名簿の全体",
	"質問票返送の復号鍵（秘密鍵）",
	"アプリ設定・シークレットキー",
	"パスコードそのもの",
}

// IssueContent は発行用ファイルへ入れる内容一式（プレビューと出力の共通のデータ源）。
//
// プレビューと実際の出力を同じ値から作ることで、表示と実内容の乖離を構造的に排除する
// （見せた内容と違うものが混入するのを防ぐ）。
type IssueContent struct {
	Questionnaire projectstore.Questionnaire
	// QuestionnaireMarkdown は実際にコンテナへ入る questionnaire.md のバイト列。
	QuestionnaireMarkdown []byte
	// Terms は同梱する用語（質問の terms から直接参照 + 1 段の閉包）。
	Terms []projectstore.Term
	// TermsYAML は実際にコンテナへ入る terms.yaml のバイト列。
	TermsYAML []byte
	Content   ContentMeta
	// ContentYAML は実際にコンテナへ入る content.yaml のバイト列。
	ContentYAML []byte
}

// SourceIssues は発行元未決事項の ID を重複なく昇順で返す。
func (c *IssueContent) SourceIssues() []string {
	seen := map[string]bool{}
	var out []string
	for _, q := range c.Questionnaire.Questions {
		if seen[q.SourceIssue] {
			continue
		}
		seen[q.SourceIssue] = true
		out = append(out, q.SourceIssue)
	}
	sort.Strings(out)
	return out
}

// Payload は内容物をコンテナのペイロードへ写す（発行用ファイルに入れてよいものだけ）。
func (c *IssueContent) Payload() Payload {
	return Payload{
		EntryQuestionnaire: c.QuestionnaireMarkdown,
		EntryTerms:         c.TermsYAML,
		EntryContent:       c.ContentYAML,
	}
}

// BuildIssueContent は発行用ファイルの内容一式を組み立てる。
//
// 呼び出し側はこの値からプレビューを作り、確認操作の後に WriteIssue で出力する。
// 組み立ての時点ではファイルを一切書かない。
func BuildIssueContent(q *projectstore.Questionnaire, terms *projectstore.Terms, returnPublicKey []byte) (*IssueContent, error) {
	if q == nil {
		return nil, fmt.Errorf("質問票がありません")
	}
	if len(returnPublicKey) != KeyPairSize {
		return nil, fmt.Errorf("プロジェクトの交換鍵が不正です。プロジェクトを開き直してください")
	}
	// 受け渡し用の写し（発行者の利用者 ID と状態を含まない。社外へ渡すファイルに内部の管理情報を載せない）。
	markdown, err := q.MarshalForExchange()
	if err != nil {
		return nil, err
	}
	included := closeTerms(q, terms)
	termsYAML, err := marshalTerms(included)
	if err != nil {
		return nil, err
	}
	content := ContentMeta{
		ContentHash: ContentHash(markdown),
		ReturnKey:   base64.StdEncoding.EncodeToString(returnPublicKey),
	}
	contentYAML, err := yaml.Marshal(&content)
	if err != nil {
		return nil, fmt.Errorf("受け渡しファイルの内容情報を組み立てられません: %w", err)
	}
	return &IssueContent{
		Questionnaire:         *q,
		QuestionnaireMarkdown: markdown,
		Terms:                 included,
		TermsYAML:             termsYAML,
		Content:               content,
		ContentYAML:           contentYAML,
	}, nil
}

// closeTerms は同梱する用語を選ぶ（直接参照 + 1 段。全用語の同梱はしない）。
func closeTerms(q *projectstore.Questionnaire, terms *projectstore.Terms) []projectstore.Term {
	if terms == nil {
		return nil
	}
	selected := map[string]projectstore.Term{}
	var direct []projectstore.Term
	for _, question := range q.Questions {
		for _, name := range question.Terms {
			term, ok := terms.Find(name)
			if !ok {
				continue
			}
			if _, dup := selected[term.Name]; dup {
				continue
			}
			selected[term.Name] = term
			direct = append(direct, term)
		}
	}
	// 1 段: 直接参照した用語の定義に現れる用語まで（さらにその先は辿らない）。
	for _, term := range direct {
		for _, other := range terms.Terms {
			if _, dup := selected[other.Name]; dup {
				continue
			}
			if strings.Contains(term.Definition, other.Name) {
				selected[other.Name] = other
			}
		}
	}
	out := make([]projectstore.Term, 0, len(selected))
	for _, name := range sortedTermNames(selected) {
		out = append(out, selected[name])
	}
	return out
}

func sortedTermNames(m map[string]projectstore.Term) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func marshalTerms(terms []projectstore.Term) ([]byte, error) {
	doc := projectstore.Terms{Terms: terms}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("同梱する用語を組み立てられません: %w", err)
	}
	return out, nil
}

// IssueResult は発行の結果（出力完了時に提示する内容）。
type IssueResult struct {
	// Path は出力した発行用ファイルのパス。
	Path string
	// Passcode は担当者へ 1 回だけ提示するパスコード（保存しない・再表示しない）。
	Passcode string
	// ArchiveName は発行控えのファイル名（exchange/ 配下）。
	ArchiveName string
	IssuedAt    time.Time
}

// WriteIssue は発行用ファイルを出力し、発行控えを exchange/ へ保存する。
//
// パスコードは質問票ごとに新規生成し、戻り値で 1 回だけ返す。保存はしない。
// 再発行のときも同じ経路を通り、新しいパスコード・新しい salt になる。
func WriteIssue(store *projectstore.Store, c *IssueContent, dst string) (*IssueResult, error) {
	if c == nil {
		return nil, fmt.Errorf("発行する内容がありません")
	}
	if !strings.HasSuffix(dst, ExtIssue) {
		dst += ExtIssue
	}
	passcode, err := NewPasscode()
	if err != nil {
		return nil, err
	}
	manifest := Manifest{
		ExchangeFormatVersion: CurrentFormatVersion,
		Kind:                  KindIssue,
		ProjectID:             store.Project().ProjectID,
		QuestionnaireID:       c.Questionnaire.ID,
		IssuedAt:              c.Questionnaire.IssuedAt,
	}
	container, err := BuildIssueContainer(manifest, c.Payload(), passcode)
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(dst, container); err != nil {
		return nil, err
	}

	// 発行控え（出力したものと同一のバイト列。パスコードは含まない）。
	archive := c.Questionnaire.ID + ExtIssue
	if err := store.SaveExchangeArtifact(c.Questionnaire.ID, archive, container); err != nil {
		return nil, err
	}
	// 突合基準（改変検出と宛先対応）。控え本体は復号できないため平文で併置する。
	record := IssueRecord{
		ContentHash:  c.Content.ContentHash,
		AddresseeRef: c.Questionnaire.AddresseeRef,
		Addressee:    c.Questionnaire.Addressee,
		IssuedAt:     c.Questionnaire.IssuedAt,
	}
	recordYAML, err := yaml.Marshal(&record)
	if err != nil {
		return nil, fmt.Errorf("発行控えを組み立てられません: %w", err)
	}
	if err := store.SaveExchangeArtifact(c.Questionnaire.ID, IssueRecordName(c.Questionnaire.ID), recordYAML); err != nil {
		return nil, err
	}
	return &IssueResult{
		Path:        dst,
		Passcode:    passcode,
		ArchiveName: archive,
		IssuedAt:    manifest.IssuedAt,
	}, nil
}

// IssueRecord は発行控えのメタデータ（取込時の突合基準 = 改変検出と宛先対応）。
//
// 発行控え本体（.rwvq）は発行時のパスコードで暗号化されており、パスコードを保持しない
// 担当者側では復号できない。そのため突合に必要な値だけを平文で控えの隣に置く。
// パスコード・その導出値は含めない。
type IssueRecord struct {
	ContentHash  string    `yaml:"content_hash"`
	AddresseeRef string    `yaml:"addressee_ref"`
	Addressee    string    `yaml:"addressee"`
	IssuedAt     time.Time `yaml:"issued_at"`
}

// IssueRecordName は発行控えメタデータの保存名（exchange/ 配下）。
func IssueRecordName(questionnaireID string) string { return questionnaireID + ".issue.yaml" }

// LoadIssueRecord は発行控えメタデータを読む。未保存のときは (nil, nil)。
func LoadIssueRecord(store *projectstore.Store, questionnaireID string) (*IssueRecord, error) {
	data, err := os.ReadFile(store.ExchangeArtifactPath(questionnaireID, IssueRecordName(questionnaireID)))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("発行控えを読み込めません（%s）: %w", questionnaireID, err)
	}
	var rec IssueRecord
	if err := yaml.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("発行控えを解釈できません（%s）: %w", questionnaireID, err)
	}
	return &rec, nil
}

// IssuedContentHash は突合の基準となる質問部のハッシュを返す（改変検出）。
//
// 発行控えメタデータがあればその値を使う。無い場合（メタデータ導入前に発行した質問票）は
// 現在の questionnaire.md から計算する。
func IssuedContentHash(store *projectstore.Store, q *projectstore.Questionnaire) (string, error) {
	rec, err := LoadIssueRecord(store, q.ID)
	if err != nil {
		return "", err
	}
	if rec != nil && rec.ContentHash != "" {
		return rec.ContentHash, nil
	}
	markdown, err := q.MarshalForExchange()
	if err != nil {
		return "", err
	}
	return ContentHash(markdown), nil
}
