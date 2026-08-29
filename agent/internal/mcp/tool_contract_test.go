package mcp

// A TOOL'S SCHEMA AND DESCRIPTION ARE A CONTRACT, AND ITS ONLY READER IS A MODEL.
//
// That is what makes drift here quieter than anywhere else in the product. When
// a declared input property is not read, the model supplies it, believes it took
// effect, and receives a result computed without it — no error, no log line, and
// no human in the loop to notice the answer did not change. When a description
// names a returned field that is not returned, or a mechanism that was replaced,
// nothing fails either.
//
// ⚠ THIS FILE EXISTS BECAUSE THE SECOND ONE HAD ALREADY HAPPENED AND LASTED
// THREE AND A HALF MONTHS. search_codebase said "Search the indexed codebase by
// path/filename substrings" from 2026-05-25 (d276e39) until 2026-08-29, while
// c1aa63f replaced that implementation with a per-query embedding on 2026-07-16.
// The commit that made the RESULT honest ("honest MCP relevance") left the
// DESCRIPTION describing the mechanism it had just removed.
//
// The measurement that found it also cleared everything else: all 21 declared
// properties are read AND used by their handlers, and 9 of the 10 descriptions
// were accurate. So this file is mostly a guard on a clean surface — which is the
// point, because the one that was not clean broke silently.
//
// ⚠ IT ADDS NO PERMISSION AND REMOVES NONE. It changes no tool's behaviour and no
// tool's capability; it checks that what is DECLARED matches what is DONE.
//
// ⚠ WHERE THE FACTS COME FROM, BECAUSE IT DECIDES WHAT A ZERO IS WORTH: the
// CONTRACT is read by CALLING toolDefinitions(), never by parsing a copy of it —
// a guard that re-declared the tool list would pass against its own copy. The
// IMPLEMENTATION facts (which handler serves which tool, which struct it decodes,
// which fields it touches) come from the AST of server.go, because "is this
// property read" cannot be answered by a text search: the declaration and the
// read are the same string in the same file, so a name-keyed grep scores every
// property as honoured and returns a beautiful zero.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const serverSrc = "server.go"

// Vacuity floors — literals, deliberately not derived from what they defend.
const (
	minTools              = 10
	minDeclaredProperties = 18
)

// ---------------------------------------------------------------------------
// Implementation facts, from the AST.
// ---------------------------------------------------------------------------

type implFacts struct {
	// tool name -> handler method name, from dispatchTool's switch.
	handlerFor map[string]string
	// handler method -> args struct it decodes into.
	argsOf map[string]string
	// struct -> json tag -> Go field name.
	fieldsOf map[string]map[string]string
	// handler method -> field name -> times referenced in its body.
	usesIn map[string]map[string]int
	// handler method -> the set of literal map keys it returns.
	returnsIn map[string]map[string]bool
	// handler method -> callee names it invokes.
	callsIn map[string]map[string]bool
}

func scanImpl(t *testing.T) *implFacts {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, serverSrc, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", serverSrc, err)
	}
	im := &implFacts{
		handlerFor: map[string]string{},
		argsOf:     map[string]string{},
		fieldsOf:   map[string]map[string]string{},
		usesIn:     map[string]map[string]int{},
		returnsIn:  map[string]map[string]bool{},
		callsIn:    map[string]map[string]bool{},
	}

	// 1. struct name -> json tag -> field
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, fl := range st.Fields.List {
			if fl.Tag == nil {
				continue
			}
			tv, e := strconv.Unquote(fl.Tag.Value)
			if e != nil {
				continue
			}
			tag := strings.Split(reflect.StructTag(tv).Get("json"), ",")[0]
			if tag == "" {
				continue
			}
			for _, nm := range fl.Names {
				if im.fieldsOf[ts.Name.Name] == nil {
					im.fieldsOf[ts.Name.Name] = map[string]string{}
				}
				im.fieldsOf[ts.Name.Name][tag] = nm.Name
			}
		}
		return true
	})

	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		name := fd.Name.Name

		// 2. dispatchTool's switch: case "tool": return s.toolX(...)
		if name == "dispatchTool" {
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				cc, ok := n.(*ast.CaseClause)
				if !ok {
					return true
				}
				var lit string
				for _, e := range cc.List {
					if bl, ok := e.(*ast.BasicLit); ok && bl.Kind == token.STRING {
						if s, err := strconv.Unquote(bl.Value); err == nil {
							lit = s
						}
					}
				}
				if lit == "" {
					return true
				}
				ast.Inspect(cc, func(m ast.Node) bool {
					ce, ok := m.(*ast.CallExpr)
					if !ok {
						return true
					}
					if se, ok := ce.Fun.(*ast.SelectorExpr); ok {
						im.handlerFor[lit] = se.Sel.Name
					}
					return true
				})
				return true
			})
			continue
		}

		// 3. handler bodies: decoded args var, field uses, returned keys, callees.
		varOf := map[string]string{}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if !ok || vs.Type == nil {
				return true
			}
			if id, ok := vs.Type.(*ast.Ident); ok {
				if _, isArgs := im.fieldsOf[id.Name]; isArgs && strings.HasSuffix(id.Name, "Args") {
					for _, nm := range vs.Names {
						varOf[nm.Name] = id.Name
					}
					im.argsOf[name] = id.Name
				}
			}
			return true
		})
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok {
					if _, isArgsVar := varOf[id.Name]; isArgsVar {
						if im.usesIn[name] == nil {
							im.usesIn[name] = map[string]int{}
						}
						im.usesIn[name][x.Sel.Name]++
					}
				}
			case *ast.KeyValueExpr:
				if bl, ok := x.Key.(*ast.BasicLit); ok && bl.Kind == token.STRING {
					if s, err := strconv.Unquote(bl.Value); err == nil {
						if im.returnsIn[name] == nil {
							im.returnsIn[name] = map[string]bool{}
						}
						im.returnsIn[name][s] = true
					}
				}
			case *ast.CallExpr:
				callee := ""
				switch fn := x.Fun.(type) {
				case *ast.Ident:
					callee = fn.Name
				case *ast.SelectorExpr:
					callee = fn.Sel.Name
				}
				if callee != "" {
					if im.callsIn[name] == nil {
						im.callsIn[name] = map[string]bool{}
					}
					im.callsIn[name][callee] = true
				}
			}
			return true
		})
	}
	return im
}

// declaredProperties pulls the property names out of a tool's inputSchema, by
// CALLING toolDefinitions() rather than re-declaring the list here.
func declaredProperties(tool map[string]any) []string {
	schema, ok := tool["inputSchema"].(map[string]any)
	if !ok {
		return nil
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(props))
	for k := range props {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestEveryDeclaredPropertyIsReadByItsHandler is the dangerous direction: a
// property a model can send that nothing behind it reads is an INERT PARAMETER,
// and its caller has no way to find out.
func TestEveryDeclaredPropertyIsReadByItsHandler(t *testing.T) {
	im := scanImpl(t)
	tools := toolDefinitions()

	if len(tools) < minTools {
		t.Fatalf("VACUITY: toolDefinitions() returned %d tools (floor %d)", len(tools), minTools)
	}
	if len(im.fieldsOf) == 0 || len(im.handlerFor) == 0 {
		t.Fatalf("VACUITY: the AST scan found %d structs and %d dispatch cases — it did not read %s",
			len(im.fieldsOf), len(im.handlerFor), serverSrc)
	}

	total := 0
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		handler, ok := im.handlerFor[name]
		if !ok {
			t.Errorf("tool %q is declared in toolDefinitions() but dispatchTool has no case for it, "+
				"so calling it returns 'unknown tool'", name)
			continue
		}
		argsStruct, ok := im.argsOf[handler]
		props := declaredProperties(tool)
		if !ok {
			if len(props) > 0 {
				t.Errorf("tool %q declares %d propert(ies) but its handler %s decodes no *Args "+
					"struct, so nothing it is sent can be read", name, len(props), handler)
			}
			continue
		}
		for _, prop := range props {
			total++
			field, ok := im.fieldsOf[argsStruct][prop]
			if !ok {
				t.Errorf("INERT PARAMETER: tool %q declares the property %q, but %s has no field "+
					"with that json tag — a caller that sends it is silently ignored.",
					name, prop, argsStruct)
				continue
			}
			if im.usesIn[handler][field] == 0 {
				t.Errorf("INERT PARAMETER: tool %q declares the property %q and %s decodes it into "+
					"%s.%s, but %s never references that field. A model that sends it believes it "+
					"took effect and receives a result computed without it — no error, and nobody "+
					"watching.", name, prop, argsStruct, argsStruct, field, handler)
			}
		}
	}
	if total < minDeclaredProperties {
		t.Fatalf("VACUITY: only %d declared properties were checked (floor %d)", total, minDeclaredProperties)
	}
}

// TestDispatchAndCatalogueAgree — a tool the dispatcher serves but the catalogue
// does not list is unreachable to any client that reads tools/list, and one the
// catalogue lists but the dispatcher does not serve answers "unknown tool".
func TestDispatchAndCatalogueAgree(t *testing.T) {
	im := scanImpl(t)
	declared := map[string]bool{}
	for _, tool := range toolDefinitions() {
		if n, ok := tool["name"].(string); ok {
			declared[n] = true
		}
	}
	if len(declared) < minTools {
		t.Fatalf("VACUITY: only %d tools declared (floor %d)", len(declared), minTools)
	}
	for n := range declared {
		if _, ok := im.handlerFor[n]; !ok {
			t.Errorf("%q is in the catalogue but dispatchTool has no case for it", n)
		}
	}
	for n := range im.handlerFor {
		if !declared[n] {
			t.Errorf("dispatchTool serves %q but the catalogue does not list it, so no client "+
				"reading tools/list can discover it", n)
		}
	}
}

// descriptionPromise is a field a tool's description tells the model it will get
// back. Prose is not parseable, so each promise is derived by READING the
// sentence and recorded here — with the words it was read from, so that
// rewriting the description forces this table to be revisited rather than
// silently invalidating it.
type descriptionPromise struct {
	// words must still appear in the live description; if they do not, the
	// sentence was rewritten and the keys below are a claim about text that no
	// longer exists.
	words string
	keys  []string
}

var descriptionPromises = map[string]descriptionPromise{
	"generate_tests": {"Returns the test code, a suggested output path, and the estimated AI cost",
		[]string{"tests", "output_file", "cost_usd"}},
	"review_code": {"Returns a markdown review plus counts of critical/warning findings",
		[]string{"review", "critical_count", "warning_count"}},
	"get_active_issue": {"Returns its identifier, title, status, description, and AI cost rollup",
		[]string{"identifier", "title", "status", "description", "ai_cost_usd"}},
	"search_codebase": {"Returns the top matches with path, language, line range, and the true cosine score",
		[]string{"path", "language", "start_line", "end_line", "score"}},
	"search_docs": {"Returns the top matches with title, space, excerpt, and URL",
		[]string{"title", "space", "excerpt", "url"}},
	"ask_docs": {"Returns the answer plus the source pages used to derive it",
		[]string{"answer", "sources"}},
	"get_codebase_summary": {"languages by file count, total files/lines, git branch, and repo name",
		[]string{"languages", "total_files", "total_lines", "git_branch", "git_repo"}},
}

// TestEveryFieldADescriptionPromisesIsReturned is the other direction: a
// description that enumerates fields is a falsifiable claim, and a model plans
// around it.
//
// ⚠ talyvor-track ships the live version of this defect: its MCP list_members
// description says "Returns id, name, email, avatar_url, and role", and
// members.avatar_url is a column NOTHING WRITES (W3.66) — so the model is told
// about a field that is the empty string for every row that has ever existed.
// Here the promised keys are all genuinely returned; this keeps that true.
func TestEveryFieldADescriptionPromisesIsReturned(t *testing.T) {
	im := scanImpl(t)
	byName := map[string]map[string]any{}
	for _, tool := range toolDefinitions() {
		if n, ok := tool["name"].(string); ok {
			byName[n] = tool
		}
	}
	if len(descriptionPromises) == 0 {
		t.Fatal("VACUITY: no promises declared, so this test verifies nothing")
	}
	checked := 0
	for name, p := range descriptionPromises {
		tool, ok := byName[name]
		if !ok {
			t.Errorf("descriptionPromises names %q, which is no longer a tool — delete the entry", name)
			continue
		}
		desc, _ := tool["description"].(string)
		if !strings.Contains(desc, p.words) {
			t.Errorf("the description of %q no longer contains the words this entry was read from,\n"+
				"    so the fields it promises are a claim about text that no longer exists.\n"+
				"    Re-read the live description and update the entry.\n"+
				"    expected to find: %q\n    live description: %q", name, p.words, desc)
			continue
		}
		handler := im.handlerFor[name]
		for _, key := range p.keys {
			checked++
			if !im.returnsIn[handler][key] {
				t.Errorf("BROKEN PROMISE: %q's description says it returns %q, but %s never puts "+
					"that key in a returned map. A model plans around this sentence.",
					name, key, handler)
			}
		}
	}
	if checked == 0 {
		t.Fatal("VACUITY: no promised key was checked")
	}
}

// TestSearchCodebaseDoesNotClaimSubstringMatching pins the specific drift that
// created this file, in the one direction that is actually checkable.
//
// A description is prose and most of it cannot be verified mechanically. But
// "which retrieval mechanism does this use" CAN be: the handler either calls the
// semantic retriever or it does not. This asserts the two cannot disagree again
// — which they did, undetected, for three and a half months.
func TestSearchCodebaseDoesNotClaimSubstringMatching(t *testing.T) {
	im := scanImpl(t)
	handler := im.handlerFor["search_codebase"]
	if handler == "" {
		t.Fatal("VACUITY: no handler found for search_codebase")
	}
	semantic := im.callsIn[handler]["semanticRetriever"]

	var desc string
	for _, tool := range toolDefinitions() {
		if n, _ := tool["name"].(string); n == "search_codebase" {
			desc, _ = tool["description"].(string)
		}
	}
	if desc == "" {
		t.Fatal("VACUITY: search_codebase has no description to check")
	}
	lower := strings.ToLower(desc)
	// "substrings" appearing as a DENIAL ("rather than on path or filename
	// substrings") is the corrected wording and must stay allowed; what must not
	// appear is the claim that the search is BY substrings.
	claimsSubstring := strings.Contains(lower, "by path/filename substrings") ||
		strings.Contains(lower, "by path substring") ||
		strings.Contains(lower, "by filename substring")

	if semantic && claimsSubstring {
		t.Errorf("search_codebase's handler %s ranks with the SEMANTIC retriever, but its "+
			"description still tells the model the search is by path/filename substrings.\n"+
			"    That exact disagreement shipped from 2026-05-25 to 2026-08-29. A model told\n"+
			"    'substrings' supplies \"auth\" or \"*.go\" and reads a cosine ranking as a\n"+
			"    filename match.\n    live description: %q", handler, desc)
	}
	if !semantic && !claimsSubstring {
		t.Errorf("search_codebase's handler %s no longer calls semanticRetriever, so the "+
			"description's semantic claim may now be the stale one. Re-read both.\n"+
			"    live description: %q", handler, desc)
	}
}
