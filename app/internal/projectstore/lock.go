package projectstore

// 本ファイルは `locks/`（同一端末の多重プロセス保護）。
//
// 以前は端末間の排他（文書ロック）に用いていたが、作業コピーを同期する方式への移行で端末間の排他は
// 予約（`reservations.yaml` = reservations.go）へ置き換えた。`locks/` は
// **同一端末で同じ作業コピーを複数のプロセスが開いた場合**の保護にのみ用いる
// （アプリを単一インスタンスに制限しないため全廃できない）。同期の対象に含めない（端末内の事情のため）。
//
// - 対象は書き込みを伴う操作（`documents-*` の生成・確定、`records` の反映、`members` / `roster` / `terms` の更新）。
// - 取得は O_EXCL 相当の排他作成。内容は `app_instance_id`（起動ごとの UUID）・`acquired_at`・`heartbeat_at`。
//   **作業者名を持たない**（同一端末内の事象であり、「このプロジェクトを開いている別のウィンドウ」として提示する）。
// - 残留（`heartbeat_at` が閾値より古い）は確認操作を経て解除できる。自動解除はしない。
// - 以前の `ids` ロックは廃止した（番号帯により端末間・プロセス間の採番衝突が起きないため。
//   同一端末の多重プロセスに対しては `records` ロックの取得で足りる）。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// ロック対象 ID（ファイル名 = <対象ID>.lock）。
const (
	LockRecords               = "records"
	LockMembers               = "members"
	LockRoster                = "roster"
	LockTerms                 = "terms"
	LockDocumentsRequirements = "documents-requirements"
	LockDocumentsBasicDesign  = "documents-basic-design"
	lockDir                   = "locks"
	lockExt                   = ".lock"
	defaultHeartbeatInterval  = 60 * time.Second // 許容範囲 30〜120 秒
	defaultStaleAfter         = 5 * time.Minute  // 許容範囲 3〜10 分。閾値 > 間隔 ×3
	defaultShortLockTimeout   = 20 * time.Second // 許容範囲 10〜30 秒
	defaultShortLockRetryMin  = 100 * time.Millisecond
	defaultShortLockRetryMax  = 1 * time.Second
)

// ErrOtherWindowBusy は「このプロジェクトを開いている別のウィンドウが処理中」の利用者向け文言
// （同一端末内の事象のため作業者名を出さない）。
const ErrOtherWindowBusy = "このプロジェクトを開いている別のウィンドウが処理中です。しばらく待って再実行してください。"

// appInstanceID は起動ごとの UUID（ロックファイルの内容）。
var appInstanceID = uuid.NewString()

// AppInstanceID は本プロセスの起動 ID を返す（自分のウィンドウが保持しているかの判定に使う）。
func AppInstanceID() string { return appInstanceID }

// LockInfo はロックファイルの内容。作業者名は持たない。
type LockInfo struct {
	AppInstanceID string    `yaml:"app_instance_id"`
	AcquiredAt    time.Time `yaml:"acquired_at"`
	HeartbeatAt   time.Time `yaml:"heartbeat_at"`
}

// IsStale は heartbeat_at が閾値より古い = 残留ロックかを返す。
// 残留と判定しても自動解除はしない（確認操作を経た解除のみ）。
func (i LockInfo) IsStale(now time.Time, staleAfter time.Duration) bool {
	return now.Sub(i.HeartbeatAt) >= staleAfter
}

// ErrLockHeld は別のプロセス（ウィンドウ）がロックを保持しているために取得できなかったことを表す。
type ErrLockHeld struct {
	Target string
	Holder LockInfo
	Stale  bool // 残留（heartbeat が閾値より古い）とみなせるか = 解除の候補
}

func (e *ErrLockHeld) Error() string {
	return ErrOtherWindowBusy
}

// lockPolicy はロックの待ち・残留判定の設定（許容範囲内で調整する）。
type lockPolicy struct {
	heartbeatInterval time.Duration
	staleAfter        time.Duration
	shortTimeout      time.Duration
	retryMin          time.Duration
	retryMax          time.Duration
}

func defaultLockPolicy() lockPolicy {
	return lockPolicy{
		heartbeatInterval: defaultHeartbeatInterval,
		staleAfter:        defaultStaleAfter,
		shortTimeout:      defaultShortLockTimeout,
		retryMin:          defaultShortLockRetryMin,
		retryMax:          defaultShortLockRetryMax,
	}
}

// Lock は保持中のロック。Release で解放する。
type Lock struct {
	path   string
	target string

	mu       sync.Mutex
	released bool
	stop     chan struct{}
	done     chan struct{}
}

// Target はロック対象 ID を返す。
func (l *Lock) Target() string { return l.target }

// Release はロックファイルを削除する（解放）。多重呼び出しは無害。
func (l *Lock) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	l.released = true
	if l.stop != nil {
		close(l.stop)
		<-l.done
	}
	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("ロックを解放できません（%s）: %w", l.path, err)
	}
	return nil
}

// lockPath は対象 ID に対応するロックファイルのパス。
func (s *Store) lockPath(target string) string {
	return filepath.Join(s.root, lockDir, target+lockExt)
}

// tryAcquireLock は O_EXCL でロックファイルを作る（取得）。
// 既に保持者がいる場合は *ErrLockHeld を返す。
func (s *Store) tryAcquireLock(target string, withHeartbeat bool) (*Lock, error) {
	path := s.lockPath(target)
	if err := os.MkdirAll(filepath.Dir(path), dataDirMode); err != nil {
		return nil, fmt.Errorf("ロックフォルダを作成できません: %w", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	info := LockInfo{
		AppInstanceID: appInstanceID,
		AcquiredAt:    now,
		HeartbeatAt:   now,
	}
	data, err := yaml.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("ロック情報を組み立てられません: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, dataFileMode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			holder, readErr := readLockInfo(path)
			if readErr != nil {
				// 直前に解放された・書き込み途中などで読めない場合は保持者不明として扱う
				return nil, &ErrLockHeld{Target: target}
			}
			return nil, &ErrLockHeld{
				Target: target,
				Holder: holder,
				Stale:  holder.IsStale(time.Now().UTC(), s.lockPolicy.staleAfter),
			}
		}
		return nil, fmt.Errorf("ロックを取得できません（%s）: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("ロック情報を書き込めません（%s）: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("ロック情報を同期できません（%s）: %w", path, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("ロックファイルを閉じられません（%s）: %w", path, err)
	}

	l := &Lock{path: path, target: target}
	if withHeartbeat {
		l.stop = make(chan struct{})
		l.done = make(chan struct{})
		go l.heartbeat(info, s.lockPolicy.heartbeatInterval)
	}
	return l, nil
}

// heartbeat は保持中のロックの heartbeat_at を定期更新する。
func (l *Lock) heartbeat(info LockInfo, interval time.Duration) {
	defer close(l.done)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
			info.HeartbeatAt = time.Now().UTC().Truncate(time.Second)
			data, err := yaml.Marshal(info)
			if err != nil {
				continue
			}
			// **解放済み・削除済みのロックを再作成しない**（外から消されたロックを復活させない）。
			// 以前は Stat で存在を確かめてから原子的書き込みをしていたが、確認と書き込みの
			// 間に消されると再作成してしまう（外からフォルダを消したときに実測）。
			// 既存のファイルにだけ書けるよう O_CREATE を付けずに開く。
			// 内容は固定長でなく短いため、書き込み前に切り詰めて 1 回で書く。
			f, oerr := os.OpenFile(l.path, os.O_WRONLY|os.O_TRUNC, dataFileMode)
			if oerr != nil {
				continue
			}
			_, werr := f.Write(data)
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				continue
			}
		}
	}
}

// readLockInfo はロックファイルを読む。
func readLockInfo(path string) (LockInfo, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return LockInfo{}, err
	}
	var info LockInfo
	if err := yaml.Unmarshal(b, &info); err != nil {
		return LockInfo{}, err
	}
	return info, nil
}

// AcquireLock は長い操作（`documents-*` の生成・確定）の間、同一端末の別プロセスを締め出す
// ロックを取得する。取得できない場合は待たずに *ErrLockHeld を返す。
// 保持中は heartbeat_at を定期更新する。
func (s *Store) AcquireLock(target string) (*Lock, error) {
	lock, err := s.tryAcquireLock(target, true)
	var held *ErrLockHeld
	if errors.As(err, &held) {
		s.notifyLockDenied(target)
	}
	return lock, err
}

// LockState はロックファイルの内容を返す（保持されていなければ ok = false）。
func (s *Store) LockState(target string) (LockInfo, bool) {
	info, err := readLockInfo(s.lockPath(target))
	if err != nil {
		return LockInfo{}, false
	}
	return info, true
}

// WithShortLock は短時間ロック（records / members / roster / terms）を取り、fn を実行して解放する。
// 取得待ちはリトライし、タイムアウト時は再実行できる形のエラーを返す
// （無限待ち・無通知の失敗をしない）。
func (s *Store) WithShortLock(target string, fn func() error) error {
	deadline := time.Now().Add(s.lockPolicy.shortTimeout)
	wait := s.lockPolicy.retryMin
	for {
		lock, err := s.tryAcquireLock(target, false)
		if err == nil {
			defer func() { _ = lock.Release() }()
			return fn()
		}
		var held *ErrLockHeld
		if !errors.As(err, &held) {
			return err
		}
		if time.Now().After(deadline) {
			// 待っても取れなかったことだけを知らせる（リトライ中は知らせない = 記録が積もらない）。
			s.notifyLockDenied(target)
			return fmt.Errorf("%w", err)
		}
		time.Sleep(wait)
		if wait < s.lockPolicy.retryMax {
			wait *= 2
			if wait > s.lockPolicy.retryMax {
				wait = s.lockPolicy.retryMax
			}
		}
	}
}

// ---- ロックの一覧と残留の解除 --------------------------------------------------

// HeldLock は保持中のロック 1 件。
type HeldLock struct {
	// Target はロック対象 ID（ファイル名から拡張子を除いたもの）。
	Target string
	Holder LockInfo
	// Stale は残留とみなせるか（heartbeat_at が閾値より古い）。解除の候補。
	Stale bool
	// SelfInstance は本プロセス（このウィンドウ）が保持しているか。
	SelfInstance bool
}

// HeldLocks は保持中のロックを対象 ID の昇順で返す。
//
// 読み出しのみで、残留と判定しても解除しない。読めないロックファイル（書き込み途中・直前の解放）は
// 内容不明として含める（黙って落とすと「誰も保持していない」と誤解させるため）。
func (s *Store) HeldLocks() ([]HeldLock, error) {
	dir := filepath.Join(s.root, lockDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ロックの一覧を取得できません: %w", err)
	}
	now := time.Now().UTC()
	var out []HeldLock
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), lockExt) {
			continue
		}
		target := strings.TrimSuffix(e.Name(), lockExt)
		held := HeldLock{Target: target}
		if info, readErr := readLockInfo(filepath.Join(dir, e.Name())); readErr == nil {
			held.Holder = info
			held.Stale = info.IsStale(now, s.lockPolicy.staleAfter)
			held.SelfInstance = info.AppInstanceID != "" && info.AppInstanceID == appInstanceID
		}
		out = append(out, held)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	return out, nil
}

// ForceReleaseLock は残留ロックを解除する。
//
// **確認操作は呼び出し側の責務**。自分が保持しているロックは通常の解放（Lock.Release）を使う。
// 解除した時点のロック内容を返す（表示に使う）。
func (s *Store) ForceReleaseLock(target string) (LockInfo, error) {
	if strings.TrimSpace(target) == "" {
		return LockInfo{}, fmt.Errorf("解除するロックを選んでください")
	}
	path := s.lockPath(target)
	info, err := readLockInfo(path)
	if os.IsNotExist(err) {
		return LockInfo{}, fmt.Errorf("このロックは既に解放されています（%s）", target)
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return LockInfo{}, fmt.Errorf("このロックは既に解放されています（%s）", target)
		}
		return LockInfo{}, fmt.Errorf("ロックを解除できません（%s）: %w", target, err)
	}
	return info, nil
}

// AnyLockHeld はプロジェクトを開かずに `locks/` にロックファイルがあるかを返す
// （プロジェクトの削除前の確認。別のウィンドウが処理中なら削除しない）。
func AnyLockHeld(root string) bool {
	entries, err := os.ReadDir(filepath.Join(root, lockDir))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), lockExt) {
			return true
		}
	}
	return false
}
