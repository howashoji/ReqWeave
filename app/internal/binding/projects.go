package binding

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/exchange"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// ProjectSummary はプロジェクト一覧の 1 行。
type ProjectSummary struct {
	Path             string `json:"path"`
	ProjectID        string `json:"projectId"`
	TargetSystemName string `json:"targetSystemName"`
	Phase            string `json:"phase"`
	PhaseLabel       string `json:"phaseLabel"`
	// UpdatedAt は UTC の ISO 8601。表示時にローカルへ変換する。
	UpdatedAt string `json:"updatedAt"`
	Role      string `json:"role"`
	RoleLabel string `json:"roleLabel"`
	// Working は作業状況（未解除の予約。「作業者名（進め方）」の並び）。
	// 最後に取り込んだ時点の情報であり、同期先の最新ではない。
	Working []string `json:"working,omitempty"`
	// Available は一覧に必要な情報を読めたか（共有フォルダ未接続・削除済みなら false）。
	Available bool `json:"available"`
	// SyncConfigured は同期先が設定された共同プロジェクトか（false = この端末だけで扱う）。
	SyncConfigured bool `json:"syncConfigured"`
	// SyncKindLabel は同期先の種別の表示名（設定時のみ）。
	SyncKindLabel string `json:"syncKindLabel,omitempty"`
	// LastSyncedAt は最後に同期した日時（同期の記録の最新。空 = まだ同期していない）。
	LastSyncedAt string `json:"lastSyncedAt,omitempty"`
	// HasUnpublished は同期先へまだ載せていない変更があるか。
	HasUnpublished bool `json:"hasUnpublished"`
	// Notice は読めない・参加していない等の理由（原因＋次の行動の 1 文）。
	Notice string `json:"notice,omitempty"`
}

// DomainPresetOption は業務領域の選択肢（組み込みの 4 領域）。
type DomainPresetOption struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Summary string `json:"summary"`
}

// domainPresetOptions は業務領域の選択肢を組み込みのプリセット定義から作る。
//
// 定義の正本は対話エンジンの presets.yaml（観点まで含む）であり、画面の選択肢はそこから導く
// （領域一覧を 2 か所に持たない = 二重管理の禁止）。アプリの版更新でのみ保守する（外部取得しない）。
func domainPresetOptions() ([]DomainPresetOption, error) {
	presets, err := dialogue.LoadDomainPresets()
	if err != nil {
		return nil, err
	}
	out := make([]DomainPresetOption, 0, len(presets.Domains))
	for _, d := range presets.Domains {
		out = append(out, DomainPresetOption{ID: d.ID, Label: d.Name, Summary: d.Summary})
	}
	return out, nil
}

// CreateProjectRequest は新規作成の入力。
type CreateProjectRequest struct {
	// Path は**保存先**のフォルダ。この下へ本システムが
	// `<対象システム名>.reqweave` を作る。利用者にフォルダ名を決めさせない。
	Path             string `json:"path"`
	TargetSystemName string `json:"targetSystemName"`
	// Summary は対象システムの概要（任意）。
	Summary       string   `json:"summary"`
	DomainPresets []string `json:"domainPresets"`
}

// DeletePreview は削除前の確認表示（確認操作なしに削除しない）。
type DeletePreview struct {
	Path             string `json:"path"`
	TargetSystemName string `json:"targetSystemName"`
	HasDocuments     bool   `json:"hasDocuments"`
	// BackupCount は削除後も残る自動退避の世代数（復元できることを利用者へ示す）。
	BackupCount int    `json:"backupCount"`
	Deletable   bool   `json:"deletable"`
	Notice      string `json:"notice,omitempty"`
}

// DomainPresets は業務領域の選択肢を返す（作成時の選択と設定変更で使う）。
func (a *API) DomainPresets() ([]DomainPresetOption, error) { return domainPresetOptions() }

// phaseLabel は現在フェーズの表示名。値集合はプロジェクトのデータ形式で閉じている。
func phaseLabel(phase string) string {
	switch phase {
	case projectstore.PhaseRequirements:
		return "要件定義"
	case projectstore.PhaseBasicDesign:
		return "基本設計"
	default:
		// 生のコード値を画面に出さない（利用者に内部の値を見せない）
		return "状態不明"
	}
}

// roleLabel は権限の表示名。値集合はメンバーのデータ形式で閉じている。
func roleLabel(role string) string {
	switch role {
	case projectstore.RoleOwner:
		return "オーナー"
	case projectstore.RoleEditor:
		return "編集"
	case projectstore.RoleViewer:
		return "閲覧"
	default:
		return "権限不明"
	}
}

// Projects は既知のプロジェクト一覧を返す。
//
// 一覧の範囲はアプリ設定の recent_projects。到達できないプロジェクトは
// 一覧から消さず、理由つきで示す（黙って消さない）。
func (a *API) Projects() ([]ProjectSummary, error) {
	settings, err := a.settings()
	if err != nil {
		return nil, err
	}
	author, _ := settings.Author()
	// 利用者がフォルダ名を変えた場合に備え、識別子で探し直して記録を直す。
	// 直しておかないと毎回探すことになり、一覧を開くたびに親フォルダを走査する。
	if settings.ResolveRecentProjects() {
		_ = projectstore.SaveSettings(a.paths, settings)
	}
	// 同期の状態は 1 つのクライアントで全件を見る（プロジェクトごとに git を探し直さない）。
	client := a.syncStatusClient(author)
	out := make([]ProjectSummary, 0, len(settings.RecentProjects))
	for _, path := range settings.RecentProjectPaths() {
		summary := a.summarize(path, author)
		a.applySyncSummary(&summary, client)
		out = append(out, summary)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

// summarize は 1 プロジェクトの一覧行を組み立てる（プロジェクトを開かずに読む遅延読込）。
func (a *API) summarize(path string, author projectstore.Author) ProjectSummary {
	summary := ProjectSummary{Path: path}
	data, err := os.ReadFile(filepath.Join(path, projectstore.FileProject))
	if err != nil {
		summary.Notice = "このプロジェクトを読み込めません。フォルダの場所と共有フォルダの接続を確認してください。"
		return summary
	}
	project, err := projectstore.UnmarshalProject(data)
	if err != nil {
		summary.Notice = "プロジェクトデータが読み込めません。自動退避からの復元を実行してください。"
		return summary
	}
	summary.Available = true
	summary.ProjectID = project.ProjectID
	summary.TargetSystemName = project.TargetSystemName
	summary.Phase = project.Phase
	summary.PhaseLabel = phaseLabel(project.Phase)
	summary.UpdatedAt = lastModified(path).UTC().Format(time.RFC3339)
	if project.Sync != nil {
		summary.SyncConfigured = true
		summary.SyncKindLabel = projectstore.SyncKindLabel(project.Sync.Kind)
	}

	members, err := loadMembers(path)
	if err != nil {
		return summary
	}
	member, ok := members.Find(author.AuthorID)
	if !ok {
		summary.Notice = "このプロジェクトのメンバーに登録されていません。オーナーに登録を依頼してください。"
		return summary
	}
	summary.Role = member.Role
	summary.RoleLabel = roleLabel(member.Role)
	summary.Working = workingMembers(path)
	return summary
}

// loadProject は project.yaml を読む（プロジェクトを開かずに一覧・照合で使う）。
func loadProject(path string) (*projectstore.Project, error) {
	data, err := os.ReadFile(filepath.Join(path, projectstore.FileProject))
	if err != nil {
		return nil, err
	}
	return projectstore.UnmarshalProject(data)
}

func loadMembers(path string) (*projectstore.Members, error) {
	data, err := os.ReadFile(filepath.Join(path, projectstore.FileMembers))
	if err != nil {
		return nil, err
	}
	return projectstore.UnmarshalMembers(data)
}

// workingMembers は作業状況（未解除の予約）を「作業者名（進め方）」の並びで返す。
//
// プロジェクトを開かずに reservations.yaml だけを読む（一覧は遅延読込）。読めない・壊れている場合は出さない
// （誤った作業状況を出さない）。表示は最後に取り込んだ時点の情報。
func workingMembers(path string) []string {
	var out []string
	for _, r := range projectstore.ActiveReservationsAt(path) {
		out = append(out, fmt.Sprintf("%s（%s）", r.DisplayName, workingModeLabel(r.Mode)))
	}
	sort.Strings(out)
	return out
}

// workingModeLabel は一覧向けの短い進め方表示（値集合は予約のデータ形式で閉じている）。
func workingModeLabel(mode string) string {
	switch mode {
	case projectstore.ReservationExclusive:
		return "排他"
	case projectstore.ReservationConcurrent:
		return "並行"
	}
	return "進め方不明"
}

// lastModified はプロジェクトフォルダ内で最も新しい更新時刻を返す（一覧の最終更新日時）。
//
// 派生インデックスはまだ無いため、現状は実体の走査で求める。
// `locks/`（同一端末の一時ファイル）は対象外にする。
func lastModified(root string) time.Time {
	var newest time.Time
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr == nil {
			rel = filepath.ToSlash(rel)
			if rel == "locks" || strings.HasPrefix(rel, "locks/") {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest
}

// ChooseFolder は OS のフォルダ選択ダイアログを開く（新規作成・復元の保存先指定）。
func (a *API) ChooseFolder(title string) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("フォルダ選択を開けません。アプリを再起動してください。")
	}
	return wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{Title: title})
}

// CreateProject はプロジェクトを作成して一覧へ加える（作成者がオーナーになる）。
func (a *API) CreateProject(req CreateProjectRequest) (ProjectSummary, error) {
	settings, err := a.settings()
	if err != nil {
		return ProjectSummary{}, err
	}
	author, ok := settings.Author()
	if !ok {
		return ProjectSummary{}, fmt.Errorf("メールアドレス（利用者 ID）が未登録です。設定でメールアドレスを登録してください。")
	}
	if strings.TrimSpace(req.Path) == "" {
		return ProjectSummary{}, fmt.Errorf("保存先のフォルダを選んでください。")
	}
	if err := validateDomainPresets(req.DomainPresets); err != nil {
		return ProjectSummary{}, err
	}

	// 質問票返送の復号鍵はプロジェクト作成時に生成する。
	keyPair, err := exchange.GenerateKeyPair()
	if err != nil {
		return ProjectSummary{}, err
	}
	exchangeKeys, err := projectstore.NewExchangeKeys(keyPair.Public, keyPair.Private)
	if err != nil {
		return ProjectSummary{}, err
	}

	// フォルダ名はアプリが決める（`<対象システム名>.reqweave`）。
	// 利用者が選ぶのは**保存先**であり、その下に本システムが作る。
	root, err := projectstore.UniqueProjectPath(req.Path, strings.TrimSpace(req.TargetSystemName))
	if err != nil {
		return ProjectSummary{}, err
	}

	store, err := projectstore.CreateProject(root, projectstore.CreateOptions{
		TargetSystemName: strings.TrimSpace(req.TargetSystemName),
		Summary:          req.Summary,
		DomainPresets:    req.DomainPresets,
		Author:           author,
		ExchangeKeys:     exchangeKeys,
	})
	if err != nil {
		return ProjectSummary{}, err
	}
	defer store.Close()

	settings.AddRecentProject(root, 0)
	if err := projectstore.SaveSettings(a.paths, settings); err != nil {
		return ProjectSummary{}, err
	}
	return a.summarize(store.Root(), author), nil
}

// AddExistingProject は既存のプロジェクトフォルダをこの端末の一覧へ加える
// （共有フォルダにある既存のプロジェクトを開く経路）。
//
// 一覧（`recent_projects`）は**端末ごとのアプリ設定**が保持するため、
// オーナーが共有フォルダにプロジェクトを作りメンバーを登録しても、他の端末の一覧には現れない。
// 本メソッドがその経路を与える（メンバーが共同プロジェクトに参加するときの前提）。
//
// **開けるかどうかの判定（メンバー照合・権限）はここでは行わない**。
// 一覧へ加えたうえで、summarize が返す注意書きで状態を示す（判定はプロジェクトを開くときの権限判定が持つ）。
func (a *API) AddExistingProject(path string) (ProjectSummary, error) {
	settings, err := a.settings()
	if err != nil {
		return ProjectSummary{}, err
	}
	if strings.TrimSpace(path) == "" {
		return ProjectSummary{}, fmt.Errorf("開くプロジェクトのフォルダを選んでください。")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ProjectSummary{}, fmt.Errorf("選んだフォルダの場所を特定できません。別のフォルダを選び直してください。")
	}
	// プロジェクトデータであることを確認してから一覧へ加える
	//（不正なフォルダを一覧へ残さない）。
	if err := validateProjectFolder(abs); err != nil {
		return ProjectSummary{}, err
	}

	settings.AddRecentProject(abs, 0)
	if err := projectstore.SaveSettings(a.paths, settings); err != nil {
		return ProjectSummary{}, err
	}
	author, _ := settings.Author()
	return a.summarize(abs, author), nil
}

// validateProjectFolder は指定フォルダがプロジェクトデータかを確かめる。
// 文言はエラーカタログ「データ系」の様式（原因＋次に取る行動）に従う。
func validateProjectFolder(path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("選んだ場所がフォルダではありません。プロジェクトのフォルダを選んでください。")
	}
	data, err := os.ReadFile(filepath.Join(path, projectstore.FileProject))
	if err != nil {
		return fmt.Errorf("選んだフォルダはプロジェクトのフォルダではありません。%s があるフォルダを選んでください。",
			projectstore.FileProject)
	}
	if _, err := projectstore.UnmarshalProject(data); err != nil {
		return fmt.Errorf("選んだフォルダのプロジェクトデータを読み取れません。フォルダの場所と共有フォルダの接続を確認してください。")
	}
	return nil
}

func validateDomainPresets(ids []string) error {
	options, err := domainPresetOptions()
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, p := range options {
		known[p.ID] = true
	}
	for _, id := range ids {
		if !known[id] {
			return fmt.Errorf("業務領域の選択が不正です。一覧から選んでください。")
		}
	}
	return nil
}

// SetDomainPresets は既存プロジェクトの業務領域を変更する（作成後に設定で変える経路）。
func (a *API) SetDomainPresets(path string, ids []string) error {
	if err := validateDomainPresets(ids); err != nil {
		return err
	}
	store, err := a.openProject(path)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.UpdateProject(func(p *projectstore.Project) error {
		p.DomainPresets = ids
		return nil
	})
}

// openProject は権限判定つきでプロジェクトを開く（権限判定を個々の画面に分散させず、バインディング前段で行う）。
//
// 自版より古いデータ形式のときは移行手順（自動退避 → 移行 → migrated_from の記録）を
// 実行してから開く。移行前の退避が作れない場合は移行せず失敗させる（Migrate 側の規定）。
func (a *API) openProject(path string) (*projectstore.Store, error) {
	settings, err := a.settings()
	if err != nil {
		return nil, err
	}
	author, ok := settings.Author()
	if !ok {
		return nil, fmt.Errorf("メールアドレス（利用者 ID）が未登録です。設定でメールアドレスを登録してください。")
	}
	members, err := loadMembers(path)
	if err == nil {
		if _, isMember := members.Find(author.AuthorID); !isMember {
			return nil, fmt.Errorf("このプロジェクトのメンバーに登録されていません。オーナーに登録を依頼してください。")
		}
	}
	store, err := projectstore.Open(path, author)
	var needsMigration *projectstore.ErrNeedsMigration
	if errors.As(err, &needsMigration) {
		if _, migrateErr := projectstore.Migrate(a.paths, path, time.Now()); migrateErr != nil {
			return nil, migrateErr
		}
		store, err = projectstore.Open(path, author)
	}
	if store != nil {
		// 別ウィンドウの処理中でロックを取れなかったことを動作ログへ残す（共同作業系）。
		store.SetLockDeniedHandler(func(target string) {
			a.recordCollabDenied(target, collabKindBusy, "")
		})
	}
	if err != nil {
		// 動作ログ（データ系）。**絶対パスは残さない**（プロジェクト内の相対パスだけ）。
		a.recordProjectDataFailure(projectstore.FileProject, projectDataKind(err), "")
	}
	return store, err
}

// projectDataKind はプロジェクトを開けなかった理由を記録用の区分へ写す。
func projectDataKind(err error) string {
	var tooNew *projectstore.ErrTooNew
	var needsMigration *projectstore.ErrNeedsMigration
	switch {
	case errors.As(err, &tooNew):
		return dataKindTooNew
	case errors.As(err, &needsMigration):
		return dataKindMigration
	case errors.Is(err, os.ErrNotExist):
		return dataKindUnreadable
	default:
		return dataKindMalformed
	}
}

// DeletePreview は削除前に提示する内容を返す（オーナーのみ・別のウィンドウが処理中は不可）。
func (a *API) DeletePreview(path string) (DeletePreview, error) {
	settings, err := a.settings()
	if err != nil {
		return DeletePreview{}, err
	}
	author, _ := settings.Author()
	summary := a.summarize(path, author)
	preview := DeletePreview{
		Path:             path,
		TargetSystemName: summary.TargetSystemName,
		HasDocuments:     hasDocuments(path),
	}
	if !summary.Available {
		preview.Notice = summary.Notice
		return preview, nil
	}
	gens, err := projectstore.ListBackupGenerations(a.paths, summary.ProjectID)
	if err == nil {
		preview.BackupCount = len(gens)
	}
	if summary.Role != "" && summary.Role != projectstore.RoleOwner {
		preview.Notice = "削除できるのはオーナーだけです。オーナーに依頼してください。"
		return preview, nil
	}
	if projectstore.AnyLockHeld(path) {
		preview.Notice = "このプロジェクトを開いている別のウィンドウが処理中のため削除できません。処理の終了後に実行してください。"
		return preview, nil
	}
	preview.Deletable = true
	return preview, nil
}

// DeleteProject はプロジェクトフォルダを削除する（DeletePreview の確認を経て呼ぶ）。
//
// 自動退避は削除しない。削除後も直近世代から復元できることを
// DeletePreview の BackupCount で利用者へ示す。
func (a *API) DeleteProject(path string) error {
	preview, err := a.DeletePreview(path)
	if err != nil {
		return err
	}
	if !preview.Deletable {
		if preview.Notice != "" {
			return fmt.Errorf("%s", preview.Notice)
		}
		return fmt.Errorf("このプロジェクトは削除できません。")
	}
	if !projectstore.IsProjectFolder(path) {
		return fmt.Errorf("プロジェクトフォルダではありません。対象を確認してください。")
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("プロジェクトを削除できません。フォルダが使用中でないか確認してください。")
	}
	return a.forgetProject(path)
}

// forgetProject は一覧（recent_projects）から取り除く。
func (a *API) forgetProject(path string) error {
	settings, err := a.settings()
	if err != nil {
		return err
	}
	settings.RemoveRecentProject(path)
	return projectstore.SaveSettings(a.paths, settings)
}

// hasDocuments は成果物ドキュメントの有無を返す（削除確認の提示内容）。
func hasDocuments(root string) bool {
	found := false
	_ = filepath.WalkDir(filepath.Join(root, "documents"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// ChooseProject はプロジェクトを選ぶダイアログを開く。
//
// **macOS と Windows で経路が分かれる**。macOS ではプロジェクトフォルダをパッケージとして
// 1 個のファイルに見せているため、フォルダ選択では中へ入ってしまい選べない。
// ファイル選択にして「パッケージを展開しない」ことで、1 個の対象として選ばせる。
// Windows はフォルダのままなので従来どおりフォルダ選択を使う。
func (a *API) ChooseProject(title string) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("プロジェクトを選べません。アプリを再起動してください。")
	}
	if runtime.GOOS != "darwin" {
		return wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{Title: title})
	}
	return wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: title,
		// パッケージを 1 個の選択対象として出す（中へ降りさせない）。
		TreatPackagesAsDirectories: false,
		Filters: []wailsruntime.FileFilter{{
			DisplayName: "ReqWeave プロジェクト",
			Pattern:     "*" + projectstore.ProjectFolderExt,
		}},
	})
}

// ProjectFolderNameFor は、その対象システム名で作られるフォルダ名を返す。
//
// 名前の安全化・長さの上限は projectstore が持つ規則であり、**画面側で組み立て直さない**
// （同じ規則が 2 か所にあると、片方だけ直して食い違う）。作成前の確認表示に使う。
func (a *API) ProjectFolderNameFor(targetSystemName string) string {
	return projectstore.ProjectFolderName(strings.TrimSpace(targetSystemName))
}

// LocationWarning は保存先について注意することがあれば 1 文で返す。
//
// いまのところクラウド同期のフォルダの検知のみ。**作業は止めない**（当て推量のため）。
// 画面は返ってきた文言をそのまま出す（画面側で判定をやり直さない）。
func (a *API) LocationWarning(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return projectstore.CloudSyncWarning(path)
}

// rememberOpenedProject は開いたプロジェクトを一覧へ載せる。
//
// 一覧を経由しない入口（パッケージのダブルクリック）から開いたときも
// 「最近開いたもの」に入るようにする。**失敗しても開く操作は妨げない**
// （一覧は端末ごとの利便であり、プロジェクトデータではない）。
func (a *API) rememberOpenedProject(path string) {
	settings, err := a.settings()
	if err != nil {
		return
	}
	settings.AddRecentProject(path, 0)
	_ = projectstore.SaveSettings(a.paths, settings)
}
