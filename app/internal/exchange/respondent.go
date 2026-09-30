package exchange

// 本ファイルは回答モード（受け取った質問票への回答の入力・中断再開・確定）を担う。
//
// 発行用ファイルは**読むだけで一切変更しない**（共有フォルダ上に置かれうるため）。
// 回答はアプリ設定領域配下の回答作業領域上で編集し、発行ファイルと同じパスコード導出鍵で
// 暗号化して保存する（平文で保持しない）。パスコード・導出鍵は保存しない。

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// dirRespondent は回答作業領域の配置（アプリ設定領域配下）。
const dirRespondent = "respondent"

// 回答作業領域のファイル名。業務情報（質問文・背景説明・宛先・回答本文）は
// すべて暗号化した .enc 側にのみ置く。
const (
	fileRespondMeta    = "meta.yaml"   // 平文（質問票 ID・状態・日時のみ）
	fileRespondAnswers = "answers.enc" // 暗号化した answers.md
	fileRespondReturn  = "return.enc"  // 暗号化した返送ファイル（確定時点のバイト列）
)

// 回答作業領域の状態（担当者側の質問票の状態とは別の、回答者側の進行状態）。
const (
	RespondAnswering = "answering" // 回答中
	RespondAnswered  = "answered"  // 確定済み（返送ファイル出力済み。再出力できる）
)

const respondDirMode = 0o700
const respondFileMode = 0o600

// ErrIncomplete は未回答の質問が残っていることを表す（回答確定の条件）。
var ErrIncomplete = fmt.Errorf("未回答の質問があります。すべての質問に回答（「不明」を含む）してから確定してください")

// RespondMeta は回答作業領域の平文メタデータ。
//
// 業務情報を持たない（質問文・背景説明・宛先・回答本文は暗号化側にのみ置く）。
type RespondMeta struct {
	QuestionnaireID string `yaml:"questionnaire_id"`
	// KeyFingerprint は鍵（= パスコードと salt）の識別子。再発行で salt が変わると別の回答作業領域になる
	// （再発行すると salt が変わるため、旧回答作業領域は新ファイルの鍵で復号できない）。
	KeyFingerprint string    `yaml:"key_fingerprint"`
	Status         string    `yaml:"status"`
	OpenedAt       time.Time `yaml:"opened_at"`
	UpdatedAt      time.Time `yaml:"updated_at"`
	FinalizedAt    time.Time `yaml:"finalized_at,omitempty"`
}

// Workspace は回答作業領域の置き場（アプリ設定領域配下）。
type Workspace struct {
	base string
}

// NewWorkspace はアプリ設定領域配下の回答作業領域の置き場を返す。
func NewWorkspace(paths projectstore.AppPaths) *Workspace {
	return &Workspace{base: filepath.Join(paths.Base, dirRespondent)}
}

// RespondSession は開いた質問票 1 件ぶんの回答作業。
//
// 導出鍵はメモリ内にのみ保持し、保存・出力・ログのいずれにも出さない。
type RespondSession struct {
	// Questionnaire は復号して解釈した質問票（発行ファイル内の写し）。
	Questionnaire *projectstore.Questionnaire
	// Terms は同梱された用語（質問の解釈に必要な範囲）。
	Terms []projectstore.Term
	// Answers は入力済みの回答（再開時は回答作業領域から復元したもの）。
	Answers *projectstore.Answers
	// Status は回答作業領域の状態（answering / answered）。
	Status string
	// Restored は回答作業領域から入力済み回答を復元したか（中断再開）。
	Restored bool
	// Notice は画面へ出す案内文（原因＋次の行動）。無ければ空。
	Notice string
	// Utterances は AI 対話回答の発話列。確定前は sessions.enc へ暗号化して置き、
	// 確定時に返送の sessions/ へ載せる。対話をしなければ空のまま。
	Utterances []projectstore.Utterance

	manifest Manifest
	// questionnaireMarkdown は発行ファイル内の原本（返送へ無変更で載せる）。
	questionnaireMarkdown []byte
	// contentYAML は発行時の content.yaml（返送へ引き継ぐ）。
	contentYAML []byte
	returnKey   []byte
	dir         string
	key         []byte
}

// Open は発行用ファイルを開いて回答作業を開始・再開する。
//
// パスコードが一致しない場合は ErrPasscodeMismatch を返し、質問文・背景説明・宛先を
// 一切復号・返却しない。発行用ファイルは変更しない。
// ErrNotIssueFile は発行用（質問票）ではないファイルを回答モードで開こうとしたこと。
//
// 呼び出し側が**種類を見分けられる形**にしておく（動作ログの失敗種別に使う）。
// 文言は利用者向けのまま（原因＋次の行動）。
var ErrNotIssueFile = errors.New(
	"このファイルは質問票（発行用）ではありません。担当者から届いた質問票ファイルを開いてください")

func (w *Workspace) Open(src, passcode string) (*RespondSession, error) {
	container, err := ReadContainer(src)
	if err != nil {
		return nil, err
	}
	if container.Manifest.Kind != KindIssue {
		return nil, ErrNotIssueFile
	}
	// 復号の成否がパスコード一致の判定を兼ねる。ここより先が「一致後」。
	payload, err := container.OpenIssuePayload(passcode)
	if err != nil {
		return nil, err
	}
	key, err := deriveKey(passcode, *container.Manifest.KDF)
	if err != nil {
		return nil, err
	}

	sess := &RespondSession{
		Status:   RespondAnswering,
		manifest: container.Manifest,
		key:      key,
		dir:      w.respondAreaDir(container.Manifest.QuestionnaireID, *container.Manifest.KDF),
	}
	if err := sess.loadIssuedContent(payload); err != nil {
		return nil, err
	}
	if err := sess.restore(w); err != nil {
		return nil, err
	}
	return sess, nil
}

// respondAreaDir は質問票 ID と鍵の識別子で回答作業領域の場所を決める。
//
// 再発行（新しい salt）は別の回答作業領域になるため、旧ファイルの回答途中データを壊さない
// （引き継がれない旨の案内は Notice で出す）。
func (w *Workspace) respondAreaDir(questionnaireID string, kdf KDFParams) string {
	return filepath.Join(w.base, questionnaireID, keyFingerprint(kdf))
}

// keyFingerprint は salt から回答作業領域の識別子を作る（パスコード・鍵そのものは含めない）。
func keyFingerprint(kdf KDFParams) string {
	sum := sha256.Sum256([]byte(kdf.Salt))
	return hex.EncodeToString(sum[:])[:16]
}

// loadIssuedContent は復号したペイロードから質問票・用語・返送先鍵を取り出す。
func (s *RespondSession) loadIssuedContent(payload Payload) error {
	markdown, ok := payload[EntryQuestionnaire]
	if !ok {
		return fmt.Errorf("質問票ファイルに質問が入っていません（ファイルが壊れています）")
	}
	q, err := projectstore.UnmarshalExchangeQuestionnaire(markdown)
	if err != nil {
		return err
	}
	if q.ID != s.manifest.QuestionnaireID {
		return fmt.Errorf("質問票ファイルの中身が一致しません（%s / %s）", s.manifest.QuestionnaireID, q.ID)
	}
	s.Questionnaire = q
	s.questionnaireMarkdown = markdown

	if body, ok := payload[EntryTerms]; ok {
		var terms projectstore.Terms
		if err := yaml.Unmarshal(body, &terms); err != nil {
			return fmt.Errorf("質問票ファイルの用語を解釈できません: %w", err)
		}
		s.Terms = terms.Terms
	}

	body, ok := payload[EntryContent]
	if !ok {
		return fmt.Errorf("質問票ファイルに内容情報がありません（ファイルが壊れています）")
	}
	var content ContentMeta
	if err := yaml.Unmarshal(body, &content); err != nil {
		return fmt.Errorf("質問票ファイルの内容情報を解釈できません: %w", err)
	}
	s.contentYAML = body
	s.returnKey, err = decodeReturnKey(content.ReturnKey)
	if err != nil {
		return err
	}
	return nil
}

// restore は回答作業領域から入力済み回答を復元する（中断再開）。
func (s *RespondSession) restore(w *Workspace) error {
	meta, err := s.readMeta()
	if err != nil {
		return err
	}
	if meta == nil {
		s.Answers = s.newAnswers()
		s.Notice = w.previousIssueNotice(s.manifest.QuestionnaireID, s.dir)
		// 回答が 1 件も無くても対話だけ残っていることがある（AI 対話回答）。
		return s.restoreUtterances()
	}
	sealed, err := os.ReadFile(filepath.Join(s.dir, fileRespondAnswers))
	if os.IsNotExist(err) {
		// 回答を 1 件も入力せずに AI 対話だけ行って閉じた場合。
		// 作業領域は在るが回答ファイルはまだ無い。対話ログだけ復元する。
		s.Answers = s.newAnswers()
		s.Status = meta.Status
		return s.restoreUtterances()
	}
	if err != nil {
		return fmt.Errorf("入力済みの回答を読み込めません: %w", err)
	}
	plain, err := openWithKey(sealed, s.key)
	if err != nil {
		return fmt.Errorf("入力済みの回答を読み取れません（作業データが壊れています）。担当者へ再発行を依頼してください")
	}
	answers, err := projectstore.UnmarshalAnswers(plain)
	if err != nil {
		return err
	}
	s.Answers = answers
	s.Status = meta.Status
	s.Restored = len(answers.Answers) > 0
	return s.restoreUtterances()
}

// previousIssueNotice は同じ質問票の別バージョン（再発行前）の回答作業領域がある場合の案内を返す。
func (w *Workspace) previousIssueNotice(questionnaireID, currentDir string) string {
	entries, err := os.ReadDir(filepath.Join(w.base, questionnaireID))
	if err != nil {
		return ""
	}
	current := filepath.Base(currentDir)
	for _, e := range entries {
		if e.IsDir() && e.Name() != current {
			return "この質問票は再発行されています。前のファイルで入力していた回答は引き継がれません。" +
				"お手数ですが、この画面で回答を入力してください。"
		}
	}
	return ""
}

// newAnswers は空の回答を作る（respondent は発行時の宛先氏名を初期値とする）。
func (s *RespondSession) newAnswers() *projectstore.Answers {
	return &projectstore.Answers{
		QuestionnaireID: s.Questionnaire.ID,
		Respondent:      s.Questionnaire.Addressee,
		AnsweredAt:      time.Now().UTC().Truncate(time.Second),
	}
}

// readMeta は回答作業領域のメタデータを読む（無ければ nil を返す）。
func (s *RespondSession) readMeta() (*RespondMeta, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, fileRespondMeta))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("作業データを読み込めません: %w", err)
	}
	var meta RespondMeta
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("作業データを解釈できません: %w", err)
	}
	if !isRespondStatus(meta.Status) {
		return nil, fmt.Errorf("作業データの状態が不正です: %q", meta.Status)
	}
	return &meta, nil
}

func isRespondStatus(v string) bool { return v == RespondAnswering || v == RespondAnswered }

// SetAnswer は 1 問の回答を記録し、回答作業領域へ自動保存する（一時保存）。
//
// 入力・変更のたびに呼ぶ前提（原子的書き込み）。確定後の変更は受け付けない
// （再出力の内容が確定時点のままであることを保つため）。
func (s *RespondSession) SetAnswer(a projectstore.Answer) error {
	if s.Status == RespondAnswered {
		return fmt.Errorf("回答は確定済みです。担当者へ連絡してください（担当者は質問票を再発行できます）")
	}
	if !s.hasQuestion(a.QuestionID) {
		return fmt.Errorf("この質問票にない質問です: %q", a.QuestionID)
	}
	if a.At.IsZero() {
		a.At = time.Now().UTC().Truncate(time.Second)
	}
	if err := a.Validate(s.Answers.QuestionnaireID); err != nil {
		return err
	}
	replaced := false
	for i := range s.Answers.Answers {
		if s.Answers.Answers[i].QuestionID == a.QuestionID {
			s.Answers.Answers[i] = a
			replaced = true
			break
		}
	}
	if !replaced {
		s.Answers.Answers = append(s.Answers.Answers, a)
	}
	s.sortAnswers()
	return s.save()
}

// hasQuestion は質問票に当該の質問があるかを返す。
func (s *RespondSession) hasQuestion(questionID string) bool {
	for _, q := range s.Questionnaire.Questions {
		if q.ID == questionID {
			return true
		}
	}
	return false
}

// sortAnswers は回答を質問順に並べる（出力の決定性）。
func (s *RespondSession) sortAnswers() {
	order := map[string]int{}
	for i, q := range s.Questionnaire.Questions {
		order[q.ID] = i
	}
	sort.SliceStable(s.Answers.Answers, func(i, j int) bool {
		return order[s.Answers.Answers[i].QuestionID] < order[s.Answers.Answers[j].QuestionID]
	})
}

// Unanswered は未回答の質問 ID を質問順で返す（確定できるかの判定・画面表示に使う）。
func (s *RespondSession) Unanswered() []string {
	var out []string
	for _, q := range s.Questionnaire.Questions {
		if _, ok := s.Answers.Find(q.ID); !ok {
			out = append(out, q.ID)
		}
	}
	return out
}

// save は回答作業領域を保存する（暗号化 + 原子的書き込み）。
func (s *RespondSession) save() error {
	plain, err := s.Answers.Marshal()
	if err != nil {
		return err
	}
	sealed, err := sealWithKey(plain, s.key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, respondDirMode); err != nil {
		return fmt.Errorf("作業データの保存先を作れません: %w", err)
	}
	if err := writeFileAtomicMode(filepath.Join(s.dir, fileRespondAnswers), sealed, respondFileMode); err != nil {
		return err
	}
	return s.saveMeta()
}

// saveMeta は平文メタデータを書く（業務情報を含めない）。
func (s *RespondSession) saveMeta() error {
	now := time.Now().UTC().Truncate(time.Second)
	meta, err := s.readMeta()
	if err != nil {
		return err
	}
	if meta == nil {
		meta = &RespondMeta{
			QuestionnaireID: s.Questionnaire.ID,
			KeyFingerprint:  filepath.Base(s.dir),
			OpenedAt:        now,
		}
	}
	meta.Status = s.Status
	meta.UpdatedAt = now
	if s.Status == RespondAnswered && meta.FinalizedAt.IsZero() {
		meta.FinalizedAt = now
	}
	data, err := yaml.Marshal(meta)
	if err != nil {
		return fmt.Errorf("作業データを組み立てられません: %w", err)
	}
	return writeFileAtomicMode(filepath.Join(s.dir, fileRespondMeta), data, respondFileMode)
}

// Finalize は回答を確定して返送用ファイルを出力する（回答確定）。
//
// 全質問への回答（「不明」を含む）が揃っていない場合は ErrIncomplete を返し、何も出力しない。
// 出力したバイト列は回答作業領域へ暗号化して残し、再出力（Export）で同じ内容を書き出せるようにする。
func (s *RespondSession) Finalize(dst string) error {
	if s.Status == RespondAnswered {
		return fmt.Errorf("回答は確定済みです。再送付する場合は再出力してください")
	}
	if len(s.Unanswered()) > 0 {
		return ErrIncomplete
	}
	s.Answers.AnsweredAt = time.Now().UTC().Truncate(time.Second)
	answersMD, err := s.Answers.Marshal()
	if err != nil {
		return err
	}
	manifest := Manifest{
		ExchangeFormatVersion: CurrentFormatVersion,
		Kind:                  KindReturn,
		ProjectID:             s.manifest.ProjectID,
		QuestionnaireID:       s.manifest.QuestionnaireID,
		IssuedAt:              s.manifest.IssuedAt,
	}
	payload := Payload{
		// 質問部と内容情報は発行ファイルの原本を無変更で載せる（突合・改変検出の基準）。
		EntryQuestionnaire: s.questionnaireMarkdown,
		EntryContent:       s.contentYAML,
		EntryAnswers:       answersMD,
	}
	// AI 対話回答を使った場合のみ、対話ログをステークホルダーセッションとして載せる
	// （回答の正は常に answers.md であり、対話ログは経緯の記録）。
	if len(s.Utterances) > 0 {
		payload[EntrySessionsDir+respondSessionID+".md"] = s.marshalUtterances()
	}
	container, err := BuildReturnContainer(manifest, payload, s.returnKey)
	if err != nil {
		return err
	}
	// 出力より先に確定内容を回答作業領域へ残す（出力先の失敗で確定内容を失わない）。
	sealed, err := sealWithKey(container, s.key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, respondDirMode); err != nil {
		return fmt.Errorf("作業データの保存先を作れません: %w", err)
	}
	if err := writeFileAtomicMode(filepath.Join(s.dir, fileRespondReturn), sealed, respondFileMode); err != nil {
		return err
	}
	if err := s.save(); err != nil {
		return err
	}
	if err := writeFileAtomic(dst, container); err != nil {
		return err
	}
	s.Status = RespondAnswered
	return s.saveMeta()
}

// Export は確定済みの返送用ファイルを再出力する（送付失敗時の再送付用）。
//
// 内容は確定時点のまま（回答作業領域に残した確定時のバイト列をそのまま書き出す）。
func (s *RespondSession) Export(dst string) error {
	if s.Status != RespondAnswered {
		return fmt.Errorf("まだ回答が確定していません。すべての質問に回答して確定してください")
	}
	sealed, err := os.ReadFile(filepath.Join(s.dir, fileRespondReturn))
	if err != nil {
		return fmt.Errorf("確定済みの返送ファイルを読み込めません: %w", err)
	}
	container, err := openWithKey(sealed, s.key)
	if err != nil {
		return fmt.Errorf("確定済みの返送ファイルを読み取れません（作業データが壊れています）。担当者へ再発行を依頼してください")
	}
	return writeFileAtomic(dst, container)
}

// SuggestedReturnName は返送用ファイルの既定のファイル名。
func (s *RespondSession) SuggestedReturnName() string {
	return s.manifest.QuestionnaireID + "-return" + ExtReturn
}

// decodeReturnKey は content.yaml の返送先公開鍵を取り出す。
func decodeReturnKey(encoded string) ([]byte, error) {
	if strings.TrimSpace(encoded) == "" {
		return nil, fmt.Errorf("質問票ファイルに返送先の鍵がありません。担当者へ再発行を依頼してください")
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != KeyPairSize {
		return nil, fmt.Errorf("質問票ファイルの返送先の鍵が不正です。担当者へ再発行を依頼してください")
	}
	return key, nil
}
