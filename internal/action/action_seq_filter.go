package action

import (
	"regexp"
	"strings"
)

// seqFilterHit 检查调用是否命中过滤（任一维度命中即过滤）。
func seqFilterHit(f SeqFilter, tid, file, label string) bool {
	if len(f.Files) > 0 {
		for _, ff := range f.Files {
			if file == ff || strings.HasSuffix(file, "/"+ff) {
				return true
			}
		}
	}
	if len(f.Fns) > 0 {
		for _, fn := range f.Fns {
			if label == fn || strings.HasSuffix(label, "."+fn) {
				return true
			}
		}
	}
	if len(f.Pkgs) > 0 && tid != "" {
		pkg := pkgOfEntityID(tid)
		short := pkg
		if i := strings.LastIndex(short, "/"); i >= 0 {
			short = short[i+1:]
		}
		for _, p := range f.Pkgs {
			if pkg == p || short == p || strings.HasSuffix(pkg, "/"+p) {
				return true
			}
		}
	}
	if len(f.Regex) > 0 {
		for _, re := range f.Regex {
			if ok, err := regexp.MatchString(re, label); err == nil && ok {
				return true
			}
		}
	}
	return false
}
