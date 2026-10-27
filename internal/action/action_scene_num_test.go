package action

// todo23 §5：内建整数转换的范围校验与遮蔽（同名函数/局部变量不得当转换）。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestSceneIntConversionRange(t *testing.T) {
	consts := sceneConstsMap{"SourceB": "19"}
	cases := []struct {
		cond   string
		scene  string
		status string
	}{
		{"int32(req.Source) == int32(4)", "4", SceneTaken},
		{"int32(req.Source) == int32(SourceB)", "19", SceneTaken},
		{"int32(req.Source) == int32(SourceB)", "4", SceneExcluded},
		// 越界转换：不得截断后匹配（保持 unknown，不 taken/excluded）
		{"req.Source == int8(256)", "0", SceneUnknown},
		{"req.Source == uint8(-1)", "255", SceneUnknown},
		{"req.Source == int64(9223372036854775808)", "9223372036854775808", SceneUnknown},
		// 合法边界仍可判定
		{"req.Source == int8(127)", "127", SceneTaken},
		{"req.Source == uint8(255)", "255", SceneTaken},
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

// TestSceneConversionShadowed：同名包级函数/局部变量遮蔽内建类型名时，
// 不按数值转换求值（条件保持 unknown）。
func TestSceneConversionShadowed(t *testing.T) {
	src := `package m

type Req struct{ Source int }

func int32(x int) int { return x }

func F(req Req) {
	if req.Source == int32(4) {
		A()
	}
}

func G(req Req) {
	int8 := func(x int) int { return x }
	if req.Source == int8(4) {
		B()
	}
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var conds []ast.Expr
	ast.Inspect(f, func(n ast.Node) bool {
		if ifs, ok := n.(*ast.IfStmt); ok {
			conds = append(conds, ifs.Cond)
		}
		return true
	})
	if len(conds) != 2 {
		t.Fatalf("应有 2 个 if 条件，got %d", len(conds))
	}
	sc := NewScene([]string{"Source=4"})
	for i, cond := range conds {
		if fit := sc.matchCondSrc(cond, nil, nil, nil); fit.Status != SceneUnknown {
			t.Errorf("遮蔽的转换（条件 %d）应保持 unknown，got %s（%s）", i, fit.Status, fit.Reason)
		}
	}
}

// TestSceneOrUnknownLeftPreserved：`mutate() || sourceType==4` 左侧未知时，
// 右侧为真也不能判 taken（未知调用可能先改写变量）。
func TestSceneOrUnknownLeftPreserved(t *testing.T) {
	sc := NewScene([]string{"Source=4"})
	fit := sc.matchCondSrc(sceneCond(t, "mutate() || req.Source == 4"), nil, nil, nil)
	if fit.Status != SceneUnknown {
		t.Errorf("左侧未知调用时 || 右侧为真应保持 unknown，got %s（%s）", fit.Status, fit.Reason)
	}
	// 左侧确定排除 / 确定 taken 仍可短路
	if fit := sc.matchCondSrc(sceneCond(t, "req.Source == 5 || req.Source == 4"), nil, nil, nil); fit.Status != SceneTaken {
		t.Errorf("左侧确定排除 + 右侧为真应 taken，got %s", fit.Status)
	}
	if fit := sc.matchCondSrc(sceneCond(t, "req.Source == 4 || mutate()"), nil, nil, nil); fit.Status != SceneTaken {
		t.Errorf("左侧 taken 应短路 taken，got %s", fit.Status)
	}
}
