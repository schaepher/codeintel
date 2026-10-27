package action

// todo22 §5 补充测试：整型/有类型常量、内建整数转换、布尔短路与取反参与
// 场景判定（不把枚举名称直接当值；常量按真实声明解析）。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// sceneCond 解析一个条件表达式（用于场景判定单测）。
func sceneCond(t *testing.T, src string) ast.Expr {
	t.Helper()
	fset := token.NewFileSet()
	expr, err := parser.ParseExprFrom(fset, "x.go", src, 0)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return expr
}

func TestSceneIntConstAndConversion(t *testing.T) {
	consts := sceneConstsMap{"SourceA": "7", "SourceB": "19"}
	cases := []struct {
		cond   string
		scene  string
		status string
	}{
		// 内建整数转换 + 常量（§8 的写法）
		{"int32(req.Source) == int32(SourceB)", "19", SceneTaken},
		{"int32(req.Source) == int32(SourceB)", "7", SceneExcluded},
		{"int32(SourceB) == int32(req.Source)", "19", SceneTaken},
		// 常量别名 = 值（不按名称匹配）
		{"req.Source == SourceA", "7", SceneTaken},
		{"req.Source == SourceA", "19", SceneExcluded},
		{"req.Source != SourceA", "19", SceneTaken},
		// 数值比较（场景值是字符串 "7"，常量是整型 7）
		{"req.Source == 7", "7", SceneTaken},
		{"req.Source == 7", "19", SceneExcluded},
	}
	for _, c := range cases {
		sc := NewScene([]string{"Source=" + c.scene})
		fit := sc.matchCondSrc(sceneCond(t, c.cond), nil, nil, consts)
		if fit.Status != c.status {
			t.Errorf("%s @ Source=%s → %s（reason=%s），期望 %s",
				c.cond, c.scene, fit.Status, fit.Reason, c.status)
		}
	}
}

func TestSceneBoolShortCircuit(t *testing.T) {
	consts := sceneConstsMap{"SourceA": "7"}
	sc := NewScene([]string{"Source=19"})
	// 短路：&& 左侧为假 → 整条为假（可排除分支，即使右侧未知）
	fit := sc.matchCondSrc(sceneCond(t, "req.Source == SourceA && IsRegionX()"), nil, nil, consts)
	if fit.Status != SceneExcluded {
		t.Errorf("&& 左侧为假应短路排除，got %s（%s）", fit.Status, fit.Reason)
	}
	// 取反
	fit = sc.matchCondSrc(sceneCond(t, "!(req.Source == SourceA)"), nil, nil, consts)
	if fit.Status != SceneTaken {
		t.Errorf("!(false) 应为 taken，got %s", fit.Status)
	}
	// 左侧为真、右侧未知 → 整体 unknown（不推测右侧）
	sc7 := NewScene([]string{"Source=7"})
	fit = sc7.matchCondSrc(sceneCond(t, "req.Source == SourceA && IsRegionX()"), nil, nil, consts)
	if fit.Status != SceneUnknown {
		t.Errorf("&& 右侧未知应保持 unknown，got %s", fit.Status)
	}
}
