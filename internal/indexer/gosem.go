package indexer

import (
	"context"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"

	"raph/internal/db"
	"raph/internal/verbose"

	"golang.org/x/tools/go/packages"
)

// linkGoUsages performs a type-accurate pass over the workspace's Go packages
// and links each function to the package-level symbols it references, with
// USES / MUTATES edges. Because this needs whole-package type information it
// runs only on a full index (not per-file incremental sync), and is
// best-effort: a package that fails to type-check is skipped, never fatal.
//
// Accurate def→use bindings (via go/types) matter specifically for globals:
// agents otherwise guess where a package-level var/const is read or written.
func (i *Indexer) linkGoUsages(ctx context.Context, stats *Stats) {
	if i.store == nil {
		return
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
		Dir:     i.root,
		Context: ctx,
		Fset:    token.NewFileSet(),
		Tests:   false,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		verbose.Printf("go usage pass skipped (load failed): %v", err)
		return
	}

	edges := 0
	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil || pkg.Fset == nil {
			continue
		}
		pkgScope := pkg.Types.Scope()
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				sourceID, ok := i.funcNodeID(pkg.Fset, fn)
				if !ok {
					continue
				}
				writes := collectWriteTargets(fn)
				seen := map[string]string{} // targetNodeID -> edgeType (USES upgraded to MUTATES)
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					ident, ok := n.(*ast.Ident)
					if !ok {
						return true
					}
					obj := pkg.TypesInfo.Uses[ident]
					if obj == nil {
						return true
					}
					if !isWorkspacePackageLevel(obj, pkgScope) {
						return true
					}
					targetID, ok := i.objectNodeID(pkg.Fset, obj)
					if !ok {
						return true
					}
					if targetID == sourceID {
						return true
					}
					edgeType := "USES"
					if _, isVar := obj.(*types.Var); isVar && writes[ident.Pos()] {
						edgeType = "MUTATES"
					}
					// MUTATES supersedes a prior USES for the same target.
					if prev, exists := seen[targetID]; exists && (prev == edgeType || edgeType == "USES") {
						return true
					}
					seen[targetID] = edgeType
					return true
				})
				for targetID, edgeType := range seen {
					if err := i.store.SaveEdge(ctx, db.Edge{SourceID: sourceID, TargetID: targetID, Type: edgeType}); err == nil {
						edges++
					}
				}
			}
		}
	}
	stats.EdgesSaved += edges
	verbose.Printf("go usage pass linked %d reference edges", edges)
}

// isWorkspacePackageLevel reports whether obj is a package-level symbol declared
// in this module (not a local, field, import, or builtin).
func isWorkspacePackageLevel(obj types.Object, pkgScope *types.Scope) bool {
	switch o := obj.(type) {
	case *types.Var:
		if o.IsField() {
			return false
		}
		return obj.Parent() == pkgScope
	case *types.Const:
		return obj.Parent() == pkgScope
	case *types.Func:
		// Package-level funcs have pkg scope as parent; methods have nil parent
		// but are still workspace symbols we want to link.
		if obj.Parent() == pkgScope {
			return true
		}
		sig, ok := o.Type().(*types.Signature)
		return ok && sig.Recv() != nil
	case *types.TypeName:
		return obj.Parent() == pkgScope
	default:
		return false
	}
}

// funcNodeID computes the graph node id for a FuncDecl, matching the scheme used
// by the AST indexer (receiver-qualified for methods).
func (i *Indexer) funcNodeID(fset *token.FileSet, fn *ast.FuncDecl) (string, bool) {
	relPath, ok := i.relPathFor(fset, fn.Pos())
	if !ok {
		return "", false
	}
	key := fn.Name.Name
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		if recv := receiverTypeName(fn.Recv.List[0].Type); recv != "" {
			key = recv + "." + fn.Name.Name
		}
	}
	return i.nodeID("func", relPath+"::"+key), true
}

// objectNodeID computes the graph node id for a referenced package-level object.
func (i *Indexer) objectNodeID(fset *token.FileSet, obj types.Object) (string, bool) {
	relPath, ok := i.relPathFor(fset, obj.Pos())
	if !ok {
		return "", false
	}
	switch o := obj.(type) {
	case *types.Const:
		return i.nodeID("const", relPath+"::"+obj.Name()), true
	case *types.Var:
		return i.nodeID("var", relPath+"::"+obj.Name()), true
	case *types.TypeName:
		return i.nodeID("type", relPath+"::"+obj.Name()), true
	case *types.Func:
		key := obj.Name()
		if sig, ok := o.Type().(*types.Signature); ok && sig.Recv() != nil {
			if recv := baseTypeName(sig.Recv().Type()); recv != "" {
				key = recv + "." + obj.Name()
			}
		}
		return i.nodeID("func", relPath+"::"+key), true
	}
	return "", false
}

func (i *Indexer) relPathFor(fset *token.FileSet, pos token.Pos) (string, bool) {
	if !pos.IsValid() {
		return "", false
	}
	filename := fset.Position(pos).Filename
	if filename == "" {
		return "", false
	}
	rel, err := filepath.Rel(i.root, filename)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// baseTypeName resolves a (possibly pointer/named) receiver type to its base
// type name.
func baseTypeName(t types.Type) string {
	switch typ := t.(type) {
	case *types.Pointer:
		return baseTypeName(typ.Elem())
	case *types.Named:
		return typ.Obj().Name()
	}
	return ""
}

// collectWriteTargets returns the positions of identifiers that are assignment
// or increment targets, so references can be classified as MUTATES vs USES.
func collectWriteTargets(fn *ast.FuncDecl) map[token.Pos]bool {
	writes := map[token.Pos]bool{}
	mark := func(expr ast.Expr) {
		if id := rootIdent(expr); id != nil {
			writes[id.Pos()] = true
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range s.Lhs {
				mark(lhs)
			}
		case *ast.IncDecStmt:
			mark(s.X)
		case *ast.UnaryExpr:
			if s.Op == token.AND {
				mark(s.X) // &global — taking the address implies potential mutation
			}
		}
		return true
	})
	return writes
}

// rootIdent walks selector/index expressions down to the base identifier
// (globalX.field / globalX[i] -> globalX).
func rootIdent(expr ast.Expr) *ast.Ident {
	switch e := expr.(type) {
	case *ast.Ident:
		return e
	case *ast.SelectorExpr:
		return rootIdent(e.X)
	case *ast.IndexExpr:
		return rootIdent(e.X)
	case *ast.StarExpr:
		return rootIdent(e.X)
	case *ast.ParenExpr:
		return rootIdent(e.X)
	}
	return nil
}
