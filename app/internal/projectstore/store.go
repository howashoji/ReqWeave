package projectstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// ErrStoreClosed は閉じた Store への操作。
var ErrStoreClosed = errors.New("プロジェクトは既に閉じられています")

// Store は 1 つのプロジェクトフォルダに対する読み書きの入口。
//
// 当該プロジェクトへの全書き込みを単一の保存キュー（goroutine + チャネル）で
// 直列化する（書き込みの順序が入れ替わらないように）。読み取りは直列化しない。
type Store struct {
	root       string
	compat     Compatibility
	author     Author     // 操作中の作業者（予約・変更履歴に記録する）
	lockPolicy lockPolicy // 待ち・残留判定の設定

	// onLockDenied は別ウィンドウの処理中でロックを取れなかったことの通知先。
	// **記録先を知るのはバインディング層だけ**（aiprovider の OnEvent と同じ委譲の形。
	// 本層は動作ログのパッケージへ依存しない = depcheck の logs-writer）。nil なら何もしない。
	onLockDenied func(target string)

	ops       chan func()
	done      chan struct{}
	closeMu   sync.RWMutex
	closed    bool
	closeOnce sync.Once

	mu      sync.RWMutex
	project *Project
}

// SetLockDeniedHandler はロックを取れなかったことの通知先を差し替える（既定は通知しない）。
func (s *Store) SetLockDeniedHandler(fn func(target string)) { s.onLockDenied = fn }

// notifyLockDenied は通知先へ知らせる（未設定なら何もしない）。
func (s *Store) notifyLockDenied(target string) {
	if s.onLockDenied != nil {
		s.onLockDenied(target)
	}
}

// Open は既存のプロジェクトフォルダを開く。
//
// 自版より新しいメジャー形式のときは *ErrTooNew を返し、いかなるファイルにも書き込まない
// （新しい版のデータを古い版が壊さないように）。自版より古い形式のときは *ErrNeedsMigration を返す
// （退避なしの黙った形式変更を避ける。移行は移行前の自動退避と組にして行う）。
func Open(root string, author Author) (*Store, error) {
	normalized, err := NormalizeAuthorID(author.AuthorID)
	if err != nil {
		return nil, err
	}
	author.AuthorID = normalized
	if author.DisplayName == "" {
		return nil, fmt.Errorf("作業者の表示名がありません")
	}

	data, err := os.ReadFile(filepath.Join(root, FileProject))
	if err != nil {
		return nil, fmt.Errorf("プロジェクトデータを読み込めません（%s）: %w", filepath.Join(root, FileProject), err)
	}
	project, err := UnmarshalProject(data)
	if err != nil {
		return nil, err
	}
	compat, err := project.FormatCompatibility()
	if err != nil {
		return nil, err
	}
	switch compat {
	case CompatTooNew:
		found, _ := ParseFormatVersion(project.FormatVersion)
		current, _ := ParseFormatVersion(CurrentFormatVersion)
		return nil, &ErrTooNew{Found: found, Current: current}
	case CompatNeedsMigration:
		found, _ := ParseFormatVersion(project.FormatVersion)
		current, _ := ParseFormatVersion(CurrentFormatVersion)
		return nil, &ErrNeedsMigration{Found: found, Current: current}
	}

	// 前回落ちたときに残った一時ファイルの残骸を掃除する（残骸がフォルダに溜まり続けないように）。
	// 開くたびに行い、失敗しても開く動作は止めない（掃除は最善努力）。
	sweepStaleTempFiles(root)

	s := &Store{
		root:       root,
		compat:     compat,
		author:     author,
		lockPolicy: defaultLockPolicy(),
		ops:        make(chan func()),
		done:       make(chan struct{}),
		project:    project,
	}
	go s.run()
	return s, nil
}

func (s *Store) run() {
	for op := range s.ops {
		op()
	}
	close(s.done)
}

// Close は保存キューを止める。実行中・待機中の書き込みは完了してから止まる。
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		s.closeMu.Lock()
		s.closed = true
		close(s.ops)
		s.closeMu.Unlock()
		<-s.done
	})
	return nil
}

// Author は操作中の作業者を返す。
func (s *Store) Author() Author { return s.author }

// Root はプロジェクトフォルダのパスを返す。
func (s *Store) Root() string { return s.root }

// Compatibility は開いたデータ形式と自版との関係を返す。
func (s *Store) Compatibility() Compatibility { return s.compat }

// Project は現在の project.yaml の内容の複製を返す（呼び出し側の変更が Store に影響しない）。
func (s *Store) Project() *Project {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.project.clone()
}

// UpdateProject は project.yaml を読み直し→変更→原子的書き込みの順で更新する。
// 変更関数は保存キューの中で実行されるため、当該プロジェクトへの他の書き込みと直列化される。
func (s *Store) UpdateProject(mutate func(*Project) error) error {
	return s.write(func() error {
		data, err := os.ReadFile(filepath.Join(s.root, FileProject))
		if err != nil {
			return fmt.Errorf("プロジェクトデータを読み込めません: %w", err)
		}
		project, err := UnmarshalProject(data)
		if err != nil {
			return err
		}
		if err := mutate(project); err != nil {
			return err
		}
		out, err := project.Marshal()
		if err != nil {
			return err
		}
		if err := WriteFileAtomic(filepath.Join(s.root, FileProject), out); err != nil {
			return err
		}
		s.mu.Lock()
		s.project = project
		s.mu.Unlock()
		return nil
	})
}

// LoadMembers は members.yaml を読む。
func (s *Store) LoadMembers() (*Members, error) {
	data, err := os.ReadFile(filepath.Join(s.root, FileMembers))
	if err != nil {
		return nil, fmt.Errorf("メンバー一覧を読み込めません: %w", err)
	}
	return UnmarshalMembers(data)
}

// SaveMembers は members.yaml を原子的に書き込む（保存キュー経由）。
// メンバー管理の操作単位（ロック・変更履歴）は本メソッドの上に載せる（members.go）。
func (s *Store) SaveMembers(m *Members) error {
	out, err := m.Marshal()
	if err != nil {
		return err
	}
	return s.write(func() error {
		return WriteFileAtomic(filepath.Join(s.root, FileMembers), out)
	})
}

// WriteFile はプロジェクトフォルダ内の相対パスへ原子的に書き込む（保存キュー経由）。
// 親ディレクトリが無ければ作る。
func (s *Store) WriteFile(rel string, data []byte) error {
	path := filepath.Join(s.root, filepath.FromSlash(rel))
	return s.write(func() error {
		if err := os.MkdirAll(filepath.Dir(path), dataDirMode); err != nil {
			return fmt.Errorf("フォルダを作成できません（%s）: %w", filepath.Dir(path), err)
		}
		return WriteFileAtomic(path, data)
	})
}

// RemoveFile はプロジェクトフォルダ内のファイルを削除する（存在しない場合は成功扱い）。
//
// 一時状態ファイル（取り込み分析の analysis.meta.yaml）の破棄に使う。
// 書き込みと同じ保存キューを通し、書き込みと削除の順序が入れ替わらないようにする。
// レコード本体（決定・未決・要件項目）を消す経路には使わない（追記・状態変化で表す）。
func (s *Store) RemoveFile(rel string) error {
	path := filepath.Join(s.root, filepath.FromSlash(rel))
	return s.write(func() error {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("ファイルを削除できません（%s）: %w", path, err)
		}
		return nil
	})
}

// AppendFile は追記専用ファイル（変更履歴などの監査データ）へ 1 行を追記する。
//
// 一時ファイル + rename ではなく O_APPEND 追記とし、保存キュー経由で直列化する
// （行単位の自己完結により、途中で切れても他の行を読み飛ばせる）。
func (s *Store) AppendFile(rel string, line []byte) error {
	path := filepath.Join(s.root, filepath.FromSlash(rel))
	return s.write(func() error {
		if err := os.MkdirAll(filepath.Dir(path), dataDirMode); err != nil {
			return fmt.Errorf("フォルダを作成できません（%s）: %w", filepath.Dir(path), err)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, dataFileMode)
		if err != nil {
			return fmt.Errorf("追記できません（%s）: %w", path, err)
		}
		defer f.Close()
		if _, err := f.Write(line); err != nil {
			return fmt.Errorf("追記に失敗（%s）: %w", path, err)
		}
		return f.Sync()
	})
}

// write は書き込み操作を保存キューへ投入し、完了まで待つ。
func (s *Store) write(fn func() error) error {
	if s.compat == CompatTooNew {
		found, _ := ParseFormatVersion(s.project.FormatVersion)
		current, _ := ParseFormatVersion(CurrentFormatVersion)
		return &ErrTooNew{Found: found, Current: current}
	}
	errc := make(chan error, 1)
	s.closeMu.RLock()
	if s.closed {
		s.closeMu.RUnlock()
		return ErrStoreClosed
	}
	s.ops <- func() { errc <- fn() }
	s.closeMu.RUnlock()
	return <-errc
}

// clone は Project の複製を返す（共有される可変フィールドを含めて複製する）。
func (p *Project) clone() *Project {
	c := *p
	if p.DomainPresets != nil {
		c.DomainPresets = append([]string(nil), p.DomainPresets...)
	}
	if p.Sync != nil {
		sy := *p.Sync
		c.Sync = &sy
	}
	if p.UsageLimit != nil {
		ul := *p.UsageLimit
		if p.UsageLimit.WarnRatio != nil {
			r := *p.UsageLimit.WarnRatio
			ul.WarnRatio = &r
		}
		c.UsageLimit = &ul
	}
	if p.unknown != nil {
		c.unknown = make(map[string]*yaml.Node, len(p.unknown))
		for k, v := range p.unknown {
			c.unknown[k] = v
		}
	}
	return &c
}
