package binding

// 本ファイルは回答モード（ステークホルダーが受け渡しファイルを開いて回答する）のバインディング。
//
// 起動経路で動作モードが決まる（受け渡しファイルを開く = 回答モード）。回答モードは
// 担当者モードの機能を一切呼ばず、初期設定・シークレットキーなしで完結する。
// パスコード・導出鍵は画面へ返さず、保存もしない（端末に残さず、画面経由で漏れないようにする）。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/exchange"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// respondState は回答モードで開いている質問票（同時に 1 件のみ）。
type respondState struct {
	mu      sync.Mutex
	session *exchange.RespondSession
	// AI 対話回答（任意機能）の状態。既定は無効で、
	// 送信範囲の事前表示を確認したときだけ有効になる（回答者が知らないうちに外部の AI へ送らない）。
	aiEnabled    bool
	aiProvider   string
	aiAuthMethod string
	// aiModel は用いるモデル（プロバイダの推奨 → 既知一覧の既定の順で決める）。
	// **画面へは出さない**（回答者にモデルを選ばせない）。解決は 1 回だけ行い、以後は使い回す。
	aiModel aiprovider.ModelInfo
	// aiRunning / aiCancel は進行中の応答（同時に 1 件。中断は担当者モードの対話と同じ方式）。
	aiRunning bool
	aiCancel  context.CancelFunc
}

// StartupMode は起動時の動作モード。
type StartupMode struct {
	// Mode は owner（担当者モード）/ respondent（回答モード）。
	Mode string `json:"mode"`
	// FilePath は回答モードで開く受け渡しファイル（回答モードのときのみ）。
	FilePath string `json:"filePath,omitempty"`
	// FileName は画面表示用のファイル名（パス全体を出さない）。
	FileName string `json:"fileName,omitempty"`
}

// 起動モードの値（担当者モード・回答モードの 2 経路）。
const (
	ModeOwner      = "owner"
	ModeRespondent = "respondent"
)

// StartupMode は起動経路から動作モードを決める。
//
// 受け渡しファイル（.rwvq）を受け取って起動した場合のみ回答モードとする。
// 返送ファイル（.rwva）は担当者側の取込対象であり、回答モードでは開かない。
//
// 受け取り経路は 2 つある（起動引数と open-file イベント）。macOS の Finder のダブルクリックは
// 起動引数を伴わず open-file イベントで届くため、**引数だけに依存しない**。
func (a *API) StartupMode() StartupMode {
	if ev := a.peekOpenFile(); ev != nil && ev.Kind == OpenFileRespond {
		return StartupMode{Mode: ModeRespondent, FilePath: ev.FilePath, FileName: ev.FileName}
	}
	for _, arg := range os.Args[1:] {
		if strings.HasSuffix(strings.ToLower(arg), exchange.ExtIssue) {
			return StartupMode{Mode: ModeRespondent, FilePath: arg, FileName: filepath.Base(arg)}
		}
	}
	return StartupMode{Mode: ModeOwner}
}

// RespondQuestionView は回答画面に出す質問 1 件。
type RespondQuestionView struct {
	ID           string   `json:"id"`
	Text         string   `json:"text"`
	Background   string   `json:"background"`
	AnswerFormat string   `json:"answerFormat"`
	Choices      []string `json:"choices,omitempty"`
}

// RespondAnswerView は入力済みの回答 1 件（再開時の復元に使う）。
type RespondAnswerView struct {
	QuestionID string   `json:"questionId"`
	Kind       string   `json:"kind"`
	Selected   []string `json:"selected,omitempty"`
	FreeText   string   `json:"freeText,omitempty"`
	// Body は「不明」のときの理由・確認先。
	Body string `json:"body,omitempty"`
}

// RespondTermView は同梱された用語（質問の解釈に必要な範囲）。
type RespondTermView struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}

// RespondOpenResult はパスコード一致後に画面へ渡す内容（パスコード入力 → 回答画面）。
//
// パスコード・導出鍵・返送先の鍵は含めない（鍵を画面へ出さない）。
type RespondOpenResult struct {
	QuestionnaireID string                `json:"questionnaireId"`
	Addressee       string                `json:"addressee"`
	Questions       []RespondQuestionView `json:"questions"`
	Answers         []RespondAnswerView   `json:"answers,omitempty"`
	Terms           []RespondTermView     `json:"terms,omitempty"`
	// Status は answering（回答中）/ answered（確定済み）。
	Status string `json:"status"`
	// Restored は前回の入力を復元したか（「続きから再開」の表示に使う）。
	Restored bool `json:"restored"`
	// Notice は画面へ出す案内（再発行で引き継がれない旨など）。無ければ空。
	Notice string `json:"notice,omitempty"`
}

// OpenQuestionnaireFile はパスコードで受け渡しファイルを開く。
//
// パスコードが一致しない場合は内容を一切返さず、担当者への連絡案内を返す。
// 試行したパスコードはどこにも記録しない（ログ等から漏れないようにする）。
func (a *API) OpenQuestionnaireFile(src, passcode string) (RespondOpenResult, error) {
	paths, err := a.appPaths()
	if err != nil {
		return RespondOpenResult{}, err
	}
	session, err := exchange.NewWorkspace(paths).Open(src, passcode)
	if err != nil {
		// 動作ログ（受け渡しの失敗。**試したパスコード・ファイル名は残さない**）。
		a.recordExchangeFailure(exchangeOpOpenIssue, src, err)
		return RespondOpenResult{}, err
	}
	a.respond.mu.Lock()
	a.respond.session = session
	a.respond.mu.Unlock()
	return respondOpenResult(session), nil
}

func respondOpenResult(s *exchange.RespondSession) RespondOpenResult {
	out := RespondOpenResult{
		QuestionnaireID: s.Questionnaire.ID,
		Addressee:       s.Questionnaire.Addressee,
		Status:          s.Status,
		Restored:        s.Restored,
		Notice:          s.Notice,
	}
	for _, q := range s.Questionnaire.Questions {
		out.Questions = append(out.Questions, RespondQuestionView{
			ID: q.ID, Text: q.Text, Background: q.Background,
			AnswerFormat: q.AnswerFormat, Choices: q.Choices,
		})
	}
	for _, ans := range s.Answers.Answers {
		out.Answers = append(out.Answers, RespondAnswerView{
			QuestionID: ans.QuestionID, Kind: ans.Kind,
			Selected: ans.Selected, FreeText: ans.FreeText, Body: ans.Body,
		})
	}
	for _, t := range s.Terms {
		out.Terms = append(out.Terms, RespondTermView{Name: t.Name, Definition: t.Definition})
	}
	return out
}

// AnswerInput は 1 問の回答（画面からの入力）。
type AnswerInput struct {
	QuestionID string   `json:"questionId"`
	Kind       string   `json:"kind"`
	Selected   []string `json:"selected,omitempty"`
	FreeText   string   `json:"freeText,omitempty"`
	// Body は「不明」のときの理由・確認先（未記入でもよい）。
	Body string `json:"body,omitempty"`
}

// RespondProgressView は保存後の進捗（回答画面の進捗表示・確定画面の確定可否）。
type RespondProgressView struct {
	Answered int `json:"answered"`
	Total    int `json:"total"`
	// UnansweredIDs は未回答の質問 ID（確定できない理由の提示に使う）。
	UnansweredIDs []string `json:"unansweredIds,omitempty"`
}

// SaveAnswer は 1 問の回答を保存する（入力・変更のたびに呼ぶ一時保存。途中で閉じても続きから再開できる）。
func (a *API) SaveAnswer(in AnswerInput) (RespondProgressView, error) {
	session, err := a.respondSession()
	if err != nil {
		return RespondProgressView{}, err
	}
	if err := session.SetAnswer(projectstore.Answer{
		QuestionID: in.QuestionID, Kind: in.Kind,
		Selected: in.Selected, FreeText: in.FreeText, Body: in.Body,
	}); err != nil {
		return RespondProgressView{}, err
	}
	return respondProgress(session), nil
}

// RespondProgress は現在の進捗を返す（画面の再表示用）。
func (a *API) RespondProgress() (RespondProgressView, error) {
	session, err := a.respondSession()
	if err != nil {
		return RespondProgressView{}, err
	}
	return respondProgress(session), nil
}

func respondProgress(s *exchange.RespondSession) RespondProgressView {
	unanswered := s.Unanswered()
	return RespondProgressView{
		Answered:      len(s.Questionnaire.Questions) - len(unanswered),
		Total:         len(s.Questionnaire.Questions),
		UnansweredIDs: unanswered,
	}
}

// RespondExportView は返送ファイルの出力結果。
type RespondExportView struct {
	Path string `json:"path"`
	// Notice は返送手順の案内（利用者が次に何をするかを書く）。
	Notice string `json:"notice"`
}

// returnNotice は出力後に必ず出す返送手順の案内。
const returnNotice = "このファイルを担当者へ返送してください。メールや共有フォルダなど、" +
	"ふだん使っている方法で構いません。返送ファイルの中身は担当者だけが読めます。"

// ChooseReturnDestination は返送ファイルの保存先を選ぶ。
func (a *API) ChooseReturnDestination() (string, error) {
	session, err := a.respondSession()
	if err != nil {
		return "", err
	}
	if a.ctx == nil {
		return "", fmt.Errorf("保存先の選択を開けません。アプリを再起動してください。")
	}
	return wailsruntime.SaveFileDialog(a.ctx, wailsruntime.SaveDialogOptions{
		Title:           "返送ファイルの保存先を選ぶ",
		DefaultFilename: session.SuggestedReturnName(),
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "返送ファイル (*" + exchange.ExtReturn + ")", Pattern: "*" + exchange.ExtReturn},
		},
	})
}

// FinalizeAnswers は回答を確定して返送ファイルを出力する。
//
// 未回答が残る間は出力しない。確定済みの場合は同じ内容を再出力する（送付失敗時の再送付）。
func (a *API) FinalizeAnswers(dst string) (RespondExportView, error) {
	session, err := a.respondSession()
	if err != nil {
		return RespondExportView{}, err
	}
	if strings.TrimSpace(dst) == "" {
		return RespondExportView{}, errors.New("保存先を選んでください。")
	}
	if session.Status == exchange.RespondAnswered {
		if err := session.Export(dst); err != nil {
			return RespondExportView{}, err
		}
		return RespondExportView{Path: dst, Notice: returnNotice}, nil
	}
	if err := session.Finalize(dst); err != nil {
		return RespondExportView{}, err
	}
	return RespondExportView{Path: dst, Notice: returnNotice}, nil
}

// respondSession は開いている回答作業を返す。
func (a *API) respondSession() (*exchange.RespondSession, error) {
	a.respond.mu.Lock()
	defer a.respond.mu.Unlock()
	if a.respond.session == nil {
		return nil, errors.New("質問票が開かれていません。届いた質問票ファイルを開き直してください。")
	}
	return a.respond.session, nil
}

// appPaths はアプリ設定領域を返す（回答モードでも設定ファイルは読まない）。
func (a *API) appPaths() (projectstore.AppPaths, error) {
	if a.pathsErr != nil {
		return projectstore.AppPaths{}, fmt.Errorf("作業データの保存先を用意できません。アプリを再起動してください。")
	}
	return a.paths, nil
}
