package event

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

// Three source facts, because all three are one convenience method away and all
// three are invisible once written. The kernel opens no transaction, commits
// none and rolls none back — the seam declares no such method, so there is no
// exemption to write here. It retries no append: a conflict is the caller's to
// resolve with a fresh load and a fresh decision, and a framework that loops
// over Append writes a stale decision at a fresh version. And it has no goto,
// which is the shape a retry takes when the loop has been argued away.
//
// Two of the arms are resolved rather than spelled. Transaction control is read
// case-blind wherever the name appears — called, or bound to a variable and
// called a line later — and covers a free function, so `tx.commit()`,
// `commit(tx)` and `finish := tx.Commit` are one finding. An `Append` is the
// store's when its receiver also answers `ReadStream` — the seam's own shape —
// so a builder that happens to have an `Append` is not reported and the check
// never has to be loosened to keep one. A retry written as recursion is the
// third arm.
//
// What is not seen, as a closed list: mutual recursion through two functions,
// which the loop arm catches in its ordinary form; and a commit reached through
// an interface method of some other name, which no source check can decide.
func TestTheKernelNeverIssuesTransactionControlAndNeverRetries(t *testing.T) {
	t.Run("the kernel issues no transaction control, retries no append and has no goto", func(t *testing.T) {
		typed := typedSources(t, ".")
		if len(typed.files) < 15 {
			t.Fatalf("%d files of the kernel were read, so this walked the wrong directory", len(typed.files))
		}
		for _, complaint := range controlComplaints(typed) {
			t.Error(complaint)
		}
	})

	t.Run("each arm reports the shape it is written for and nothing beside it", func(t *testing.T) {
		reported := strings.Join(controlComplaints(typedSources(t, writeFixture(t, transactionControlFixture))), "\n")

		for _, expected := range []string{
			"beginsATransaction", "commitsThroughAMethod", "commitsThroughAFreeFunction",
			"commitsThroughABoundMethod", "commitsThroughAHandedOffFunction",
			"rollsBack", "retriesInALoop", "retriesByRecursion", "jumpsBack",
		} {
			if !strings.Contains(reported, expected) {
				t.Fatalf("the fixture's %s was not reported, so the arm that would have found it proves nothing:\n%s", expected, reported)
			}
		}
		for _, permitted := range []string{"pagesARead", "appendsToABuilder", "handsOffAnAppend"} {
			if strings.Contains(reported, permitted) {
				t.Fatalf("the fixture's %s was reported, and a paged read, a builder of somebody else's and a bound append are what every load does:\n%s", permitted, reported)
			}
		}
	})
}

func controlComplaints(typed typedPackage) []string {
	var complaints []string
	for _, file := range typed.files {
		for _, declaration := range file.Decls {
			within := declaredAs(declaration)
			report := func(node ast.Node, what string) {
				complaints = append(complaints, typed.at(node)+" in "+within+": "+what)
			}
			called := calleesOf(declaration)
			ast.Inspect(declaration, func(node ast.Node) bool {
				switch found := node.(type) {
				case *ast.CallExpr:
					if name := controlCalled(found); name != "" {
						report(found, name+" is called, and the transaction belongs to the caller from the first statement to the last")
					}
				case *ast.SelectorExpr, *ast.Ident:
					if called[node] {
						return true
					}
					if name := controlBound(typed.info, node); name != "" {
						report(node, name+" is bound as a value, and a transaction control reached through a variable is still this framework issuing it")
					}
				case *ast.BranchStmt:
					if found.Tok == token.GOTO {
						report(found, "goto, which is what a retry looks like once the loop around it has been argued away")
					}
				case *ast.ForStmt, *ast.RangeStmt:
					for _, retried := range appendsOnALog(typed.info, node) {
						report(retried, "the store's Append is called inside a loop, and re-appending a decision the stream has moved past writes a stale decision at a fresh version")
					}
				}
				return true
			})
			if function, isFunction := declaration.(*ast.FuncDecl); isFunction && retriesItself(typed.info, function) {
				report(function, "calls itself around the store's Append, and a retry is the caller's transaction rather than a loop of ours")
			}
		}
	}
	return complaints
}

func controlCalled(call *ast.CallExpr) string {
	return controlNamed(calledName(call))
}

func controlNamed(name string) string {
	switch strings.ToLower(name) {
	case "begin", "commit", "rollback":
		return name
	}
	return ""
}

// The callee of every call in the declaration, so the arm that reads a name in
// value position does not report the ordinary call the arm above it already
// did.
func calleesOf(declaration ast.Decl) map[ast.Node]bool {
	called := map[ast.Node]bool{}
	ast.Inspect(declaration, func(node ast.Node) bool {
		if call, isCall := node.(*ast.CallExpr); isCall {
			called[call.Fun] = true
		}
		return true
	})
	return called
}

// A method or a function reached by name and not called there: `finish :=
// tx.Commit`, a `defer` of a bound method value, a commit handed to something
// that takes a `func()`. The selector's own identifier is skipped, because the
// selector it belongs to answers for it.
func controlBound(info *types.Info, node ast.Node) string {
	switch held := node.(type) {
	case *ast.SelectorExpr:
		if chosen := info.Selections[held]; chosen != nil && chosen.Kind() == types.MethodVal {
			return controlNamed(held.Sel.Name)
		}
	case *ast.Ident:
		function, isFunction := info.Uses[held].(*types.Func)
		if isFunction && function.Signature().Recv() == nil {
			return controlNamed(held.Name)
		}
	}
	return ""
}

func calledName(call *ast.CallExpr) string {
	called := call.Fun
	for {
		switch held := called.(type) {
		case *ast.ParenExpr:
			called = held.X
		case *ast.IndexExpr:
			called = held.X
		case *ast.IndexListExpr:
			called = held.X
		case *ast.SelectorExpr:
			return held.Sel.Name
		case *ast.Ident:
			return held.Name
		default:
			return ""
		}
	}
}

func appendsOnALog(info *types.Info, loop ast.Node) []ast.Node {
	var retried []ast.Node
	ast.Inspect(loop, func(node ast.Node) bool {
		if call, isCall := node.(*ast.CallExpr); isCall && onALog(info, call) {
			retried = append(retried, call)
		}
		return true
	})
	return retried
}

func onALog(info *types.Info, call *ast.CallExpr) bool {
	selector, isSelector := call.Fun.(*ast.SelectorExpr)
	if !isSelector || selector.Sel.Name != "Append" {
		return false
	}
	chosen := info.Selections[selector]
	if chosen == nil || chosen.Kind() != types.MethodVal {
		return false
	}
	reads, _, _ := types.LookupFieldOrMethod(chosen.Recv(), true, nil, "ReadStream")
	_, isMethod := reads.(*types.Func)
	return isMethod
}

func retriesItself(info *types.Info, function *ast.FuncDecl) bool {
	declared := info.Defs[function.Name]
	if declared == nil || function.Body == nil {
		return false
	}
	appends, recurses := false, false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		if onALog(info, call) {
			appends = true
		}
		if called, isName := call.Fun.(*ast.Ident); isName && info.Uses[called] == declared {
			recurses = true
		}
		if selector, isSelector := call.Fun.(*ast.SelectorExpr); isSelector && info.Uses[selector.Sel] == declared {
			recurses = true
		}
		return true
	})
	return appends && recurses
}
