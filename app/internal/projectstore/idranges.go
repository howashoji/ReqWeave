package projectstore

// 本ファイルは ID の番号帯（id-ranges.yaml）。
//
// 共同プロジェクトでは、作業者ごとに確保した**区間**の中から ID を採番する。区間は重ならないため、
// 同期先へ到達できない間に複数の作業者が並行して記録を作っても ID が衝突しない。
//
// - **保持**: `id-ranges.yaml`（同期対象）。対象種別 → 確保済み区間の列（作業者・下限・上限・確保日時）。
// - **確保**: **反映の直前**に、同期先で見えている上限も跨いで自分の区間を確保する。
//   反映が成功したときに確定する（失敗したら巻き戻す。取り込みの完了後には確保しない）。
//   確保は `ReserveIDRanges`（呼び出しは同期処理の**反映の直前**。反映が成功したときに確定し、
//   失敗したら同期モジュールが巻き戻す）。
// - **採番**: 自分の確保済み区間の**未使用最小値**を使い、区間を跨がない。
// - **枯渇**: 残りが閾値（既定 20%）以下で警告し、使い切ったら新規採番を止めて同期を促す。
// - **単独利用（同期先未設定）**: 本ファイルを作らない。採番は「既存 ID の最大値 + 1」のまま
//   （`id-ranges.yaml` の有無で分岐する）。
//
// 番号は飛ぶ（区間の切り替わり・欠番の不再利用）。飛びを理由にエラー・警告を出さない。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// 番号帯の既定値（許容範囲は幅 50〜1000・枯渇予告 10〜30%）。
const (
	// DefaultIDRangeWidth は 1 回の確保で取る区間の幅（既定 100）。
	DefaultIDRangeWidth = 100
	// DefaultIDRangeWarnRatio は枯渇予告の閾値（残りがこの割合以下で警告）。
	DefaultIDRangeWarnRatio = 0.2
)

// IDRange は確保済みの区間 1 件。From・To はいずれも含む。
type IDRange struct {
	AuthorID   string    `yaml:"author_id"`
	From       int       `yaml:"from"`
	To         int       `yaml:"to"`
	ReservedAt time.Time `yaml:"reserved_at"`
}

// Count は区間に含まれる番号の個数。
func (r IDRange) Count() int { return r.To - r.From + 1 }

// IDRanges は id-ranges.yaml 全体（対象種別 → 区間の列）。
type IDRanges struct {
	Ranges map[string][]IDRange `yaml:"ranges"`
}

// UnmarshalIDRanges は id-ranges.yaml のバイト列を解釈する。
func UnmarshalIDRanges(data []byte) (*IDRanges, error) {
	var r IDRanges
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("id-ranges.yaml を解釈できません: %w", err)
	}
	// 読み込みは形の検証のみ（区間の重なりでは失敗させない）。重なりは、未反映のまま採番された区間が
	// 取り込みで合流したときにだけ起こりうる。ここで読み込みを失敗させるとプロジェクトが開けなくなるため、
	// **採番側で他の作業者の区間を避ける**ことで重複 ID を防ぐ（nextInRanges）。
	if err := r.validateShape(); err != nil {
		return nil, err
	}
	return &r, nil
}

// Marshal は id-ranges.yaml のバイト列を組み立てる。
func (r *IDRanges) Marshal() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if len(r.Ranges) == 0 {
		return []byte("ranges: {}\n"), nil
	}
	out, err := yaml.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("id-ranges.yaml を組み立てられません: %w", err)
	}
	return out, nil
}

// Validate は id-ranges.yaml の制約を検証する（対象種別・区間の下限上限・重なりの無いこと）。
// 自分が書き出す内容に対して用いる（書き込み前の検証）。
func (r *IDRanges) Validate() error {
	if err := r.validateShape(); err != nil {
		return err
	}
	for _, key := range sortedKeys(r.Ranges) {
		sorted := append([]IDRange(nil), r.Ranges[key]...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].From < sorted[j].From })
		prevTo := 0
		for _, e := range sorted {
			if e.From <= prevTo {
				return fmt.Errorf("id-ranges.yaml（%s）の区間が重なっています: %d〜%d", key, e.From, e.To)
			}
			prevTo = e.To
		}
	}
	return nil
}

// validateShape は対象種別・作業者・区間の値・確保日時を検証する（重なりは見ない）。
func (r *IDRanges) validateShape() error {
	for _, key := range sortedKeys(r.Ranges) {
		if !validRangeKey(key) {
			return fmt.Errorf("id-ranges.yaml に未知の対象種別があります: %q", key)
		}
		for i, e := range r.Ranges[key] {
			if _, err := NormalizeAuthorID(e.AuthorID); err != nil {
				return fmt.Errorf("id-ranges.yaml（%s）の %d 件目: %w", key, i+1, err)
			}
			if e.From < 1 || e.To < e.From {
				return fmt.Errorf("id-ranges.yaml（%s）の %d 件目の区間が不正です: %d〜%d", key, i+1, e.From, e.To)
			}
			if e.ReservedAt.IsZero() {
				return fmt.Errorf("id-ranges.yaml（%s）の %d 件目に確保日時がありません", key, i+1)
			}
		}
	}
	return nil
}

// othersCover は authorID **以外**が確保している区間に n が含まれるかを返す。
func (r *IDRanges) othersCover(key string, authorID string, n int) bool {
	for _, e := range r.Ranges[key] {
		if e.AuthorID != authorID && n >= e.From && n <= e.To {
			return true
		}
	}
	return false
}

// For は対象種別のうち authorID が確保した区間を下限の昇順で返す。
func (r *IDRanges) For(key, authorID string) []IDRange {
	var out []IDRange
	for _, e := range r.Ranges[key] {
		if e.AuthorID == authorID {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].From < out[j].From })
	return out
}

// maxTo は対象種別で確保済みの最大上限を返す（0 = 未確保）。
func (r *IDRanges) maxTo(key string) int {
	max := 0
	for _, e := range r.Ranges[key] {
		if e.To > max {
			max = e.To
		}
	}
	return max
}

// IDRangeStatus は番号帯の残量（枯渇予告の判断に使う）。
type IDRangeStatus struct {
	// Key は対象種別（RangeSession 等）。
	Key string
	// Reserved は確保済みの番号の個数、Used は使用済みの個数。
	Reserved int
	Used     int
	// Remaining は残りの個数（Reserved - Used）。
	Remaining int
	// Warn は残りが閾値以下か（同期による再確保へ誘導する）。
	Warn bool
	// Exhausted は使い切ったか（新規採番ができない）。
	Exhausted bool
}

// FileIDRanges は番号帯ファイルの名前。
const FileIDRanges = "id-ranges.yaml"

// LoadIDRanges は id-ranges.yaml を読む。ファイルが無い（単独利用・データ形式 1.4 より前）ときは空を返す。
func (s *Store) LoadIDRanges() (*IDRanges, error) {
	data, err := os.ReadFile(filepath.Join(s.root, FileIDRanges))
	if os.IsNotExist(err) {
		return &IDRanges{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("番号帯を読み込めません: %w", err)
	}
	return UnmarshalIDRanges(data)
}

// SaveIDRanges は id-ranges.yaml を原子的に書き込む（保存キュー経由）。
func (s *Store) SaveIDRanges(r *IDRanges) error {
	out, err := r.Marshal()
	if err != nil {
		return err
	}
	return s.write(func() error {
		return WriteFileAtomic(filepath.Join(s.root, FileIDRanges), out)
	})
}

// UsesIDRanges は番号帯からの採番を行うか（= id-ranges.yaml があるか）を返す。
//
// 単独利用（同期先未設定）のプロジェクトでは本ファイルを作らず、採番は既存 ID の最大値 + 1 のまま。
// 同期先を設定して最初に確保した時点で番号帯モードへ切り替わる。
func (s *Store) UsesIDRanges() bool {
	_, err := os.Stat(filepath.Join(s.root, FileIDRanges))
	return err == nil
}

// ReserveIDRanges は全対象種別について自分の新しい区間を確保する。
//
// 呼び出しは同期処理の**反映の直前**（取り込みの完了後には確保しない）。
// width は区間の幅（0 以下なら既定 100）。
//
// floors は対象種別 → 同期先で見えている確保済みの最大上限（同期モジュールが反映の直前に取得した値）。
// 作業コピーの `id-ranges.yaml` はまだ取り込んでいない他メンバーの確保を含まないことがあるため、
// **同期先で見えた上限も跨いで**確保する。nil なら作業コピーの内容だけで判断する。
//
// 自分の末尾の区間と連続する場合は区間を伸ばす（エントリの際限ない増加を避ける。区間の意味は変わらない）。
// 確保した区間を対象種別ごとに返す。
func (s *Store) ReserveIDRanges(width int, floors map[string]int) (map[string]IDRange, error) {
	if width <= 0 {
		width = DefaultIDRangeWidth
	}
	reserved := map[string]IDRange{}
	err := s.WithShortLock(LockRecords, func() error {
		ranges, err := s.LoadIDRanges()
		if err != nil {
			return err
		}
		if ranges.Ranges == nil {
			ranges.Ranges = map[string][]IDRange{}
		}
		now := time.Now().UTC().Truncate(time.Second)
		for _, key := range RangeKeys() {
			// 確保済みの最大上限の次から取る。**既存 ID の最大値も跨ぐ**（同期先を設定する前に採番済みの
			// 番号が区間を丸ごと占め、確保直後に枯渇するのを避ける）。番号が飛ぶことは許容する。
			start := ranges.maxTo(key)
			maxUsed, err := s.maxUsedNumber(key)
			if err != nil {
				return err
			}
			if maxUsed > start {
				start = maxUsed
			}
			// 同期先で見えた上限（未取り込みの他メンバーの確保）も跨ぐ
			if floors[key] > start {
				start = floors[key]
			}
			from := start + 1
			to := from + width - 1
			list := ranges.Ranges[key]
			if idx := lastOwnRangeIndex(list, s.author.AuthorID); idx >= 0 && list[idx].To == from-1 {
				list[idx].To = to
				list[idx].ReservedAt = now
				ranges.Ranges[key] = list
				reserved[key] = IDRange{AuthorID: s.author.AuthorID, From: from, To: to, ReservedAt: now}
				continue
			}
			e := IDRange{AuthorID: s.author.AuthorID, From: from, To: to, ReservedAt: now}
			ranges.Ranges[key] = append(list, e)
			reserved[key] = e
		}
		return s.SaveIDRanges(ranges)
	})
	if err != nil {
		return nil, err
	}
	return reserved, nil
}

// maxUsedNumber は対象種別で使用済みの最大連番を返す（0 = 未採番）。
func (s *Store) maxUsedNumber(key string) (int, error) {
	used, err := s.usedNumbers(key)
	if err != nil {
		return 0, err
	}
	max := 0
	for n := range used {
		if n > max {
			max = n
		}
	}
	return max, nil
}

// lastOwnRangeIndex は authorID が持つ区間のうち上限が最大のものの添字を返す（無ければ -1）。
func lastOwnRangeIndex(list []IDRange, authorID string) int {
	idx, max := -1, 0
	for i, e := range list {
		if e.AuthorID == authorID && e.To >= max {
			idx, max = i, e.To
		}
	}
	return idx
}

// IDRangeStatuses は全対象種別の番号帯の残量を返す（番号帯を使っていなければ nil）。
//
// 枯渇予告（残り 20% 以下）と枯渇の判定に使う。使用済みの数は実体の走査で求める
// （カウンタを持たず実体と二重管理しない。単独利用の採番と同じ方針）。
func (s *Store) IDRangeStatuses(warnRatio float64) ([]IDRangeStatus, error) {
	if !s.UsesIDRanges() {
		return nil, nil
	}
	if warnRatio <= 0 || warnRatio >= 1 {
		warnRatio = DefaultIDRangeWarnRatio
	}
	ranges, err := s.LoadIDRanges()
	if err != nil {
		return nil, err
	}
	var out []IDRangeStatus
	for _, key := range RangeKeys() {
		mine := ranges.For(key, s.author.AuthorID)
		reserved := 0
		for _, e := range mine {
			reserved += e.Count()
		}
		used, err := s.usedInRanges(key, mine)
		if err != nil {
			return nil, err
		}
		st := IDRangeStatus{Key: key, Reserved: reserved, Used: used, Remaining: reserved - used}
		st.Exhausted = st.Remaining <= 0
		st.Warn = st.Exhausted || float64(st.Remaining) <= float64(reserved)*warnRatio
		out = append(out, st)
	}
	return out, nil
}

// usedInRanges は自分の区間の中で使用済みの番号の個数を返す。
func (s *Store) usedInRanges(key string, mine []IDRange) (int, error) {
	used, err := s.usedNumbers(key)
	if err != nil {
		return 0, err
	}
	count := 0
	for n := range used {
		for _, e := range mine {
			if n >= e.From && n <= e.To {
				count++
				break
			}
		}
	}
	return count, nil
}

// usedNumbers は対象種別で使用済みの連番の集合を実体から求める。
//
// 要件項目（`requirement`）はグループごとに番号空間が分かれるため、**全グループの和集合**を用いる
// （区間は作業者ごとに重ならないので、和集合で見ても他の作業者の番号を奪わない）。
func (s *Store) usedNumbers(key string) (map[int]bool, error) {
	out := map[int]bool{}
	if key == RangeRequirement {
		ids, err := listRequirementIDs(s.root)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if _, _, n, ok := ParseRequirementID(id); ok {
				out[n] = true
			}
		}
		return out, nil
	}
	kind, ok := idKindForRange(key)
	if !ok {
		return nil, fmt.Errorf("番号帯の対象種別が不正です: %q", key)
	}
	nums, err := kind.scanUsed(s.root, kind)
	if err != nil {
		return nil, err
	}
	for _, n := range nums {
		out[n] = true
	}
	return out, nil
}

// nextInRanges は自分の確保済み区間の未使用最小値を返す（番号帯モードの採番）。
//
// 区間を跨がない。使い切っている場合は、原因と次に取る行動（同期）を示すエラーを返す
// （採番の重複を起こさない）。extraUsed は同一呼び出し内で
// 既に使うと決めた番号（要件項目のようにグループ別の実体を見るときの補正）。
func (s *Store) nextInRanges(key string, used map[int]bool) (int, error) {
	ranges, err := s.LoadIDRanges()
	if err != nil {
		return 0, err
	}
	mine := ranges.For(key, s.author.AuthorID)
	if len(mine) == 0 {
		return 0, fmt.Errorf("%sの番号帯がまだ確保されていません。同期（取り込み・反映）を実行してください。",
			rangeKeyLabel(key))
	}
	for _, e := range mine {
		for n := e.From; n <= e.To; n++ {
			// 他の作業者の区間と重なる番号は使わない。健全な `id-ranges.yaml` では重なりが無いため
			// 挙動は変わらない。未反映のまま採番された区間が取り込みで合流して重なった場合でも、
			// **重複 ID を作らない**（採番できないときは同期を促す）。
			if used[n] || ranges.othersCover(key, s.author.AuthorID, n) {
				continue
			}
			return n, nil
		}
	}
	return 0, fmt.Errorf("%sの番号帯を使い切ったため、新しく作成できません。同期（取り込み・反映）を実行して番号帯を確保してください。",
		rangeKeyLabel(key))
}

// IDRangeReserver は同期モジュールの受け口（sync.RangeReserver）に適合させる薄い実装。
//
// 同期処理（**反映の直前**）から呼ばれ、開いているプロジェクトの番号帯を確保する。
// **本メソッドが呼ばれた時点で `id-ranges.yaml` を作る**ため、同期先を設定して初めて同期した時点から
// 番号帯モードになる（単独利用のプロジェクトでは作られない）。
type IDRangeReserver struct {
	// Store は対象のプロジェクト。
	Store *Store
	// Width は区間の幅（0 = 既定 100。許容範囲 50〜1000）。
	Width int
}

// ReserveIDRanges は sync.RangeReserver を満たす。root が開いているプロジェクトと異なる場合は何もしない
// （取り違えて別のプロジェクトの番号帯を進めない）。
func (r IDRangeReserver) ReserveIDRanges(root string, floors map[string]int) error {
	if r.Store == nil {
		return nil
	}
	if filepath.Clean(root) != filepath.Clean(r.Store.Root()) {
		return nil
	}
	_, err := r.Store.ReserveIDRanges(r.Width, floors)
	return err
}
