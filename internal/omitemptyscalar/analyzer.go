// Package omitemptyscalar flags `,omitempty` on struct fields whose zero
// value carries meaning, so the marshaler silently drops a real value.
//
// It is a go/analysis analyzer, not a ruleguard rule, because ruleguard can read
// neither struct tags as match variables nor tags through a custom filter: its
// type shim exposes no Tag accessor. go/types does. The audit gate that runs it
// is described in repo-standard's README, section "Rules the audit and lint
// standard adds".
//
// Scope is deliberately bool-only. A `bool` with omitempty can never send
// `false`, so a meaningful false is dropped from the wire: always a bug.
// Integer fields are left out on purpose: a zero int is often
// a legitimately-absent value (page=0, offset=0), so an int rule carries real
// false positives that would drown the signal. Widen only per-repo, behind a
// flag, if a repo's ints are known-meaningful.
package omitemptyscalar

import (
	"errors"
	"go/ast"
	"go/types"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// Analyzer reports bool struct fields tagged with `,omitempty`.
var Analyzer = &analysis.Analyzer{
	Name:     "omitemptyscalar",
	Doc:      "reports omitempty on bool fields, whose false zero-value is meaningful and gets silently dropped",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

// marshalKeys are the struct-tag keys whose grammar defines `,omitempty` as
// "drop the zero value". Keys outside this set (e.g. a custom `validate` tag)
// may contain the word incidentally and must not fire the rule.
var marshalKeys = []string{"json", "yaml", "xml", "toml", "bson", "msgpack", "mapstructure"}

// errNoInspector marks a pass run without the inspect analyzer's result.
var errNoInspector = errors.New("omitemptyscalar: inspect result missing")

func run(pass *analysis.Pass) (any, error) {
	insp, ok := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if !ok {
		return nil, errNoInspector
	}
	insp.Preorder([]ast.Node{(*ast.StructType)(nil)}, func(n ast.Node) {
		st, ok := n.(*ast.StructType)
		if !ok {
			return
		}
		for _, field := range st.Fields.List {
			checkField(pass, field)
		}
	})
	return nil, nil //nolint:nilnil // the analyzer declares no ResultType, so go/analysis requires a nil result
}

// checkField reports one field tagged omitempty whose type is bool.
func checkField(pass *analysis.Pass, field *ast.Field) {
	if field.Tag == nil || !isBool(pass.TypesInfo.TypeOf(field.Type)) {
		return
	}
	// field.Tag.Value is a Go string literal (backtick- or double-quoted);
	// Unquote yields the raw tag content.
	raw, err := strconv.Unquote(field.Tag.Value)
	if err != nil || silenced(field) {
		return
	}
	if key, ok := hasOmitempty(reflect.StructTag(raw)); ok {
		pass.Report(analysis.Diagnostic{
			Pos:     field.Tag.Pos(),
			Message: "omitempty on bool field (" + key + "): false is meaningful and will be dropped; drop omitempty or use *bool",
		})
	}
}

// silenced reports whether the field's doc or trailing comment carries
// `//nolint:omitemptyscalar` (a reason after `//` is expected by convention).
// A deliberate absent-equals-false wire encoding is silenced this way.
func silenced(f *ast.Field) bool {
	for _, cg := range []*ast.CommentGroup{f.Doc, f.Comment} {
		if cg == nil {
			continue
		}
		for _, c := range cg.List {
			if strings.Contains(c.Text, "nolint:omitemptyscalar") {
				return true
			}
		}
	}
	return false
}

func isBool(t types.Type) bool {
	if t == nil {
		return false
	}
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Kind() == types.Bool
}

// hasOmitempty returns the first marshal key whose tag value carries the
// omitempty option, and whether any did.
func hasOmitempty(tag reflect.StructTag) (string, bool) {
	for _, key := range marshalKeys {
		v, ok := tag.Lookup(key)
		if !ok {
			continue
		}
		if slices.Contains(strings.Split(v, ",")[1:], "omitempty") {
			return key, true
		}
	}
	return "", false
}
