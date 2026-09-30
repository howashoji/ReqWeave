package projectstore

// 本ファイルは対話セッション（sessions/S-nnnn.md と併置メタデータ）を担う。
//
// 発話は追記のみで、書き換え・削除の API を持たない（対話の記録を後から改変できないようにする）。
// 対話エンジンの再開に必要な可変メタデータは併置ファイル S-nnnn.meta.yaml へ分離する。

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// 対話セッションの種別（フロントマターの type）。
const (
	SessionOwner       = "owner"       // 担当者セッション
	SessionStakeholder = "stakeholder" // ステークホルダーセッション
)

// 発話の話者（08: 話者）。
const (
	SpeakerAgent = "agent"
	SpeakerUser  = "user"
)

// 発話の状態（発話ブロックの status）。
const (
	UtteranceCompleted = "completed"
	// UtteranceInterrupted は中断発話。抽出対象から除外する（途中で切れた AI の応答から要件を拾わない）。
	UtteranceInterrupted = "interrupted"
)

// dirSessions はセッションの配置。
const dirSessions = "sessions"

// utteranceDigits は発話 ID の桁数（utt-nnnnn）。
const utteranceDigits = 5

// Session は対話セッションのフロントマター。
type Session struct {
	ID        string    `yaml:"id"`
	Type      string    `yaml:"type"`
	Phase     string    `yaml:"phase"`
	StartedAt time.Time `yaml:"started_at"`
	// Author は担当者セッションで必須（作成した作業者の利用者 ID。セッションは作成した作業者が所有する）。
	// ステークホルダーセッションは respondent 表示のため持たない。
	Author string `yaml:"author,omitempty"`
}

// Utterance は 1 発話（発話ブロック）。
type Utterance struct {
	ID      string
	Speaker string
	At      time.Time
	Status  string
	Body    string
}

// IsInterrupted は中断発話かを返す（抽出・履歴から除外する対象）。
func (u Utterance) IsInterrupted() bool { return u.Status == UtteranceInterrupted }

// SessionMeta は S-nnnn.meta.yaml。対話の再開に必要な可変メタデータ。
//
// PendingCandidates / PresentedQuestion の**スキーマの正本は対話エンジン側**。
// 本層は構造を解釈せずそのまま保持する（二重定義を作らない）。
type SessionMeta struct {
	SummaryBlocks     []SummaryBlock `yaml:"summary_blocks,omitempty"`
	DialogueState     string         `yaml:"dialogue_state"`
	PendingCandidates any            `yaml:"pending_candidates,omitempty"`
	PresentedQuestion any            `yaml:"presented_question,omitempty"`
	// Baselines は未承認候補が対象とする共有レコードの基準版。
	// 候補生成時に採取し、承認・反映の直前の競合検知に使う（候補と同じ寿命で、承認後は消える）。
	Baselines map[string]RecordBaseline `yaml:"baselines,omitempty"`
}

// SummaryBlock は対話履歴の要約ブロック（再開時に再計算しない）。
type SummaryBlock struct {
	// CoversUntil はこのブロックが要約している最後の発話 ID（S-nnnn 内の utt-nnnnn）。
	CoversUntil string `yaml:"covers_until"`
	Body        string `yaml:"body"`
	// RecordIDs は要約が言及する決定・未決・要件項目の ID（要約しても ID で辿れるように保持する）。
	RecordIDs []string `yaml:"record_ids,omitempty"`
}

// Validate は必須項目・値集合を検証する。
func (s *Session) Validate() error {
	if _, ok := IDSession.Parse(s.ID); !ok {
		return fmt.Errorf("対話セッションの ID が不正です: %q", s.ID)
	}
	switch s.Type {
	case SessionOwner:
		if s.Author == "" {
			return fmt.Errorf("担当者セッションには作業者のメールアドレス（利用者 ID）が必要です（%s）", s.ID)
		}
	case SessionStakeholder:
	default:
		return fmt.Errorf("対話セッションの種別が不正です: %q", s.Type)
	}
	if s.Phase == "" {
		return fmt.Errorf("対話セッションのフェーズがありません（%s）", s.ID)
	}
	if s.StartedAt.IsZero() {
		return fmt.Errorf("対話セッションの開始日時がありません（%s）", s.ID)
	}
	return nil
}

// Validate は発話の必須・値集合を検証する。
func (u *Utterance) Validate() error {
	switch u.Speaker {
	case SpeakerAgent, SpeakerUser:
	default:
		return fmt.Errorf("発話の話者が不正です: %q", u.Speaker)
	}
	switch u.Status {
	case UtteranceCompleted, UtteranceInterrupted:
	default:
		return fmt.Errorf("発話の状態が不正です: %q", u.Status)
	}
	if u.At.IsZero() {
		return fmt.Errorf("発話の日時がありません")
	}
	return nil
}

// ParseSession は S-nnnn.md 1 ファイル分を解釈する。
//
// MarshalSession と対で、プロジェクトストアの外の書き手が読み書きするために公開する
// （回答モードの対話ログ）。解釈の実体は内部の parseSession 1 つだけ。
func ParseSession(data []byte) (*Session, []Utterance, error) { return parseSession(data) }

// MarshalSession は S-nnnn.md 1 ファイル分を組み立てる。
//
// プロジェクトストアの外で 1 ファイルを作る書き手のために公開する
// （回答モードの返送に載せるステークホルダーセッション）。
// **形式の実体は marshalHeader / marshalBlock の 1 組だけ**であり、ここはその並べ替えに過ぎない
// （書式を二重に持たない。取り込み側 ImportStakeholderSession も同じ 2 つを使う）。
func MarshalSession(s Session, utterances []Utterance) []byte {
	body := s.marshalHeader()
	for _, u := range utterances {
		body = append(body, u.marshalBlock()...)
	}
	return body
}

// NextUtteranceID は n 件目（0 起点）の発話 ID を返す（utt-nnnnn）。
//
// プロジェクトストアの外で発話を並べる書き手のために公開する（採番規則を写させない）。
func NextUtteranceID(index int) string {
	return fmt.Sprintf("utt-%0*d", utteranceDigits, index+1)
}

// SessionFile はセッションファイルの相対パスを返す。
func SessionFile(id string) string { return dirSessions + "/" + id + ".md" }

// SessionMetaFile は併置メタデータの相対パスを返す。
func SessionMetaFile(id string) string { return dirSessions + "/" + id + ".meta.yaml" }

// UtteranceRef は発話へのプロジェクト内参照（`S-nnnn#utt-nnnnn`）を返す。
func UtteranceRef(sessionID, utteranceID string) string { return sessionID + "#" + utteranceID }

// CreateSession は新しい対話セッションを作る（ID 採番つき）。
//
// 担当者セッションの作成者は Store の作業者になる（セッションは作成した作業者が所有する）。
func (s *Store) CreateSession(sessionType, phase string) (*Session, error) {
	sess := &Session{Type: sessionType, Phase: phase, StartedAt: time.Now()}
	if sessionType == SessionOwner {
		sess.Author = s.author.AuthorID
	}
	id, err := s.AllocateID(IDSession, func(id string) error {
		sess.ID = id
		if err := sess.Validate(); err != nil {
			return err
		}
		return s.WriteFile(SessionFile(id), sess.marshalHeader())
	})
	if err != nil {
		return nil, err
	}
	sess.ID = id
	return sess, nil
}

// marshalHeader はフロントマターのみのファイル内容を返す（本文は発話の追記で伸びる）。
func (s *Session) marshalHeader() []byte {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", s.ID)
	fmt.Fprintf(&b, "type: %s\n", s.Type)
	fmt.Fprintf(&b, "phase: %s\n", s.Phase)
	fmt.Fprintf(&b, "started_at: %s\n", s.StartedAt.Format(time.RFC3339))
	if s.Author != "" {
		fmt.Fprintf(&b, "author: %s\n", s.Author)
	}
	b.WriteString("---\n")
	return []byte(b.String())
}

// AppendUtterance は発話を 1 件追記し、採番した発話 ID を返す（発話単位の自動保存）。
//
// 追記専用（O_APPEND）で行い、既存の発話を書き換えない（対話の記録を改変しない）。
// 採番（末尾の最大値 +1）と追記は保存キュー内で行うため、同一プロジェクトへの並行追記でも重複しない。
func (s *Store) AppendUtterance(sessionID string, u Utterance) (string, error) {
	if _, ok := IDSession.Parse(sessionID); !ok {
		return "", fmt.Errorf("対話セッションの ID が不正です: %q", sessionID)
	}
	if u.At.IsZero() {
		u.At = time.Now()
	}
	if u.Status == "" {
		u.Status = UtteranceCompleted
	}
	if err := u.Validate(); err != nil {
		return "", err
	}

	path := filepath.Join(s.root, filepath.FromSlash(SessionFile(sessionID)))
	var assigned string
	err := s.write(func() error {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("対話セッションが見つかりません（%s）: %w", sessionID, err)
		}
		max, err := maxUtteranceNumber(path)
		if err != nil {
			return err
		}
		u.ID = fmt.Sprintf("utt-%0*d", utteranceDigits, max+1)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, dataFileMode)
		if err != nil {
			return fmt.Errorf("発話を追記できません（%s）: %w", sessionID, err)
		}
		defer f.Close()
		if _, err := f.Write(u.marshalBlock()); err != nil {
			return fmt.Errorf("発話の追記に失敗（%s）: %w", sessionID, err)
		}
		// 追記のたびに同期する（強制終了しても直前の発話まで残す）。
		if err := f.Sync(); err != nil {
			return fmt.Errorf("発話の保存に失敗（%s）: %w", sessionID, err)
		}
		assigned = u.ID
		return nil
	})
	if err != nil {
		return "", err
	}
	return assigned, nil
}

// marshalBlock は発話ブロックを返す。
func (u Utterance) marshalBlock() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "\n### %s\n", u.ID)
	fmt.Fprintf(&b, "- speaker: %s\n", u.Speaker)
	fmt.Fprintf(&b, "- at: %s\n", u.At.Format(time.RFC3339))
	fmt.Fprintf(&b, "- status: %s\n\n", u.Status)
	b.WriteString(strings.TrimRight(u.Body, "\n"))
	b.WriteString("\n")
	return []byte(b.String())
}

// maxUtteranceNumber はファイル末尾までを走査して既存の最大発話番号を返す（0 = 発話なし）。
func maxUtteranceNumber(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("対話セッションを読み込めません: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxUtteranceLineBytes)
	max := 0
	for sc.Scan() {
		n, ok := parseUtteranceHeading(sc.Text())
		if ok && n > max {
			max = n
		}
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("対話セッションの読み出しに失敗: %w", err)
	}
	return max, nil
}

// maxUtteranceLineBytes は 1 行の上限（AI 応答が長文になるため既定の 64KB では足りない）。
const maxUtteranceLineBytes = 16 * 1024 * 1024

// parseUtteranceHeading は "### utt-00012" 形式の見出しから番号を取り出す。
func parseUtteranceHeading(line string) (int, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "### utt-")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil {
		return 0, false
	}
	return n, true
}

// LoadSession はセッションのフロントマターと全発話を読む（読み出しのみ。記録を書き換えない）。
func (s *Store) LoadSession(id string) (*Session, []Utterance, error) {
	path := filepath.Join(s.root, filepath.FromSlash(SessionFile(id)))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("対話セッションを読み込めません（%s）: %w", id, err)
	}
	return parseSession(data)
}

// parseSession はフロントマター + 発話ブロック列を解釈する。
func parseSession(data []byte) (*Session, []Utterance, error) {
	header, body, err := splitFrontMatter(data)
	if err != nil {
		return nil, nil, err
	}
	var sess Session
	if err := yaml.Unmarshal(header, &sess); err != nil {
		return nil, nil, fmt.Errorf("対話セッションのフロントマターを解釈できません: %w", err)
	}
	if err := sess.Validate(); err != nil {
		return nil, nil, err
	}
	utterances, err := parseUtterances(body)
	if err != nil {
		return nil, nil, err
	}
	return &sess, utterances, nil
}

// splitFrontMatter は "---" で囲まれたフロントマターと本文に分ける。
func splitFrontMatter(data []byte) (header, body []byte, err error) {
	text := string(data)
	if !strings.HasPrefix(text, "---\n") {
		return nil, nil, fmt.Errorf("フロントマターがありません")
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		if strings.HasSuffix(rest, "\n---\n") || strings.HasSuffix(rest, "\n---") {
			return []byte(strings.TrimSuffix(strings.TrimSuffix(rest, "\n---"), "\n---\n")), nil, nil
		}
		return nil, nil, fmt.Errorf("フロントマターが閉じていません")
	}
	return []byte(rest[:end]), []byte(rest[end+len("\n---\n"):]), nil
}

// parseUtterances は発話ブロック列を解釈する。
// 末尾の不完全なブロック（追記の途中で切れた場合）は読み飛ばす。
func parseUtterances(body []byte) ([]Utterance, error) {
	lines := strings.Split(string(body), "\n")
	var out []Utterance
	var cur *Utterance
	var bodyLines []string
	flush := func() {
		if cur == nil {
			return
		}
		cur.Body = strings.Trim(strings.Join(bodyLines, "\n"), "\n")
		// 必須項目の揃わないブロックは採らない（追記途中の切断）。
		if cur.Validate() == nil {
			out = append(out, *cur)
		}
		cur, bodyLines = nil, nil
	}
	inMeta := false
	for _, line := range lines {
		if _, ok := parseUtteranceHeading(line); ok {
			flush()
			id := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "### "))
			cur = &Utterance{ID: id}
			inMeta = true
			continue
		}
		if cur == nil {
			continue
		}
		if inMeta && strings.HasPrefix(line, "- ") {
			key, value, ok := strings.Cut(strings.TrimPrefix(line, "- "), ":")
			if !ok {
				continue
			}
			value = strings.TrimSpace(value)
			switch strings.TrimSpace(key) {
			case "speaker":
				cur.Speaker = value
			case "at":
				at, err := time.Parse(time.RFC3339, value)
				if err != nil {
					return nil, fmt.Errorf("発話の日時を解釈できません（%s）: %w", cur.ID, err)
				}
				cur.At = at
			case "status":
				cur.Status = value
			}
			continue
		}
		inMeta = false
		bodyLines = append(bodyLines, line)
	}
	flush()
	return out, nil
}

// ListSessions は全対話セッションのフロントマターを ID 順で返す（セッション一覧のデータ源）。
func (s *Store) ListSessions() ([]Session, error) {
	dir := filepath.Join(s.root, dirSessions)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("対話セッションを走査できません: %w", err)
	}
	var out []Session
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		id := strings.TrimSuffix(name, ".md")
		if _, ok := IDSession.Parse(id); !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("対話セッションを読み込めません（%s）: %w", id, err)
		}
		header, _, err := splitFrontMatter(data)
		if err != nil {
			return nil, fmt.Errorf("対話セッションのフロントマターを解釈できません（%s）: %w", id, err)
		}
		var sess Session
		if err := yaml.Unmarshal(header, &sess); err != nil {
			return nil, fmt.Errorf("対話セッションのフロントマターを解釈できません（%s）: %w", id, err)
		}
		if err := sess.Validate(); err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SaveSessionMeta は併置メタデータを原子的に書き込む（更新頻度が高いため発話ファイルと分ける）。
func (s *Store) SaveSessionMeta(id string, m *SessionMeta) error {
	if _, ok := IDSession.Parse(id); !ok {
		return fmt.Errorf("対話セッションの ID が不正です: %q", id)
	}
	if m == nil || m.DialogueState == "" {
		return fmt.Errorf("対話状態がありません（%s）", id)
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("セッションメタデータを組み立てられません（%s）: %w", id, err)
	}
	return s.WriteFile(SessionMetaFile(id), data)
}

// LoadSessionMeta は併置メタデータを読む。未作成のときは nil を返す（中断前に保存されていない）。
func (s *Store) LoadSessionMeta(id string) (*SessionMeta, error) {
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(SessionMetaFile(id))))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("セッションメタデータを読み込めません（%s）: %w", id, err)
	}
	var m SessionMeta
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("セッションメタデータを解釈できません（%s）: %w", id, err)
	}
	return &m, nil
}

// ImportStakeholderSession は返送ファイルに含まれるステークホルダーセッションを
// プロジェクト内の S-nnnn へ再採番して保存する。
//
// 返送ファイル内の ID（S-0001 等）はステークホルダー側の採番でありプロジェクト内 ID と
// 衝突するため、採番し直して保存する。発話は本文・ID・日時とも無変更で写す（対話の記録を改変しない）。
// 受領原本は exchange/ に無変更で残るため、本処理は原本を書き換えない。
func (s *Store) ImportStakeholderSession(data []byte) (*Session, error) {
	sess, utterances, err := parseSession(data)
	if err != nil {
		return nil, fmt.Errorf("返送ファイルのステークホルダーセッションを解釈できません: %w", err)
	}
	if sess.Type != SessionStakeholder {
		return nil, fmt.Errorf("返送ファイルのセッションが %s ではありません: %q", SessionStakeholder, sess.Type)
	}
	imported := *sess
	// 担当者セッションの所有権はステークホルダーセッションに付けない。
	imported.Author = ""
	id, err := s.AllocateID(IDSession, func(id string) error {
		imported.ID = id
		if err := imported.Validate(); err != nil {
			return err
		}
		body := imported.marshalHeader()
		for _, u := range utterances {
			body = append(body, u.marshalBlock()...)
		}
		return s.WriteFile(SessionFile(id), body)
	})
	if err != nil {
		return nil, err
	}
	imported.ID = id
	return &imported, nil
}
