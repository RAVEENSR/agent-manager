// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package audit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBeginIsAlwaysCompleted guards the intent/outcome protocol at the source
// level.
//
// audit.Begin writes an intent record with outcome "unknown"; attempt.Complete
// resolves it. A path that returns without calling Complete leaves that record
// unresolved forever, which reads in the trail as "the process died
// mid-operation" — indistinguishable from a real crash. Nothing at runtime
// detects it, and no behavioural test catches it, because the operation itself
// still succeeds.
//
// This found a live bug: DeleteUser recorded intent, called Complete on the
// failure branch, and returned from the success branch without one. Every
// successful user deletion left an orphan while every failure recorded
// correctly — the exact inverse of what a reader would assume.
//
// The check is deliberately shallow: it does not prove every path completes,
// only that a function starting an attempt has a Complete that is not confined
// to a single error branch. Two shapes pass:
//
//	attempt.Complete(ctx, err)           // one call covering both outcomes
//	... Complete(ctx, err) ... Complete(ctx, nil)  // one per branch
func TestBeginIsAlwaysCompleted(t *testing.T) {
	root := ".."
	dirs := []string{"services", "controllers"}

	for _, dir := range dirs {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			checkFile(t, filepath.Join(root, dir, name))
		}
	}
}

func checkFile(t *testing.T, path string) {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		begins, completes, guardedOnly := countAttemptCalls(fn.Body)
		if begins == 0 {
			continue
		}

		// A helper whose whole job is to start an attempt and hand it back
		// (services/audit_helpers.go, controllers/audit_helpers.go) completes
		// nothing itself; its caller does.
		if completes == 0 && returnsAttempt(fn) {
			continue
		}

		switch {
		case completes == 0:
			t.Errorf("%s: %s starts an audit attempt but never completes it; "+
				"every intent record it writes stays at outcome \"unknown\"",
				fset.Position(fn.Pos()), fn.Name.Name)
		case completes == 1 && guardedOnly:
			t.Errorf("%s: %s completes its audit attempt only inside an error branch, "+
				"so the success path leaves an unresolved intent record; "+
				"add attempt.Complete(ctx, nil) before the success return",
				fset.Position(fn.Pos()), fn.Name.Name)
		}
	}
}

// countAttemptCalls reports how many attempts a function body starts, how many
// Complete calls it makes, and whether every Complete sits inside a conditional
// (which usually means the success path is uncovered).
func countAttemptCalls(body *ast.BlockStmt) (begins, completes int, guardedOnly bool) {
	guardedOnly = true

	//nolint:staticcheck // S1021: a recursive closure cannot be declared and assigned in one statement.
	var walk func(n ast.Node, depth int)
	walk = func(n ast.Node, depth int) {
		ast.Inspect(n, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				if fun.Sel.Name == "Begin" && isAuditPkg(fun.X) {
					begins++
				}
				if fun.Sel.Name == "Complete" {
					completes++
					if depth == 0 {
						guardedOnly = false
					}
				}
			case *ast.Ident:
				if strings.HasPrefix(fun.Name, "begin") && strings.Contains(fun.Name, "Audit") {
					begins++
				}
			}
			return true
		})
	}

	// Statements directly in the function body are depth 0; anything nested in
	// an if/switch/for is depth 1.
	for _, stmt := range body.List {
		switch s := stmt.(type) {
		case *ast.IfStmt, *ast.SwitchStmt, *ast.ForStmt, *ast.RangeStmt, *ast.TypeSwitchStmt:
			walk(s, 1)
		default:
			walk(stmt, 0)
		}
	}
	return begins, completes, guardedOnly
}

func isAuditPkg(x ast.Expr) bool {
	ident, ok := x.(*ast.Ident)
	return ok && ident.Name == "audit"
}

// returnsAttempt reports whether a function hands an *audit.Attempt back to its
// caller, which makes it a helper rather than an emit site.
func returnsAttempt(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil {
		return false
	}
	for _, result := range fn.Type.Results.List {
		star, ok := result.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		if sel, ok := star.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "Attempt" {
			return true
		}
		if ident, ok := star.X.(*ast.Ident); ok && ident.Name == "Attempt" {
			return true
		}
	}
	return false
}
