package money

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moneyPaths are the packages where a float is not a style question.
//
// Add a package here the moment it starts touching peso amounts. The list is
// explicit rather than "everything under internal/domain" so that adding a
// package is a deliberate act with a reason, and so this test never starts
// failing on code it was never meant to police.
var moneyPaths = []string{
	".",
	"../ledger",
}

// TestNoFloatsInMoneyPaths is the enforcement behind the promise in the package
// comment.
//
// A rule written in a review checklist survives until the reviewer is busy. A
// rule that fails the build survives until someone deliberately deletes this
// test, which is a thing you cannot do by accident and cannot do quietly.
func TestNoFloatsInMoneyPaths(t *testing.T) {
	for _, dir := range moneyPaths {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Fatalf("%s is listed as a money path but does not exist; fix the list rather than deleting it", dir)
		}
		for _, found := range floatsIn(t, dir) {
			t.Errorf("%s: %s — peso amounts are int64 centavos; see the money package comment", found.position, found.what)
		}
	}
}

// TestTheFloatDetectorActuallyDetects proves the test above can fail.
//
// Without this, a detector that silently matched nothing would look exactly
// like a clean codebase, and the guarantee would quietly evaporate — the same
// failure mode as the legacy carry-forward balances this system exists to
// replace.
func TestTheFloatDetectorActuallyDetects(t *testing.T) {
	deliberate := map[string]string{
		"a declared float": `package p
			var rate float64`,
		"a float conversion": `package p
			func f(n int64) any { return float64(n) }`,
		"a float literal": `package p
			var rate = 0.03`,
		"a float struct field": `package p
			type Advance struct{ Amount float32 }`,
		"a float in a signature": `package p
			func Interest(principal int64) float64 { return 0 }`,
	}

	for name, src := range deliberate {
		t.Run(name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "sample.go", src, 0)
			if err != nil {
				t.Fatalf("parse sample: %v", err)
			}
			if found := floatsInFile(fset, file); len(found) == 0 {
				t.Errorf("%s was not detected; the float ban is not actually enforced", name)
			}
		})
	}

	// And the other half of the claim: honest integer code is left alone.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", `package p
		const rate = 3
		func Interest(principal int64) int64 { return principal * rate / 100 }`, 0)
	if err != nil {
		t.Fatalf("parse sample: %v", err)
	}
	if found := floatsInFile(fset, file); len(found) != 0 {
		t.Errorf("integer arithmetic was reported as a float: %v", found)
	}
}

type floatUse struct {
	position string
	what     string
}

func floatsIn(t *testing.T, dir string) []floatUse {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	var found []floatUse
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		path := filepath.Join(dir, name)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		found = append(found, floatsInFile(fset, file)...)
	}
	return found
}

// floatsInFile reports every floating-point declaration, conversion and literal
// in one file.
//
// Both halves matter. The identifiers catch `var x float64` and `float64(n)`,
// which are the same node as far as the syntax tree is concerned. The literals
// catch `0.03`, which has no identifier at all and would otherwise walk
// straight past an identifier-only check into an untyped constant.
func floatsInFile(fset *token.FileSet, file *ast.File) []floatUse {
	var found []floatUse

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.Ident:
			switch node.Name {
			case "float32", "float64", "complex64", "complex128":
				found = append(found, floatUse{
					position: fset.Position(node.Pos()).String(),
					what:     node.Name + " appears here",
				})
			}
		case *ast.BasicLit:
			if node.Kind == token.FLOAT || node.Kind == token.IMAG {
				found = append(found, floatUse{
					position: fset.Position(node.Pos()).String(),
					what:     "the literal " + node.Value + " is floating point",
				})
			}
		}
		return true
	})

	return found
}
