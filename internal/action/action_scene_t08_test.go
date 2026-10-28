package action

// T08：静态场景常量/字面量/转换的边界矩阵（十六进制、字符、iota/别名常量、
// 遮蔽、越界转换、布尔求值顺序）。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// sceneCondOf 解析 `if <cond> { }` 里的条件表达式。
func sceneCondOf(t *testing.T, src string) ast.Expr {
	t.Helper()
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, "x.go", "package p\nfunc f(){\nif "+src+" { }\n}\n", 0)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	fn := f.Decls[0].(*ast.FuncDecl)
	ifStmt := fn.Body.List[0].(*ast.IfStmt)
	return ifStmt.Cond
}

func sceneFitOf(t *testing.T, sceneVal, cond string, consts sceneConstLookup) SceneFit {
	t.Helper()
	s := NewScene([]string{"sourceType=" + sceneVal})
	if consts == nil {
		consts = sceneConstsMap{}
	}
	return s.matchCondSrc(sceneCondOf(t, cond), map[string]string{}, nil, consts)
}

func TestSceneNumericLiteralNormalization(t *testing.T) {
	cases := []struct {
		cond   string
		status string
		lit    string
	}{
		{"sourceType==0x4", SceneTaken, "4"},
		{"sourceType==26", SceneExcluded, "26"},
		{"sourceType==0b100", SceneTaken, "4"},
		{"sourceType==0o4", SceneTaken, "4"},
		{"sourceType==1_0", SceneExcluded, "10"},
	}
	for _, c := range cases {
		fit := sceneFitOf(t, "4", c.cond, nil)
		if fit.Status != c.status {
			t.Errorf("%q status = %s, want %s (%+v)", c.cond, fit.Status, c.status, fit)
		}
		if fit.Literal != c.lit {
			t.Errorf("%q literal = %q, want %q（归一化显示）", c.cond, fit.Literal, c.lit)
		}
	}
}

func TestSceneCharLiteralComparesAsRune(t *testing.T) {
	// 'a' == 97
	fit := sceneFitOf(t, "97", "sourceType=='a'", nil)
	if fit.Status != SceneTaken || fit.Literal != "97" {
		t.Fatalf("字符字面量应按 rune 数值比较: %+v", fit)
	}
}

func TestSceneConstIotaAlias(t *testing.T) {
	consts := sceneConstsMap{
		"SourceA": "4",
		"SourceB": "5",
		"Alias":   "4",
	}
	for _, c := range []struct {
		cond   string
		status string
	}{
		{"sourceType==SourceA", SceneTaken},
		{"sourceType==SourceB", SceneExcluded},
		{"sourceType==Alias", SceneTaken},
		{"sourceType!=SourceB && sourceType!=SourceA", SceneExcluded},
	} {
		fit := sceneFitOf(t, "4", c.cond, consts)
		if fit.Status != c.status {
			t.Errorf("%q status = %s, want %s (%+v)", c.cond, fit.Status, c.status, fit)
		}
	}
}

// 遮蔽的枚举名/动态变量/未知来源：保持 unknown（不借同名常量）。
func TestSceneShadowedAndDynamicUnknown(t *testing.T) {
	// 场景键匹配不到字段 → unknown
	if fit := sceneFitOf(t, "4", "other==4", nil); fit.Status != SceneUnknown {
		t.Errorf("无对应场景键应 unknown: %+v", fit)
	}
	// 非字面量对（动态 var 比较）→ unknown
	if fit := sceneFitOf(t, "4", "sourceType==other", nil); fit.Status != SceneUnknown {
		t.Errorf("动态变量比较应 unknown: %+v", fit)
	}
}

// 布尔求值顺序：左侧未知且可能改写右侧读取值 → unknown（不代数化升级）；
// `mutate() || sourceType==4` 与 `unknown || true` 都保留 unknown。
func TestSceneBoolOrderingKeepsUnknown(t *testing.T) {
	// 左侧未知（调用可能改写 sourceType、返回真/假）→ 整体 unknown。
	// 反例：`sourceType==4 || mutate()` 左侧已确定真，Go 短路保证右侧不执行
	// ——taken 是正确的（不属"未知枚举 || 已知真"）。
	cases := []string{
		"mutate() || sourceType==4",
	}
	for _, c := range cases {
		fit := sceneFitOf(t, "4", c, nil)
		if fit.Status != SceneUnknown {
			t.Errorf("%q 应保持 unknown（求值顺序/副作用）: %+v", c, fit)
		}
	}
	// `sourceType!=4 && mutate()`：左侧已 excluded → 整体 excluded 是安全的
	// （短路保证 mutate 不执行）。
	if fit := sceneFitOf(t, "4", "sourceType!=4 && mutate()", nil); fit.Status != SceneExcluded {
		t.Errorf("左侧 excluded 的 && 应 excluded: %+v", fit)
	}
}

// 越界/非法整数转换保持 unknown（不按截断值匹配）。
func TestSceneConversionRangeConservative(t *testing.T) {
	for _, c := range []string{
		"sourceType==int8(256)",
		"sourceType==uint8(-1)",
		"sourceType==int32(4)",
	} {
		fit := sceneFitOf(t, "4", c, nil)
		switch c {
		case "sourceType==int32(4)":
			if fit.Status != SceneTaken {
				t.Errorf("%q 合法转换应可判定: %+v", c, fit)
			}
		default:
			if fit.Status != SceneUnknown {
				t.Errorf("%q 越界转换应 unknown: %+v", c, fit)
			}
		}
	}
}
