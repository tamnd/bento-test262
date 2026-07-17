package tc39

import "strings"

// A non-strict test262 test may name a global it never declares. The generated
// for-of, for-in, and for-await-of families do this constantly: the binding is a
// destructuring pattern whose leaves are bare single letters, and in sloppy mode
// an assignment to an undeclared name is an implicit global, legal at runtime.
// bento rides the tamnd/typescript checker under a strict config, so each of
// those leaves comes back as `Cannot find name 'x'` and the whole test hands
// back before it ever runs.
//
// hoistSloppyForBindings gives those leaves a top-level `var` so the checker
// binds them, and only those leaves. The rule is deliberately narrow so it can
// never turn a should-fail test green: a name is hoisted only when every one of
// its occurrences in the body is a binding target inside a for-of/for-in head,
// and it never appears as a read or a declaration. A bare read of an undeclared
// name, a host probe like $262, or a name a test reads to force a ReferenceError
// is never a binding target, so it keeps its honest handback. Every uncertainty
// in the walk resolves toward read, so a parse the walker cannot model drops the
// candidate rather than inventing a binding for it.
//
// The scan is only ever run for the sloppy mode of a non-negative test. A strict
// test wants the name error, and a negative test wants its failure, so neither
// is touched by the caller.
func hoistSloppyForBindings(body string) string {
	code := maskLiterals(body)
	targets := forBindingTargets(code)
	if len(targets) == 0 {
		return ""
	}

	// A name is winnable only if it is a target and nothing else. Walk every
	// identifier occurrence in the body; a single non-target occurrence, a read
	// or a declaration, drops the name.
	hasTarget := map[string]bool{}
	hasOther := map[string]bool{}
	for _, occ := range scanIdents(code) {
		if targets[occ.pos] {
			hasTarget[occ.name] = true
			continue
		}
		hasOther[occ.name] = true
	}

	var names []string
	seen := map[string]bool{}
	for _, occ := range scanIdents(code) {
		if !targets[occ.pos] || seen[occ.name] {
			continue
		}
		if hasOther[occ.name] {
			continue
		}
		seen[occ.name] = true
		names = append(names, occ.name)
	}
	if len(names) == 0 {
		return ""
	}
	return "var " + strings.Join(names, ", ") + ";\n"
}

// idOcc is one identifier token and the byte offset it starts at in the masked
// code, so a target position collected by the pattern walk lines up with the
// same token seen by the global occurrence scan.
type idOcc struct {
	name string
	pos  int
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// scanIdents returns every identifier token in the masked code, skipping a name
// that reads as a member property (a lone `.` before it), since a property is
// not a use of the variable of that name. A rest `...x` keeps its name: three
// dots is not a member access.
func scanIdents(code string) []idOcc {
	var out []idOcc
	for i := 0; i < len(code); {
		c := code[i]
		if !isIdentStart(c) {
			i++
			continue
		}
		j := i + 1
		for j < len(code) && isIdentPart(code[j]) {
			j++
		}
		if !memberProperty(code, i) {
			out = append(out, idOcc{name: code[i:j], pos: i})
		}
		i = j
	}
	return out
}

// memberProperty reports whether the identifier starting at i is the property of
// a member access, a single `.` immediately before it. A `...` spread is not a
// member, so a `.` that is itself preceded by another `.` does not count.
func memberProperty(code string, i int) bool {
	k := i - 1
	for k >= 0 && (code[k] == ' ' || code[k] == '\t' || code[k] == '\n' || code[k] == '\r') {
		k--
	}
	if k < 0 || code[k] != '.' {
		return false
	}
	return k == 0 || code[k-1] != '.'
}

// maskLiterals replaces the interior of string literals, template literals, and
// comments with spaces, preserving length so byte offsets still line up. A
// masked region cannot contribute a for-head or an identifier, which keeps the
// walk from reading a `for` or a name written inside a string or a comment.
// Regex literals are left as they are: an identifier seen inside one only ever
// costs a hoist, never grants a wrong one, because the global occurrence scan
// counts it as a read and drops the candidate.
func maskLiterals(src string) string {
	b := []byte(src)
	n := len(b)
	blank := func(from, to int) {
		for k := from; k < to && k < n; k++ {
			if b[k] != '\n' {
				b[k] = ' '
			}
		}
	}
	for i := 0; i < n; {
		c := b[i]
		switch {
		case c == '/' && i+1 < n && b[i+1] == '/':
			j := i + 2
			for j < n && b[j] != '\n' {
				j++
			}
			blank(i, j)
			i = j
		case c == '/' && i+1 < n && b[i+1] == '*':
			j := i + 2
			for j+1 < n && !(b[j] == '*' && b[j+1] == '/') {
				j++
			}
			end := j + 2
			if end > n {
				end = n
			}
			blank(i, end)
			i = end
		case c == '\'' || c == '"':
			j := i + 1
			for j < n && b[j] != c {
				if b[j] == '\\' {
					j++
				}
				j++
			}
			blank(i+1, j)
			i = j + 1
		case c == '`':
			j := i + 1
			for j < n && b[j] != '`' {
				if b[j] == '\\' {
					j++
				}
				j++
			}
			blank(i+1, j)
			i = j + 1
		default:
			i++
		}
	}
	return string(b)
}

// forBindingTargets returns the byte offsets of the identifier tokens that are
// binding targets inside a for-of, for-in, or for-await-of head. The head's
// binding is the text between the `(` and the top-level `of` or `in`, a region
// that is a binding by grammar, so it can never be an array-index write. A
// C-style for, a head that declares its binding with var/let/const, and any
// pattern the walk cannot model contribute nothing.
func forBindingTargets(code string) map[int]bool {
	targets := map[int]bool{}
	n := len(code)
	for i := 0; i < n; {
		if !wordAt(code, i, "for") {
			i++
			continue
		}
		j := i + 3
		j = skipSpace(code, j)
		if wordAt(code, j, "await") {
			j = skipSpace(code, j+5)
		}
		if j >= n || code[j] != '(' {
			i += 3
			continue
		}
		open := j
		bindStart := open + 1
		bindEnd, ok := forBindingEnd(code, bindStart)
		if !ok {
			i = open + 1
			continue
		}
		collectPatternTargets(code, bindStart, bindEnd, targets)
		i = bindEnd
	}
	return targets
}

// forBindingEnd finds the end of the binding in a for-head that opened at the
// `(` before start. It returns the offset of the `of` or `in` keyword that
// closes the binding, and false when the head is a C-style for, has no such
// keyword, or runs off the end.
func forBindingEnd(code string, start int) (int, bool) {
	depth := 1
	n := len(code)
	for i := start; i < n; {
		c := code[i]
		switch c {
		case '(', '[', '{':
			depth++
			i++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return 0, false
			}
			i++
		case ';':
			if depth == 1 {
				return 0, false
			}
			i++
		default:
			if depth == 1 {
				if wordAt(code, i, "of") && boundBefore(code, i) {
					return i, true
				}
				if wordAt(code, i, "in") && boundBefore(code, i) {
					return i, true
				}
			}
			if isIdentStart(c) {
				i++
				for i < n && isIdentPart(code[i]) {
					i++
				}
				continue
			}
			i++
		}
	}
	return 0, false
}

// boundBefore reports whether the byte before i is a word boundary, so `of` in
// `off` or `in` in `bin` does not read as the loop keyword.
func boundBefore(code string, i int) bool {
	return i == 0 || !isIdentPart(code[i-1])
}

func wordAt(code string, i int, word string) bool {
	if i+len(word) > len(code) {
		return false
	}
	if code[i:i+len(word)] != word {
		return false
	}
	if i > 0 && isIdentPart(code[i-1]) {
		return false
	}
	after := i + len(word)
	return after >= len(code) || !isIdentPart(code[after])
}

func skipSpace(code string, i int) int {
	for i < len(code) && (code[i] == ' ' || code[i] == '\t' || code[i] == '\n' || code[i] == '\r') {
		i++
	}
	return i
}

// ptok is a token of a for-head binding: an identifier or a structural punct.
// Anything the walk does not model (an operator, a number, a stray char) is an
// "other" token, which the walk treats as opaque inside a default expression and
// as a reason to abort anywhere else.
type ptok struct {
	kind string // "ident", "punct", "other"
	text string
	pos  int
}

func tokenizePattern(code string, start, end int) []ptok {
	var toks []ptok
	for i := start; i < end; {
		c := code[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case isIdentStart(c):
			j := i + 1
			for j < end && isIdentPart(code[j]) {
				j++
			}
			toks = append(toks, ptok{kind: "ident", text: code[i:j], pos: i})
			i = j
		case c == '.' && i+2 < end && code[i+1] == '.' && code[i+2] == '.':
			toks = append(toks, ptok{kind: "punct", text: "...", pos: i})
			i += 3
		case c == '=' && i+1 < end && (code[i+1] == '=' || code[i+1] == '>'):
			toks = append(toks, ptok{kind: "punct", text: string([]byte{c, code[i+1]}), pos: i})
			i += 2
		case strings.IndexByte("[]{}(),:=.", c) >= 0:
			toks = append(toks, ptok{kind: "punct", text: string(c), pos: i})
			i++
		default:
			toks = append(toks, ptok{kind: "other", text: string(c), pos: i})
			i++
		}
	}
	return toks
}

// frame is one bracket context of a destructuring pattern: an array element list
// or an object property list. atPropStart is only meaningful for an object and
// marks that the next token opens a fresh property, where an identifier is a key
// or a shorthand target and a `[` is a computed key. expectSep marks that the
// current element has a complete value, so the only tokens that may follow are a
// separator, a default `=`, or the frame's closer. Anything else, a `[` or a
// `.` or another value, is a member or call continuation like `x[k]` or `{}[k]`,
// which is a reference target with no plain name to bind, so the walk gives up.
type frame struct {
	kind        byte // '[' or '{'
	atPropStart bool
	expectSep   bool
}

// collectPatternTargets walks the binding text between start and end as a
// destructuring pattern and records the offset of every target identifier. It
// commits nothing unless the whole pattern parses, so a shape it cannot model,
// a computed key, a member target, an unbalanced bracket, leaves the binding
// with no targets and the test with its honest handback.
func collectPatternTargets(code string, start, end int, out map[int]bool) {
	toks := tokenizePattern(code, start, end)
	if len(toks) == 0 {
		return
	}
	// A bare binding is a single identifier. A member target like `a.b` has more
	// tokens and is not a name to hoist.
	if toks[0].kind == "ident" {
		if len(toks) == 1 {
			out[toks[0].pos] = true
		}
		return
	}
	if toks[0].text != "[" && toks[0].text != "{" {
		return
	}

	var found []int
	var stack []frame
	i := 0
	top := func() *frame {
		if len(stack) == 0 {
			return nil
		}
		return &stack[len(stack)-1]
	}
	for i < len(toks) {
		t := toks[i]
		f := top()
		switch {
		case t.text == "[":
			if f != nil && (f.expectSep || (f.kind == '{' && f.atPropStart)) {
				return // computed key, or a `[k]` member index after a value
			}
			stack = append(stack, frame{kind: '['})
			i++
		case t.text == "{":
			if f != nil && (f.expectSep || (f.kind == '{' && f.atPropStart)) {
				return
			}
			stack = append(stack, frame{kind: '{', atPropStart: true})
			i++
		case t.text == "]":
			if f == nil || f.kind != '[' {
				return
			}
			stack = stack[:len(stack)-1]
			if p := top(); p != nil {
				p.expectSep = true
			}
			i++
		case t.text == "}":
			if f == nil || f.kind != '{' {
				return
			}
			stack = stack[:len(stack)-1]
			if p := top(); p != nil {
				p.expectSep = true
			}
			i++
		case t.text == ",":
			if f != nil {
				f.expectSep = false
				if f.kind == '{' {
					f.atPropStart = true
				}
			}
			i++
		case t.text == "...":
			if f != nil && f.expectSep {
				return
			}
			i++
		case t.text == "=":
			if f == nil || !f.expectSep {
				return // a default only follows a complete target
			}
			i = skipDefault(toks, i+1)
		case t.kind == "ident":
			if f == nil || f.expectSep {
				return
			}
			if f.kind == '{' && f.atPropStart && i+1 < len(toks) && toks[i+1].text == ":" {
				f.atPropStart = false // key, value follows the colon
				i += 2
				continue
			}
			found = append(found, t.pos) // shorthand, array element, or object value target
			f.expectSep = true
			if f.kind == '{' {
				f.atPropStart = false
			}
			i++
		default:
			return
		}
	}
	if len(stack) != 0 {
		return
	}
	for _, p := range found {
		out[p] = true
	}
}

// skipDefault advances past a default-value expression that started after an `=`
// at j, tracking bracket depth so a comma or a closer inside the expression does
// not read as the end of the element. It returns the index of the separator or
// closer that ends the default, which the caller processes next.
func skipDefault(toks []ptok, j int) int {
	depth := 0
	for j < len(toks) {
		switch toks[j].text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			if depth == 0 {
				return j
			}
			depth--
		case ",":
			if depth == 0 {
				return j
			}
		}
		j++
	}
	return j
}
