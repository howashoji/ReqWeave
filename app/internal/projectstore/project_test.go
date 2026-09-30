package projectstore

import (
	"strings"
	"testing"
	"time"
)

const validProjectYAML = `format_version: "1.4"
project_id: 3f2a1c8e-9d4b-4f6a-8c2e-7b1d5a9f0e33
target_system_name: 在庫管理システム
phase: requirements
created_at: 2026-08-27T05:00:00Z
`

func TestUnmarshalProject(t *testing.T) {
	p, err := UnmarshalProject([]byte(validProjectYAML))
	if err != nil {
		t.Fatalf("解釈に失敗: %v", err)
	}
	if p.FormatVersion != CurrentFormatVersion {
		t.Errorf("format_version: %q", p.FormatVersion)
	}
	if p.TargetSystemName != "在庫管理システム" {
		t.Errorf("target_system_name: %q", p.TargetSystemName)
	}
	if p.Phase != PhaseRequirements {
		t.Errorf("phase: %q", p.Phase)
	}
	if !p.CreatedAt.Equal(time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)) {
		t.Errorf("created_at: %v", p.CreatedAt)
	}
	if p.UsageLimit != nil {
		t.Errorf("usage_limit が無いのに読み込まれた: %+v", p.UsageLimit)
	}
}

// usage_limit のキーが無い = 上限未設定。ある場合は tokens_max が必須。
func TestUnmarshalProjectUsageLimit(t *testing.T) {
	y := validProjectYAML + "usage_limit:\n  tokens_max: 1000000\n  warn_ratio: 0.7\n"
	p, err := UnmarshalProject([]byte(y))
	if err != nil {
		t.Fatalf("解釈に失敗: %v", err)
	}
	if p.UsageLimit == nil || p.UsageLimit.TokensMax != 1000000 {
		t.Fatalf("usage_limit が読めていない: %+v", p.UsageLimit)
	}
	if p.UsageLimit.WarnRatio == nil || *p.UsageLimit.WarnRatio != 0.7 {
		t.Errorf("warn_ratio が読めていない: %+v", p.UsageLimit.WarnRatio)
	}
}

func TestUnmarshalProjectRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"format_version が無い": strings.Replace(validProjectYAML, `format_version: "1.4"`, "", 1),
		"project_id が UUID でない": strings.Replace(validProjectYAML,
			"3f2a1c8e-9d4b-4f6a-8c2e-7b1d5a9f0e33", "not-a-uuid", 1),
		"project_id が UUIDv4 でない": strings.Replace(validProjectYAML,
			"3f2a1c8e-9d4b-4f6a-8c2e-7b1d5a9f0e33", "f47ac10b-58cc-11e4-8000-0800200c9a66", 1),
		"target_system_name が空": strings.Replace(validProjectYAML, "target_system_name: 在庫管理システム", `target_system_name: ""`, 1),
		"phase が値集合外":           strings.Replace(validProjectYAML, "phase: requirements", "phase: design", 1),
		"created_at が無い":        strings.Replace(validProjectYAML, "created_at: 2026-08-27T05:00:00Z\n", "", 1),
		"tokens_max が 0":        validProjectYAML + "usage_limit:\n  tokens_max: 0\n",
	}
	for name, y := range cases {
		t.Run(name, func(t *testing.T) {
			if p, err := UnmarshalProject([]byte(y)); err == nil {
				t.Errorf("不正な project.yaml が受理された: %+v", p)
			}
		})
	}
}

// 自版より新しい minor の未知フィールドを、書き戻しで削除しない。
func TestUnknownFieldsSurviveRoundTrip(t *testing.T) {
	y := strings.Replace(validProjectYAML, `format_version: "1.4"`, `format_version: "1.5"`, 1) +
		"future_flag: true\nfuture_map:\n  nested: 値\n"
	p, err := UnmarshalProject([]byte(y))
	if err != nil {
		t.Fatalf("解釈に失敗: %v", err)
	}
	compat, err := p.FormatCompatibility()
	if err != nil {
		t.Fatalf("互換判定に失敗: %v", err)
	}
	if compat != CompatNewerMinor {
		t.Fatalf("互換判定が違う: got %v, want CompatNewerMinor", compat)
	}

	p.TargetSystemName = "在庫管理システム（改）"
	out, err := p.Marshal()
	if err != nil {
		t.Fatalf("組み立てに失敗: %v", err)
	}
	s := string(out)
	for _, want := range []string{"future_flag: true", "nested: 値", "在庫管理システム（改）"} {
		if !strings.Contains(s, want) {
			t.Errorf("書き戻しに %q がありません:\n%s", want, s)
		}
	}

	// 再読込しても未知フィールドが残る
	again, err := UnmarshalProject(out)
	if err != nil {
		t.Fatalf("再解釈に失敗: %v", err)
	}
	if len(again.unknown) != 2 {
		t.Errorf("未知フィールドの数が違う: %v", again.unknown)
	}
}

// 日時は UTC の ISO 8601 で保持する。
func TestMarshalWritesUTCTimestamp(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	p := &Project{
		FormatVersion:    CurrentFormatVersion,
		ProjectID:        "3f2a1c8e-9d4b-4f6a-8c2e-7b1d5a9f0e33",
		TargetSystemName: "在庫管理システム",
		Phase:            PhaseRequirements,
		CreatedAt:        time.Date(2026, 8, 27, 9, 0, 0, 0, jst).UTC(),
	}
	out, err := p.Marshal()
	if err != nil {
		t.Fatalf("組み立てに失敗: %v", err)
	}
	if !strings.Contains(string(out), "created_at: 2026-08-27T00:00:00Z") {
		t.Errorf("UTC の ISO 8601 で書かれていない:\n%s", out)
	}
}

// 概要は任意フィールド。未入力なら書き出しに現れない。
func TestProjectSummaryIsOptional(t *testing.T) {
	p, err := UnmarshalProject([]byte(validProjectYAML))
	if err != nil {
		t.Fatal(err)
	}
	if p.Summary != "" {
		t.Errorf("未指定の概要が入っている: %q", p.Summary)
	}
	out, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "summary") {
		t.Errorf("未入力の概要がキーごと書き出された:\n%s", out)
	}

	p.Summary = "受発注から出荷までを扱う社内システム"
	out, err = p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	again, err := UnmarshalProject(out)
	if err != nil {
		t.Fatal(err)
	}
	if again.Summary != p.Summary {
		t.Errorf("概要が往復しない: %q", again.Summary)
	}
}

// 曖昧語リストの調整を保持し、書き戻しでも失われない。
func TestProjectAmbiguousTerms(t *testing.T) {
	y := strings.Replace(validProjectYAML, "phase: requirements",
		"phase: requirements\nambiguous_terms:\n  added: [随時対応]\n  excluded: [随時]", 1)
	p, err := UnmarshalProject([]byte(y))
	if err != nil {
		t.Fatalf("解釈に失敗: %v", err)
	}
	if p.AmbiguousTerms == nil || len(p.AmbiguousTerms.Added) != 1 || len(p.AmbiguousTerms.Excluded) != 1 {
		t.Fatalf("曖昧語の調整が読めていない: %+v", p.AmbiguousTerms)
	}
	out, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "随時対応") || !strings.Contains(string(out), "excluded") {
		t.Errorf("書き戻しで調整が失われた:\n%s", out)
	}

	// 未設定のときはキーごと出力しない（調整なし = 初期リストのみ）。
	plain, err := UnmarshalProject([]byte(validProjectYAML))
	if err != nil {
		t.Fatal(err)
	}
	if plain.AmbiguousTerms != nil {
		t.Errorf("未設定なのに値が入っている: %+v", plain.AmbiguousTerms)
	}
	out, _ = plain.Marshal()
	if strings.Contains(string(out), "ambiguous_terms") {
		t.Errorf("未設定でキーが出力されている:\n%s", out)
	}
}

// sync は任意。種別の値集合・所在の非空・認証情報を含まないことを検証し、書き戻しで失われない。
func TestProjectSyncSetting(t *testing.T) {
	p := &Project{FormatVersion: CurrentFormatVersion, ProjectID: newProjectID(), TargetSystemName: "x",
		Phase: PhaseRequirements, CreatedAt: time.Now()}
	if err := p.Validate(); err != nil {
		t.Fatalf("sync 未設定が不正と判定された: %v", err)
	}
	p.Sync = &SyncSetting{Kind: SyncKindFolder, Location: "/Volumes/share/proj.git"}
	out, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "sync:") || !strings.Contains(string(out), "kind: folder") {
		t.Errorf("sync が書き出されない:\n%s", out)
	}
	back, err := UnmarshalProject(out)
	if err != nil {
		t.Fatal(err)
	}
	if back.Sync == nil || back.Sync.Location != "/Volumes/share/proj.git" {
		t.Errorf("読み戻した sync が違う: %+v", back.Sync)
	}
	if c := back.clone(); c.Sync == back.Sync {
		t.Error("clone が sync を共有している")
	}
	for name, s := range map[string]*SyncSetting{
		"種別が値集合外": {Kind: "svn", Location: "/x"},
		"所在が空":    {Kind: SyncKindFolder, Location: " "},
		"所在に認証情報": {Kind: SyncKindGitInternal, Location: "https://alice:pw@git.example.co.jp/x.git"},
	} {
		p.Sync = s
		if err := p.Validate(); err == nil {
			t.Errorf("%s が受理された", name)
		}
	}
}
