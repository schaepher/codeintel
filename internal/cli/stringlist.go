package cli

import "strings"

// stringList 可重复 flag（如 `--ignore-dir a --ignore-dir b`）。
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

// Set 实现 flag.Value（每次出现追加一个值）。
func (s *stringList) Set(v string) error {
	if strings.TrimSpace(v) != "" {
		*s = append(*s, v)
	}
	return nil
}
