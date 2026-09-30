package codex

// 本ファイルは利用者ホームの skill の一覧を送らせないための無効化。
//
// 設定では止められない（`skills.enabled=false` もフォルダ単位の指定も効かない。`HOME` を
// 差し替えると macOS で keyring が使えなくなる。いずれも実測）。そこで **1 件ずつ無効にする**。
// 起動後と、**毎回の `thread/start` の直前**に行う（本システムの実行中に利用者が skill を
// 足した場合に備えるため）。0 件にならなければ呼び出しを行わない（fail-closed）。
//
// skill の中身は Codex が手元で読むだけで送られない。要件が禁じるのは一覧（名前・説明・パス）の送信である。
// 無効化の設定は一時領域の `config.toml` に書かれ、終了時に消える（利用者の `~/.codex` と `~/.agents` は変えない）。

import (
	"context"
	"encoding/json"
	"fmt"
)

// disableSkills は有効な skill を 1 件ずつ無効にし、0 件になったことを確かめる。
func (p *process) disableSkills(ctx context.Context) error {
	enabled, err := p.enabledSkillPaths(ctx)
	if err != nil {
		return err
	}
	if len(enabled) == 0 {
		return nil
	}
	for _, path := range enabled {
		if _, err := p.rpc.call(ctx, "skills/config/write", map[string]any{
			"path": path, "enabled": false,
		}); err != nil {
			return p.startupFailure(err)
		}
	}
	remaining, err := p.enabledSkillPaths(ctx)
	if err != nil {
		return err
	}
	if len(remaining) > 0 {
		// パスは利用者のホームを含むため件数だけを示す。
		return p.guardFailure(fmt.Sprintf("利用者の skill を無効にできません（%d 件が有効のまま）", len(remaining)))
	}
	return nil
}

// enabledSkillPaths は有効な skill のパスを返す（`forceReload: true` で毎回読み直す）。
func (p *process) enabledSkillPaths(ctx context.Context) ([]string, error) {
	raw, err := p.rpc.call(ctx, "skills/list", map[string]any{
		"cwds": []string{p.cfg.workDir}, "forceReload": true,
	})
	if err != nil {
		return nil, p.startupFailure(err)
	}
	var result struct {
		Data []struct {
			Skills []struct {
				Path    string `json:"path"`
				Enabled bool   `json:"enabled"`
			} `json:"skills"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, p.guardFailure("Codex の応答を解釈できません")
	}
	var paths []string
	for _, entry := range result.Data {
		for _, skill := range entry.Skills {
			if skill.Enabled && skill.Path != "" {
				paths = append(paths, skill.Path)
			}
		}
	}
	return paths, nil
}
