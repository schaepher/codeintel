package action

// P1-03（构建流程收口）：仓库级忽略目录配置（<repo>/codeintel.yaml）的
// 读取/归一/写入从 cli 迁 action——init/update/reindex 与变更检测都要用
// 同一份口径（CLI 与 MCP 共用）。cli 只保留命令解析与输出。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const ignoreConfigFile = "codeintel.yaml"

type ignoreConfig struct {
	IgnoreDirs []string `yaml:"ignore_dirs"`
}

// ignoreConfigPath 仓库级配置文件路径。
func IgnoreConfigPath(repoAbs string) string {
	return filepath.Join(repoAbs, ignoreConfigFile)
}

// normalizeIgnoreDir 归一忽略目录：仓库相对路径、去 "./" 与首尾 "/"、
// 反斜杠转正斜杠；拒绝空/仓库根/绝对路径/越出仓库。
func NormalizeIgnoreDir(dir string) (string, error) {
	if filepath.IsAbs(dir) || strings.HasPrefix(strings.TrimSpace(dir), "/") {
		return "", fmt.Errorf("忽略目录必须是仓库相对路径：%s", dir)
	}
	d := strings.TrimSpace(strings.ReplaceAll(dir, "\\", "/"))
	d = strings.TrimPrefix(d, "./")
	d = strings.Trim(d, "/")
	if d == "" || d == "." {
		return "", fmt.Errorf("忽略目录不能为空或仓库根（.）")
	}
	if d == ".." || strings.HasPrefix(d, "../") {
		return "", fmt.Errorf("忽略目录必须在仓库内：%s", dir)
	}
	return d, nil
}

// loadIgnoreDirs 读仓库级配置的 ignore_dirs（无文件/解析失败返回 nil）。
func LoadIgnoreDirs(repoAbs string) []string {
	b, err := os.ReadFile(IgnoreConfigPath(repoAbs))
	if err != nil {
		return nil
	}
	var c ignoreConfig
	if yaml.Unmarshal(b, &c) != nil {
		return nil
	}
	var out []string
	for _, d := range c.IgnoreDirs {
		if n, err := NormalizeIgnoreDir(d); err == nil {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// saveIgnoreDirs 写仓库级配置（无忽略目录时删除文件保持干净）。
func SaveIgnoreDirs(repoAbs string, dirs []string) error {
	if len(dirs) == 0 {
		if err := os.Remove(IgnoreConfigPath(repoAbs)); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	b, err := yaml.Marshal(ignoreConfig{IgnoreDirs: dirs})
	if err != nil {
		return err
	}
	header := "# codeintel 仓库配置（由 `codeintel config ignore-dir add|rm` 管理）\n" +
		"# ignore_dirs：init/update/reindex 不解析这些目录及其子目录中的文件\n"
	return os.WriteFile(IgnoreConfigPath(repoAbs), append([]byte(header), b...), 0o644)
}
