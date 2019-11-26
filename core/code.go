package core

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"unsafe"
)

type (
	CodeEnv struct {
		codeWriterEnv    *CodeWriterEnv
		Namespace        *Namespace
		Definitions      map[*string]struct{}
		Symbols          []*string
		Strings          map[*string]uint16
		Bindings         map[*Binding]int
		nextStringIndex  uint16
		nextBindingIndex int
		statics          string
		interns          string
		runtime          []func() string
	}

	CodeWriterEnv struct {
		NeedSyms     map[*string]struct{}
		NeedStrs     map[string]struct{}
		NeedBindings map[string]*Binding
		NeedKeywords map[uint32]Keyword
		Generated    map[interface{}]interface{} // nil: being generated; else: fully generated (self)
	}

	EmitHeader struct {
		GlobalEnv *Env
		Strings   []*string
		Bindings  []Binding
	}
)

func NewCodeEnv(cwe *CodeWriterEnv) *CodeEnv {
	return &CodeEnv{
		codeWriterEnv: cwe,
		Namespace:     GLOBAL_ENV.CoreNamespace,
		Definitions:   make(map[*string]struct{}),
		Symbols:       []*string{},
		Strings:       make(map[*string]uint16),
		Bindings:      make(map[*Binding]int),
	}
}

var tr = [][2]string{
	{"_", "US"},
	{"?", "Q"},
	{"!", "BANG"},
	{"<=", "LE"},
	{">=", "GE"},
	{"<", "LT"},
	{">", "GT"},
	{"=", "EQ"},
	{"'", "APOS"},
	{"+", "PLUS"},
	{"-", "DASH"},
	{"*", "STAR"},
	{"/", "SLASH"},
	{"&", "AMP"},
	{"#", "HASH"},
	{".", "DOT"},
	{"%", "PCT"},
	{".", "DOT"},
}

func NameAsGo(name string) string {
	for _, t := range tr {
		name = strings.ReplaceAll(name, t[0], "_"+t[1]+"_")
	}
	return name
}

func noBang(s string) string {
	if len(s) > 0 && s[0] == '!' {
		return s[1:]
	}
	return s
}

func indirect(s string) string {
	if s[0] == '&' {
		return s[1:]
	}
	if s[0] == '!' {
		return s
	}
	return "*" + s
}

func notNil(s string) bool {
	return s != "" && s != "nil" && !strings.HasSuffix(s, "{}")
}

func uniqueName(target, prefix, f string, id interface{}) string {
	if strings.Contains(target, ".") {
		return fmt.Sprintf("%s"+f, prefix, id)
	}
	return prefix + target
}

func coreType(e interface{}) string {
	return strings.Replace(fmt.Sprintf("%T", e), "core.", "", 1)
}

func assertType(e interface{}) string {
	return ".(" + coreType(e) + ")"
}

func joinStringFns(fns []func() string) string {
	strs := make([]string, len(fns))
	for ix, fn := range fns {
		strs[ix] = fn()
	}
	return strings.Join(strs, "")
}

func isEmpty(s string) bool {
	return s == "" || (s[0:2] == "/*" && s[len(s)-2:] == "*/")
}

func maybeEmpty(s string, obj interface{}) string {
	if !isEmpty(s) {
		return ""
	}
	return fmt.Sprintf("// (%T) ", obj)
}

func makeTypedTarget(target string, typedTarget bool, typeStr string) string {
	if typedTarget {
		return target
	}
	return target + typeStr
}

func metaHolder(target string, m Map, env *CodeEnv) string {
	res := noBang(emitMap(target+".meta", false, m, env))
	if isEmpty(res) {
		return res
	}
	return fmt.Sprintf(`
	MetaHolder: MetaHolder{meta: %s},`[1:],
		res)
}

func metaHolderField(target string, m MetaHolder, fields []string, env *CodeEnv) []string {
	f := metaHolder(target, m.meta, env)
	if isEmpty(f) {
		return fields
	}
	return append(fields, f)
}

func infoHolder(target string, i InfoHolder, env *CodeEnv) string {
	res := noBang(i.info.Emit(target+".info", env))
	if isEmpty(res) {
		return res
	}
	return fmt.Sprintf(`
	InfoHolder: InfoHolder{info: %s},`[1:],
		res)
}

func infoHolderField(target string, m InfoHolder, fields []string, env *CodeEnv) []string {
	f := infoHolder(target, m, env)
	if isEmpty(f) {
		return fields
	}
	return append(fields, f)
}

func emitString(s *string, env *CodeEnv) string {
	if s == nil {
		return "nil"
	}
	env.codeWriterEnv.NeedStrs[*s] = struct{}{}
	return "s_" + NameAsGo(*s)
}

func directAssign(target string) string {
	cmp := strings.Split(target, ".")
	if len(cmp) < 2 {
		return target
	}
	final := cmp[len(cmp)-1]
	if final[0] == '(' && final[len(final)-1] == ')' {
		if len(cmp) > 2 {
			penultimate := cmp[len(cmp)-2]
			if penultimate[0] == '(' && penultimate[len(final)-1] == ')' {
				panic(fmt.Sprintf("directAssign(\"%s\")", target))
			}
		}
		return strings.Join(cmp[:len(cmp)-1], ".")
	}
	return target
}

func (b *Binding) SymName() *string {
	return b.name.name
}

func (b *Binding) UniqueId() string {
	isUsed := ""
	if b.IsUsed() {
		isUsed = "_used"
	}
	return fmt.Sprintf("%s_%d_%d%s", *b.SymName(), b.Index(), b.Frame(), isUsed)
}

func (b *Binding) Index() int {
	return b.index
}

func (b *Binding) Frame() int {
	return b.frame
}

func (b *Binding) IsUsed() bool {
	return b.isUsed
}

func (b *Binding) Emit(target string, env *CodeEnv) string {
	id := NameAsGo(b.UniqueId())
	env.codeWriterEnv.NeedBindings[id] = b
	return fmt.Sprintf("&binding_%s", id)
}

func (env *CodeEnv) AddForm(o Object) {
	seq, ok := o.(Seq)
	if !ok {
		fmt.Printf("code.go: Skipping %s\n", o.ToString(false))
		return
	}
	first := seq.First()
	if v, ok := first.(Symbol); ok {
		switch v.ToString(false) {
		case "def", "defn", "defn-", "defmacro", "defonce", "defmulti", "defmethod":
			for {
				seq = seq.Rest()
				if seq == nil {
					break
				}
				next := seq.First()
				if sym, ok := next.(Symbol); ok && v.ns == nil && v.name != nil {
					if _, ok := env.Definitions[sym.name]; ok {
					} else {
						env.Symbols = append(env.Symbols, sym.name)
						env.Definitions[sym.name] = struct{}{}
					}
					return
				}
				fmt.Printf("code.go: strange symbol name in %s\n", v.ToString(false))
			}
		case "add-doc-and-meta", "set-macro__", "joker.core/refer":
			return // Reflected, after evaluation, in final version of form
		case "ns", "in-ns":
			fmt.Printf("At %s\n", o.ToString(false))
			seq = seq.Rest()
			if l, ok := seq.First().(*List); ok {
				if q, ok := l.First().(Symbol); !ok || *q.name != "quote" {
					fmt.Printf("code.go: unexpected form where namespace expected: %s\n", l.ToString(false))
					return
				}
				env.Namespace = GLOBAL_ENV.EnsureNamespace(l.Second().(Symbol))
			} else {
				env.Namespace = GLOBAL_ENV.EnsureNamespace(seq.First().(Symbol))
			}
			return
		case "comment":
			return // Ok to ignore
		default:
			panic(fmt.Sprintf("%s unsupported", v.ToString(false))) // TODO: implement these (doseq, ns-unmap, etc.)
		}
	}
	fmt.Printf("code.go: Ignoring %s\n", o.ToString(false))
}

func (env *CodeEnv) Emit() {
	statics := ""
	interns := fmt.Sprintf(`
	_ns := GLOBAL_ENV.CurrentNamespace()
`[1:],
	)
	for ix, s := range env.Symbols {
		v, ok := env.Namespace.mappings[s]
		if !ok {
			fmt.Printf("code.go: cannot find %s [%d] in %s\n", *s, ix, *env.Namespace.Name.name)
			continue
		}

		name := NameAsGo(*s)

		v_var := ""

		if v.Value != nil {
			v_value := indirect(emitInterface(fmt.Sprintf("v_%s.Value.(%s)", name, coreType(v.Value)), true, v.Value, env))
			intermediary := v_value[1:]
			if v_value[0] != '!' {
				intermediary = fmt.Sprintf("&value_%s", name)
				statics += fmt.Sprintf(`
var value_%s = %s
`[1:],
					name, v_value)
			}
			v_var += fmt.Sprintf(`
	Value: %s,
`[1:],
				intermediary)
		}

		if v.expr != nil {
			v_expr := indirect(v.expr.Emit("expr_"+name, env))
			intermediary := v_expr[1:]
			if v_expr[0] != '!' {
				intermediary = fmt.Sprintf("&expr_%s", name)
				statics += fmt.Sprintf(`
var expr_%s = %s
`[1:],
					name, v_expr)
			}
			v_var += fmt.Sprintf(`
	expr: %s,
`[1:],
				intermediary)
		}

		if v.isMacro {
			v_var += fmt.Sprintf(`
	isMacro: true,
`[1:])
		}

		if v.isPrivate {
			v_var += fmt.Sprintf(`
	isPrivate: true,
`[1:])
		}

		if v.isDynamic {
			v_var += fmt.Sprintf(`
	isDynamic: true,
`[1:])
		}

		if v.isUsed {
			v_var += fmt.Sprintf(`
	isUsed: true,
`[1:])
		}

		if v.isGloballyUsed {
			v_var += fmt.Sprintf(`
	isGloballyUsed: true,
`[1:])
		}

		v_tt := v.taggedType.Emit(fmt.Sprintf(`v_%s.taggedType`, name), env)
		if notNil(v_tt) {
			intermediary := v_tt[1:]
			if v_tt[0] != '!' {
				intermediary = fmt.Sprintf("&taggedType_%s", name)
				statics += fmt.Sprintf(`
var taggedType_%s = %s
`[1:],
					v_tt)
			}
			v_var += fmt.Sprintf(`
	%staggedType: %s,
`[1:],
				maybeEmpty(v_tt, v.taggedType), intermediary)
		}

		if !isEmpty(v_var) {
			v_var = `
` + v_var + `
`
		}
		info := infoHolder("v_"+name, v.InfoHolder, env)
		if info != "" {
			info = "\n" + info
		}
		meta := metaHolder("v_"+name, v.meta, env)
		if meta != "" {
			meta = "\n" + meta
		}
		v_var = fmt.Sprintf(`
var v_%s = Var{%s%s%s}
var p_v_%s = &v_%s
`[1:],
			name, info, meta, v_var, name, name)
		env.codeWriterEnv.Generated[v] = v

		env.codeWriterEnv.NeedSyms[s] = struct{}{}
		interns += fmt.Sprintf(`
	_ns.InternExistingVar(sym_%s, &v_%s)
`,
			name, name)

		statics += v_var
	}

	env.statics += statics
	env.interns += interns + joinStringFns(env.runtime)
}

func (p Position) Hash() uint32 {
	h := getHash()
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, uint64(p.endLine))
	h.Write(b)
	binary.LittleEndian.PutUint64(b, uint64(p.endColumn))
	h.Write(b)
	binary.LittleEndian.PutUint64(b, uint64(p.startLine))
	h.Write(b)
	binary.LittleEndian.PutUint64(b, uint64(p.startColumn))
	h.Write(b)
	h.Write([]byte(*p.filename))
	return h.Sum32()
}

func (p Position) Emit(target string, env *CodeEnv) string {
	fields := []string{}
	if p.endLine != 0 {
		fields = append(fields, fmt.Sprintf(`
	endLine: %d,`[1:],
			p.endLine))
	}
	if p.endColumn != 0 {
		fields = append(fields, fmt.Sprintf(`
	endColumn: %d,`[1:],
			p.endColumn))
	}
	if p.startLine != 0 {
		fields = append(fields, fmt.Sprintf(`
	startLine: %d,`[1:],
			p.startLine))
	}
	if p.startColumn != 0 {
		fields = append(fields, fmt.Sprintf(`
	startColumn: %d,`[1:],
			p.startColumn))
	}
	f := noBang(emitString(p.filename, env))
	if notNil(f) {
		fields = append(fields, fmt.Sprintf(`
	filename: %s,`[1:],
			f))
	}
	f = strings.Join(fields, "\n")
	if f != "" {
		f = "\n" + f + "\n"
	}
	return fmt.Sprintf(`Position{%s}`, f)
}

func (info *ObjectInfo) Emit(target string, env *CodeEnv) string {
	if info == nil {
		return "nil"
	}
	name := uniqueName(target, "objectInfo_", "%p", info)
	if _, ok := env.codeWriterEnv.Generated[info]; !ok {
		env.codeWriterEnv.Generated[info] = info
		fields := []string{}
		f := noBang(info.Position.Emit(name+".Position", env))
		if f != "" {
			fields = append(fields, f+",")
		}
		f = strings.Join(fields, "\n")
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = ObjectInfo{%s}
`,
			name, f)
	}
	return "!&" + name
}

func (s Symbol) Emit(target string, env *CodeEnv) string {
	if s.name == nil {
		if s.ns == nil && s.hash == 0 {
			return ""
		}
		return "Symbol{ABEND: No name!!}"
	}

	env.codeWriterEnv.NeedSyms[s.name] = struct{}{}

	sym := fmt.Sprintf("sym_%s", NameAsGo(*s.name))

	env.runtime = append(env.runtime, func() string {
		return fmt.Sprintf(`
	%s = %s
`[1:],
			directAssign(target), sym)
	})
	return "!Symbol{}"
}

func (t *Type) Emit(target string, env *CodeEnv) string {
	if t == nil {
		return "nil"
	}
	name := NameAsGo(t.name)
	env.codeWriterEnv.NeedStrs[t.name] = struct{}{}
	typeFn := func() string {
		return fmt.Sprintf(`
	%s = TYPES[s_%s]
`[1:],
			directAssign(target), name)
	}
	env.runtime = append(env.runtime, typeFn)
	return "nil"
}

func emitProc(target string, p Proc, env *CodeEnv) string {
	return "!" + p.name
}

func (le *LocalEnv) Hash() uint32 {
	return HashPtr(uintptr(unsafe.Pointer(le)))
}

func (le *LocalEnv) Emit(target string, env *CodeEnv) string {
	name := uniqueName(target, "localEnv_", "%d", le.Hash())
	if _, ok := env.codeWriterEnv.Generated[le]; !ok {
		env.codeWriterEnv.Generated[le] = le
		fields := []string{}
		f := deferObjectSeq(name+".bindings", le.bindings, env)
		if f != "" {
			f = fmt.Sprintf("\t%sbindings: %s,", maybeEmpty(f, le.bindings), f)
		}
		fields = append(fields, f)
		if le.parent != nil {
			f := noBang(le.parent.Emit(name+".parent", env))
			if f != "" {
				fields = append(fields, fmt.Sprintf("\t%sparent: %s,", maybeEmpty(f, le.parent), f))
			}
		}
		if le.frame != 0 {
			fields = append(fields, fmt.Sprintf("\tframe: %d,", le.frame))
		}
		f = strings.Join(fields, "\n")
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = LocalEnv{%s}
`,
			name, f)
	}
	return "!&" + name
}

func emitFn(target string, fn *Fn, env *CodeEnv) string {
	name := uniqueName(target, "fn_", "%d", fn.Hash())
	if _, ok := env.codeWriterEnv.Generated[name]; !ok {
		env.codeWriterEnv.Generated[name] = fn
		fields := []string{}
		fields = infoHolderField(name, fn.InfoHolder, fields, env)
		fields = metaHolderField(name, fn.MetaHolder, fields, env)
		if fn.isMacro {
			fields = append(fields, "\tisMacro: true,")
		}
		if fn.fnExpr != nil {
			f := noBang(fn.fnExpr.Emit(name+".fnExpr", env))
			if f != "" {
				fields = append(fields, fmt.Sprintf("\t%sfnExpr: %s,", maybeEmpty(f, fn.fnExpr), f))
			}
		}
		if fn.env != nil {
			f := noBang(fn.env.Emit(name+".env", env))
			if f != "" {
				fields = append(fields, fmt.Sprintf("\t%senv: %s,", maybeEmpty(f, fn.env), f))
			}
		}
		f := strings.Join(fields, "\n")
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = Fn{%s%s}
`,
			name, metaHolder(name, fn.meta, env), f)
	}
	return "!&" + name
}

func (b Boolean) Emit(target string, env *CodeEnv) string {
	if b.B {
		return "!Boolean{B: true}"
	}
	return "!Boolean{B: false}"
}

func (m *MapSet) Emit(target string, env *CodeEnv) string {
	name := uniqueName(target, "mapset_", "%d", m.Hash())
	if _, ok := env.codeWriterEnv.Generated[m]; !ok {
		env.codeWriterEnv.Generated[m] = m
		f := noBang(emitMap(target+".m", false, m.m, env))
		if f != "" {
			f = fmt.Sprintf("\t%sm: %s,", maybeEmpty(f, m.m), f)
		}
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = MapSet{%s}
`,
			name, f)
	}
	return "!&" + name
}

func emitMap(target string, typedTarget bool, m Map, env *CodeEnv) string {
	switch m := m.(type) {
	case *ArrayMap:
		return m.Emit(makeTypedTarget(target, typedTarget, ".(*ArrayMap)"), env)
	case *HashMap:
		return m.Emit(makeTypedTarget(target, typedTarget, ".(*HashMap)"), env)
	case nil:
		return ""
	}
	return fmt.Sprintf("nil /*ABEND: %T*/", m)
}

func (l *List) Emit(target string, env *CodeEnv) string {
	name := uniqueName(target, "list_", "%d", l.Hash())
	if _, ok := env.codeWriterEnv.Generated[name]; !ok {
		env.codeWriterEnv.Generated[name] = nil
		fields := []string{}
		f := noBang(emitInterface(name+".first", false, l.first, env))
		if f != "" {
			fields = append(fields, fmt.Sprintf("\t%sfirst: %s,", maybeEmpty(f, l.first), f))
		}
		field := name + ".rest"
		if status, found := env.codeWriterEnv.Generated[l.rest]; l.rest != nil && (!found || status == nil) {
			fieldFn := func() string {
				return fmt.Sprintf(`
	%s = %s
`[1:],
					directAssign(field), noBang(l.rest.Emit(field, env)))
			}
			env.runtime = append(env.runtime, fieldFn)
		} else if l.rest != nil {
			f := noBang(l.rest.Emit(field, env))
			if f != "" {
				fields = append(fields, fmt.Sprintf("\t%srest: %s,", maybeEmpty(f, l.rest), f))
			}
		}
		if l.count != 0 {
			fields = append(fields, fmt.Sprintf("\tcount: %d,", l.count))
		}
		f = strings.Join(fields, "\n")
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = List{%s}
`,
			name, f)
		env.codeWriterEnv.Generated[name] = l
	}
	return "!&" + name
}

func (v *Vector) Emit(target string, env *CodeEnv) string {
	name := uniqueName(target, "vector_", "%p", v)
	if _, ok := env.codeWriterEnv.Generated[v]; !ok {
		env.codeWriterEnv.Generated[v] = v
		fields := []string{}
		fields = append(fields, fmt.Sprintf("\troot: %s,", emitInterfaceSeq(name+".root", v.root, env)))
		fields = append(fields, fmt.Sprintf("\ttail: %s,", emitInterfaceSeq(name+".tail", v.tail, env)))
		if v.count != 0 {
			fields = append(fields, fmt.Sprintf("\tcount: %d,", v.count))
		}
		if v.shift != 0 {
			fields = append(fields, fmt.Sprintf("\tshift: %d,", v.shift))
		}
		f := strings.Join(fields, "\n")
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = Vector{%s}
`,
			name, f)
	}
	return "!&" + name
}

func (v *VectorSeq) Emit(target string, env *CodeEnv) string {
	name := uniqueName(target, "vectorSeq_", "%p", v)
	if _, ok := env.codeWriterEnv.Generated[v]; !ok {
		env.codeWriterEnv.Generated[v] = v
		fields := []string{}
		f := noBang(v.vector.Emit(name+".root", env))
		if f != "" {
			fields = append(fields, fmt.Sprintf("\t%svector: %s,", maybeEmpty(f, v.vector), f))
		}
		if v.index != 0 {
			fields = append(fields, fmt.Sprintf("\tindex: %d,", v.index))
		}
		f = strings.Join(fields, "\n")
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = VectorSeq{%s%s}
`,
			name, metaHolder(name, v.meta, env), f)
	}
	return "!&" + name
}

func (m *ArrayMap) Emit(target string, env *CodeEnv) string {
	name := uniqueName(target, "arrayMap_", "%d", m.Hash())
	if _, ok := env.codeWriterEnv.Generated[m]; !ok {
		env.codeWriterEnv.Generated[m] = m
		f := emitObjectSeq(target+".arr", m.arr, env)
		if f != "" {
			f = fmt.Sprintf("\t%sarr: %s,", maybeEmpty(f, m.arr), f)
		}
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = ArrayMap{%s%s}
`,
			name, metaHolder(name, m.meta, env), f)
	}
	return "!&" + name
}

func (m *HashMap) Emit(target string, env *CodeEnv) string {
	name := uniqueName(target, "hashMap_", "%d", m.Hash())
	if _, ok := env.codeWriterEnv.Generated[m]; !ok {
		env.codeWriterEnv.Generated[m] = m
		fields := []string{}
		if m.count != 0 {
			fields = append(fields, fmt.Sprintf("\tcount: %d,", m.count))
		}
		f := noBang(emitInterface(target+".root", false, m.root, env))
		if f != "" {
			fields = append(fields, fmt.Sprintf("\t%sroot: %s,", maybeEmpty(f, m.root), f))
		}
		f = strings.Join(fields, "\n")
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = HashMap{%s%s}
`,
			name, metaHolder(name, m.meta, env), f)
	}
	return "!&" + name
}

func (b *BitmapIndexedNode) Emit(target string, env *CodeEnv) string {
	name := uniqueName(target, "bitmapIndexedNode_", "%p", b)
	if _, ok := env.codeWriterEnv.Generated[b]; !ok {
		env.codeWriterEnv.Generated[b] = b
		fields := []string{}
		if b.bitmap != 0 {
			fields = append(fields, fmt.Sprintf("\tbitmap: %d,", b.bitmap))
		}
		fields = append(fields, fmt.Sprintf("\tarray: %s,", emitInterfaceSeq(name+".array", b.array, env)))
		f := strings.Join(fields, "\n")
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = BitmapIndexedNode{%s}
`,
			name, f)
	}
	return "!&" + name
}

func (io *IOWriter) Emit(target string, env *CodeEnv) string {
	return "!(*IOWriter)(nil)"
}

func (ns *Namespace) Emit(target string, env *CodeEnv) string {
	if *ns.Name.name != "joker.core" {
		panic(fmt.Sprintf("code.go: (*Namespace)Emit() supports only ns=joker.core, not =%s\n", *ns.Name.name))
	}
	nsFn := func() string {
		return fmt.Sprintf("\t%s = _ns\n", directAssign(target))
	}
	env.runtime = append(env.runtime, nsFn)
	return "nil"
}

func (s String) Emit(target string, env *CodeEnv) string {
	name := uniqueName(target, "string_", "%d", s.Hash())
	if _, ok := env.codeWriterEnv.Generated[name]; !ok {
		env.codeWriterEnv.Generated[name] = s
		fields := []string{}
		fields = infoHolderField(name, s.InfoHolder, fields, env)
		fields = append(fields, fmt.Sprintf(`
	S: %s,`[1:],
			strconv.Quote(s.S)))
		f := strings.Join(fields, "\n")
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = String{%s}
`,
			name, f)
	}
	return "!" + name
}

func (k Keyword) NsField() *string {
	return k.ns
}

func (k Keyword) NameField() *string {
	return k.name
}

func (k Keyword) HashField() uint32 {
	return k.hash
}

func (k Keyword) UniqueId() string {
	name := NameAsGo(*k.NameField())
	if k.NsField() != nil {
		return NameAsGo(*k.NsField()) + "_FW_" + name
	}
	return name
}

func (k Keyword) Emit(target string, env *CodeEnv) string {
	if k.ns != nil {
		env.codeWriterEnv.NeedStrs[*k.ns] = struct{}{}

	}
	env.codeWriterEnv.NeedStrs[*k.name] = struct{}{}

	kwId := fmt.Sprintf("kw_%s", k.UniqueId())

	env.codeWriterEnv.NeedKeywords[k.hash] = k

	return fmt.Sprintf(`&%s`, kwId)
}

func (i Int) Emit(target string, env *CodeEnv) string {
	name := uniqueName(target, "int_", "%d", i.I)
	if _, ok := env.codeWriterEnv.Generated[i.I]; !ok {
		env.codeWriterEnv.Generated[i.I] = i
		fields := []string{}
		fields = infoHolderField(name, i.InfoHolder, fields, env)
		fields = append(fields, fmt.Sprintf(`
	I: %d,`[1:],
			i.I))
		f := strings.Join(fields, "\n")
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = Int{%s}
`,
			name, f)
	}
	return "!" + name
}

func (ch Char) Emit(target string, env *CodeEnv) string {
	name := uniqueName(target, "char_", "%d", ch.Ch)
	if _, ok := env.codeWriterEnv.Generated[ch.Ch]; !ok {
		env.codeWriterEnv.Generated[ch.Ch] = ch
		fields := []string{}
		fields = infoHolderField(name, ch.InfoHolder, fields, env)
		fields = append(fields, fmt.Sprintf(`
	Ch: '%c',`[1:],
			ch.Ch))
		f := strings.Join(fields, "\n")
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = Char{%s}
`,
			name, f)
	}
	return "!" + name
}

func (d Double) Emit(target string, env *CodeEnv) string {
	dValue := strconv.FormatFloat(d.D, 'g', -1, 64)
	name := uniqueName(target, "double_", "%s", NameAsGo(dValue))
	if _, ok := env.codeWriterEnv.Generated[name]; !ok {
		env.codeWriterEnv.Generated[name] = d
		fields := []string{}
		fields = infoHolderField(name, d.InfoHolder, fields, env)
		fields = append(fields, fmt.Sprintf(`
	D: %s,`[1:],
			dValue))
		f := strings.Join(fields, "\n")
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = Double{%s}
`,
			name, f)
	}
	return "!" + name
}

func (n Nil) Emit(target string, env *CodeEnv) string {
	var hash uint32
	if n.InfoHolder.info != nil {
		hash = n.InfoHolder.info.Position.Hash()
	}
	name := uniqueName(target, "nil_", "%d", hash)
	if _, ok := env.codeWriterEnv.Generated[name]; !ok {
		env.codeWriterEnv.Generated[name] = n
		fields := []string{}
		fields = infoHolderField(name, n.InfoHolder, fields, env)
		f := strings.Join(fields, "\n")
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = Nil{%s}
`,
			name, f)
	}
	return "!" + name
}

func emitInterface(target string, typedTarget bool, obj interface{}, env *CodeEnv) string {
	switch obj := obj.(type) {
	case Symbol:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(Symbol)"), env)
	case *Var:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(*Var)"), env)
	case *Type:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(*Type)"), env)
	case Proc:
		return emitProc(makeTypedTarget(target, typedTarget, ".(Proc)"), obj, env)
	case *Fn:
		return emitFn(makeTypedTarget(target, typedTarget, ".(*Fn)"), obj, env)
	case Boolean:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(Boolean)"), env)
	case *MapSet:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(*MapSet)"), env)
	case *List:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(*List)"), env)
	case *Vector:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(*Vector)"), env)
	case *VectorSeq:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(*VectorSeq)"), env)
	case *ArrayMap:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(*ArrayMap)"), env)
	case *HashMap:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(*HashMap)"), env)
	case *IOWriter:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(*IOWriter)"), env)
	case *Namespace:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(*Namespace)"), env)
	case *BitmapIndexedNode:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(*BitmapIndexedNode)"), env)
	case String:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(String)"), env)
	case Keyword:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(Keyword)"), env)
	case Int:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(Int)"), env)
	case Char:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(Char)"), env)
	case Double:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(Double)"), env)
	case Nil:
		return obj.Emit(makeTypedTarget(target, typedTarget, ".(Nil)"), env)
	default:
		return fmt.Sprintf("/*ABEND: unknown object type %T*/", obj)
	}
}

func (expr *LiteralExpr) Emit(target string, env *CodeEnv) string {
	name := uniqueName(target, "literalExpr_", "%p", expr)
	if _, ok := env.codeWriterEnv.Generated[expr]; !ok {
		env.codeWriterEnv.Generated[expr] = expr
		fields := []string{}
		f := expr.Position.Emit(name+".Position", env)
		if notNil(f) {
			fields = append(fields, fmt.Sprintf(`
	Position: %s,`[1:],
				f))
		}
		f = noBang(emitInterface(name+".obj", false, expr.obj, env))
		if notNil(f) {
			fields = append(fields, fmt.Sprintf(`
	obj: %s,`[1:],
				f))
		}
		if expr.isSurrogate {
			fields = append(fields, `
	isSurrogate: true,`[1:])
		}

		f = strings.Join(fields, "\n")
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = LiteralExpr{%s}
`,
			name, f)
	}
	return "!&" + name
}

func emitInterfaceSeq(target string, thingies []interface{}, env *CodeEnv) string {
	thingyae := []string{}
	for ix, thingy := range thingies {
		if thingy == nil {
			thingyae = append(thingyae, "\tnil, // Empty")
		} else {
			f := noBang(emitInterface(fmt.Sprintf("%s[%d]", target, ix), false, thingy, env))
			thingyae = append(thingyae, fmt.Sprintf("\t%s%s,", maybeEmpty(f, thingy), f))
		}
	}
	ret := strings.Join(thingyae, "\n")
	if !isEmpty(ret) {
		ret = "\n" + ret + "\n"
	}
	return fmt.Sprintf(`[]interface{}{%s}`, ret)
}

func emitSeq(target string, exprs []Expr, env *CodeEnv) string {
	exprae := []string{}
	for ix, expr := range exprs {
		exprae = append(exprae, "\t"+noBang(expr.Emit(fmt.Sprintf("%s[%d].(%s)", target, ix, coreType(expr)), env))+",")
	}
	ret := strings.Join(exprae, "\n")
	if !isEmpty(ret) {
		ret = "\n" + ret + "\n"
	}
	return fmt.Sprintf(`[]Expr{%s}`, ret)
}

func emitObjectSeq(target string, objs []Object, env *CodeEnv) string {
	objae := []string{}
	for ix, obj := range objs {
		objae = append(objae, "\t"+noBang(emitInterface(fmt.Sprintf("%s[%d]", target, ix), false, obj, env))+",")
	}
	ret := strings.Join(objae, "\n")
	if !isEmpty(ret) {
		ret = "\n" + ret + "\n"
	}
	return fmt.Sprintf(`[]Object{%s}`, ret)
}

func deferObjectSeq(target string, objs []Object, env *CodeEnv) string {
	objae := []string{}
	for ix, obj := range objs {
		objae = append(objae, fmt.Sprintf("\t(%s)(nil),", coreType(obj)))
		objFn := func() string {
			el := fmt.Sprintf("%s[%d]", target, ix)
			return fmt.Sprintf(`
	%s = %s
`[1:],
				directAssign(el), noBang(emitInterface(el, false, obj, env)))
		}
		env.runtime = append(env.runtime, objFn)
	}
	ret := strings.Join(objae, "\n")
	if !isEmpty(ret) {
		ret = "\n" + ret + "\n"
	}
	return fmt.Sprintf(`[]Object{%s}`, ret)
}

func emitSymbolSeq(target string, syms []Symbol, env *CodeEnv) string {
	symv := []string{}
	for ix, sym := range syms {
		symv = append(symv, "\t"+noBang(sym.Emit(fmt.Sprintf("%s[%d]", target, ix), env))+",")
	}
	ret := strings.Join(symv, "\n")
	if !isEmpty(ret) {
		ret = "\n" + ret + "\n"
	}
	return fmt.Sprintf(`[]Symbol{%s}`, ret)
}

func emitFnArityExprSeq(target string, fns []FnArityExpr, env *CodeEnv) string {
	fnae := []string{}
	for ix, fn := range fns {
		fnae = append(fnae, "\t"+indirect(noBang(fn.Emit(fmt.Sprintf("%s[%d]", target, ix), env)))+",")
	}
	ret := strings.Join(fnae, "\n")
	if !isEmpty(ret) {
		ret = "\n" + ret + "\n"
	}
	return fmt.Sprintf(`[]FnArityExpr{%s}`, ret)
}

func emitCatchExprSeq(target string, ces []*CatchExpr, env *CodeEnv) string {
	ceae := []string{}
	for ix, ce := range ces {
		ceae = append(ceae, "\t"+noBang(ce.Emit(fmt.Sprintf("%s[%d]", target, ix), env))+",")
	}
	ret := strings.Join(ceae, "\n")
	if !isEmpty(ret) {
		ret = "\n" + ret + "\n"
	}
	return fmt.Sprintf(`[]*CatchExpr{%s}`, ret)
}

func (expr *VectorExpr) Emit(target string, env *CodeEnv) string {
	return fmt.Sprintf(`&VectorExpr{
	v: %s,
}`,
		emitSeq(target+".v", expr.v, env))
}

func (expr *SetExpr) Emit(target string, env *CodeEnv) string {
	return fmt.Sprintf(`&SetExpr{
	elements: %s,
}`,
		emitSeq(target+".elements", expr.elements, env))
}

// func unpackSetExpr(p []byte, header *EmitHeader) (*SetExpr, []byte) {
// 	p = p[1:]
// 	pos, p := unpackPosition(p, header)
// 	v, p := unpackSeq(p, header)
// 	res := &SetExpr{
// 		Position: pos,
// 		elements: v,
// 	}
// 	return res, p
// }

func (expr *MapExpr) Emit(target string, env *CodeEnv) string {
	return fmt.Sprintf(`&MapExpr{
	keys: %s,
	values: %s,
}`,
		emitSeq(target+".keys", expr.keys, env),
		emitSeq(target+".values", expr.values, env))
}

// func unpackMapExpr(p []byte, header *EmitHeader) (*MapExpr, []byte) {
// 	p = p[1:]
// 	pos, p := unpackPosition(p, header)
// 	ks, p := unpackSeq(p, header)
// 	vs, p := unpackSeq(p, header)
// 	res := &MapExpr{
// 		Position: pos,
// 		keys:     ks,
// 		values:   vs,
// 	}
// 	return res, p
// }

func (expr *IfExpr) Emit(target string, env *CodeEnv) string {
	return fmt.Sprintf(`&IfExpr{
	cond: %s,
	positive: %s,
	negative: %s,
}`,
		noBang(expr.cond.Emit(target+".cond"+assertType(expr.cond), env)),
		noBang(expr.positive.Emit(target+".positive"+assertType(expr.positive), env)),
		noBang(expr.negative.Emit(target+".negative"+assertType(expr.negative), env)))
}

// func unpackIfExpr(p []byte, header *EmitHeader) (*IfExpr, []byte) {
// 	p = p[1:]
// 	pos, p := unpackPosition(p, header)
// 	cond, p := UnpackExpr(p, header)
// 	positive, p := UnpackExpr(p, header)
// 	negative, p := UnpackExpr(p, header)
// 	res := &IfExpr{
// 		Position: pos,
// 		positive: positive,
// 		negative: negative,
// 		cond:     cond,
// 	}
// 	return res, p
// }

// func (expr *DefExpr) Emit(target string, env *CodeEnv) string {
// 	// p = append(p, DEF_EXPR)
// 	// p = expr.Pos().Emit(p, env)
// 	// p = expr.name.Emit(p, env)
// 	// p = emitExprOrNil(expr.value, p, env)
// 	// p = emitExprOrNil(expr.meta, p, env)
// 	// p = expr.vr.info.Emit(p, env)
// 	// return p
// 	if expr.value == nil {
// 		return "" // just (declare name), which can be ignored here
// 	}

// 	name := NameAsGo(*expr.name.name)

// 	vr := noBang(expr.vr.Emit(target+".vr", env))
// 	if vr != "" {
// 		vr = fmt.Sprintf(`
// 	vr: %s,
// `[1:],
// 			vr)

// 	}

// 	initial := fmt.Sprintf(`
// &DefExpr{
// 	Position: %s,
// %s	name: %s,
// 	value: %s,
// 	meta: %s,
// 	}
// `[1:],
// 		name,
// 		noBang(expr.Pos().Emit(target+".Position", env)),
// 		vr,
// 		noBang(expr.name.Emit(target+".name", env)),
// 		noBang(emitExprOrNil(target+".value"+assertType(expr.value), expr.value, env)),
// 		noBang(emitExprOrNil(target+".meta"+assertType(expr.meta), expr.meta, env)))

// 	return initial
// }

// func unpackDefExpr(p []byte, header *EmitHeader) (*DefExpr, []byte) {
// 	p = p[1:]
// 	pos, p := unpackPosition(p, header)
// 	name, p := unpackSymbol(p, header)
// 	varName := name
// 	varName.ns = nil
// 	vr := header.GlobalEnv.CurrentNamespace().Intern(varName)
// 	value, p := UnpackExprOrNil(p, header)
// 	meta, p := UnpackExprOrNil(p, header)
// 	varInfo, p := unpackObjectInfo(p, header)
// 	updateVar(vr, varInfo, value, name)
// 	res := &DefExpr{
// 		Position: pos,
// 		vr:       vr,
// 		name:     name,
// 		value:    value,
// 		meta:     meta,
// 	}
// 	return res, p
// }

func (expr *CallExpr) Emit(target string, env *CodeEnv) string {
	// p = append(p, CALL_EXPR)
	// p = expr.Pos().Emit(p, env)
	// p = expr.callable.Emit(p, env)
	// p = packSeq(p, expr.args, env)
	// return p
	return fmt.Sprintf(`&CallExpr{
	callable: %s,
	args: %s,
}`,
		noBang(expr.callable.Emit(target+".callable"+assertType(expr.callable), env)),
		emitSeq(target+".args", expr.args, env))
}

// func unpackCallExpr(p []byte, header *EmitHeader) (*CallExpr, []byte) {
// 	p = p[1:]
// 	pos, p := unpackPosition(p, header)
// 	callable, p := UnpackExpr(p, header)
// 	args, p := unpackSeq(p, header)
// 	res := &CallExpr{
// 		Position: pos,
// 		callable: callable,
// 		args:     args,
// 	}
// 	return res, p
// }

func (expr *RecurExpr) Emit(target string, env *CodeEnv) string {
	return fmt.Sprintf(`&RecurExpr{
	args: %s,
}`,
		emitSeq(target+".args", expr.args, env))
}

func (vr *Var) Emit(target string, env *CodeEnv) string {
	sym := *vr.name.name
	g := NameAsGo(sym)
	env.codeWriterEnv.NeedStrs[sym] = struct{}{}

	runtimeDefineVarFn := func() string {
		/* Defer this logic until interns are generated during EOF handling. */
		if _, ok := env.codeWriterEnv.Generated[vr]; ok {
			return "\n"
		}

		env.codeWriterEnv.Generated[vr] = vr

		decl := fmt.Sprintf(`
var p_v_%s *Var
`[1:],
			g)
		env.statics += decl

		return fmt.Sprintf(`
	p_v_%s = GLOBAL_ENV.CoreNamespace.mappings[s_%s]
`,
			g, g)
	}
	env.runtime = append(env.runtime, runtimeDefineVarFn)

	runtimeAssignFn := func() string {
		return fmt.Sprintf(`
	%s = p_v_%s
`[1:],
			directAssign(target), g)
	}
	env.runtime = append(env.runtime, runtimeAssignFn)

	return ""
}

func (expr *VarRefExpr) Emit(target string, env *CodeEnv) string {
	name := uniqueName(target, "varRefExpr_", "%p", expr)
	if _, ok := env.codeWriterEnv.Generated[expr]; !ok {
		env.codeWriterEnv.Generated[expr] = expr
		fields := []string{}
		f := expr.Position.Emit(name+".Position", env)
		if notNil(f) {
			fields = append(fields, fmt.Sprintf(`
	Position: %s,`[1:], f))
		}
		f = noBang(expr.vr.Emit(name+".vr", env))
		if notNil(f) {
			fields = append(fields, fmt.Sprintf(`
	%svr: %s,`[1:], maybeEmpty(f, expr.vr), f))
		}
		f = strings.Join(fields, "\n")
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = VarRefExpr{%s}
`,
			name, f)
	}
	return "!&" + name
}

func (expr *SetMacroExpr) Emit(target string, env *CodeEnv) string {
	// p = append(p, SET_MACRO_EXPR)
	// p = expr.Pos().Emit(p, env)
	// p = expr.vr.Emit(p, env)
	// return p
	return "!(*SetMacroExpr)(nil)"
}

// func unpackSetMacroExpr(p []byte, header *EmitHeader) (*SetMacroExpr, []byte) {
// 	p = p[1:]
// 	pos, p := unpackPosition(p, header)
// 	vr, p := unpackVar(p, header)
// 	res := &SetMacroExpr{
// 		Position: pos,
// 		vr:       vr,
// 	}
// 	return res, p
// }

func (expr *BindingExpr) Emit(target string, env *CodeEnv) string {
	name := uniqueName(target, "bindingExpr_", "%p", expr)
	if _, ok := env.codeWriterEnv.Generated[expr]; !ok {
		env.codeWriterEnv.Generated[expr] = expr
		fields := []string{}
		f := expr.Position.Emit(name+".Position", env)
		if notNil(f) {
			fields = append(fields, fmt.Sprintf(`
	Position: %s,`[1:], f))
		}
		f = noBang(expr.binding.Emit(name+".binding", env))
		if notNil(f) {
			fields = append(fields, fmt.Sprintf(`
	%sbinding: %s,`[1:], maybeEmpty(f, expr.binding), f))
		}
		f = strings.Join(fields, "\n")
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = BindingExpr{%s}
`,
			name, f)
	}
	return "!&" + name
}

func (expr *MetaExpr) Emit(target string, env *CodeEnv) string {
	// p = append(p, META_EXPR)
	// p = expr.Pos().Emit(p, env)
	// p = expr.meta.Emit(p, env)
	// p = expr.expr.Emit(p, env)
	// return p
	return "ABEND(*MetaExpr)"
}

func (expr *DoExpr) Emit(target string, env *CodeEnv) string {
	name := uniqueName(target, "doExpr_", "%p", expr)
	if _, ok := env.codeWriterEnv.Generated[expr]; !ok {
		env.codeWriterEnv.Generated[expr] = expr
		fields := []string{}
		f := expr.Position.Emit(name+".Position", env)
		if notNil(f) {
			fields = append(fields, fmt.Sprintf(`
	Position: %s,`[1:], f))
		}
		f = emitSeq(name+".body", expr.body, env)
		if notNil(f) {
			fields = append(fields, fmt.Sprintf(`
	%sbody: %s,`[1:], maybeEmpty(f, expr.body), f))
		}
		f = strings.Join(fields, "\n")
		if !isEmpty(f) {
			f = "\n" + f + "\n"
		}
		env.statics += fmt.Sprintf(`
var %s = DoExpr{%s}
`,
			name, f)
	}
	return "!&" + name
}

func (expr *FnArityExpr) Emit(target string, env *CodeEnv) string {
	if expr == nil {
		return ""
	}

	res := fmt.Sprintf(`&FnArityExpr{
	args: %s,
	body: %s,
`,
		emitSymbolSeq(target+".args", expr.args, env),
		emitSeq(target+".body", expr.body, env))

	ty := noBang(expr.taggedType.Emit(target+".taggedType", env))
	if ty != "" {
		res += fmt.Sprintf(`
	%staggedType: %s,
`[1:],
			maybeEmpty(ty, expr.taggedType), ty)
	}

	return res + `}`
}

// func (expr *FnArityExpr) Emit(env *CodeEnv) string {
// 	// p = append(p, FN_ARITY_EXPR)
// 	// p = expr.Pos().Emit(p, env)
// 	// p = packSymbolSeq(p, expr.args, env)
// 	// p = packSeq(p, expr.body, env)
// 	// if expr.taggedType != nil {
// 	// 	p = append(p, NOT_NULL)
// 	// 	p = appendUint16(p, env.stringIndex(STRINGS.Intern(expr.taggedType.name)))
// 	// } else {
// 	// 	p = append(p, NULL)
// 	// }
// 	// return p
// 	return "!(*FnArityExpr)(nil)"
// }

// func unpackFnArityExpr(p []byte, header *EmitHeader) (*FnArityExpr, []byte) {
// 	p = p[1:]
// 	pos, p := unpackPosition(p, header)
// 	args, p := unpackSymbolSeq(p, header)
// 	body, p := unpackSeq(p, header)
// 	var taggedType *Type
// 	if p[0] == NULL {
// 		p = p[1:]
// 	} else {
// 		p = p[1:]
// 		var i uint16
// 		i, p = extractUInt16(p)
// 		taggedType = TYPES[header.Strings[i]]
// 	}
// 	res := &FnArityExpr{
// 		Position:   pos,
// 		body:       body,
// 		args:       args,
// 		taggedType: taggedType,
// 	}
// 	return res, p
// }

func (expr *FnExpr) Emit(target string, env *CodeEnv) string {
	variadic := ""
	if expr.variadic != nil {
		variadic = fmt.Sprintf(`
	variadic: %s,
`[1:],
			noBang(emitExprOrNil(target+".variadic", expr.variadic, env)))
	}

	expr.self.Emit(target+".self", env)

	return fmt.Sprintf(`&FnExpr{
	arities: %s,
%s}`,
		emitFnArityExprSeq(target+".arities", expr.arities, env),
		variadic)
}

// func unpackFnExpr(p []byte, header *EmitHeader) (*FnExpr, []byte) {
// 	p = p[1:]
// 	pos, p := unpackPosition(p, header)
// 	arities, p := unpackFnArityExprSeq(p, header)
// 	var variadic *FnArityExpr
// 	if p[0] == NULL {
// 		p = p[1:]
// 	} else {
// 		p = p[1:]
// 		variadic, p = unpackFnArityExpr(p, header)
// 	}
// 	self, p := unpackSymbol(p, header)
// 	res := &FnExpr{
// 		Position: pos,
// 		arities:  arities,
// 		variadic: variadic,
// 		self:     self,
// 	}
// 	return res, p
// }

func (expr *LetExpr) Emit(target string, env *CodeEnv) string {
	return fmt.Sprintf(`&LetExpr{
	names: %s,
	values: %s,
	body: %s,
}`,
		emitSymbolSeq(target+".names", expr.names, env),
		emitSeq(target+".values", expr.values, env),
		emitSeq(target+".body", expr.body, env))
}

// func unpackLetExpr(p []byte, header *EmitHeader) (*LetExpr, []byte) {
// 	p = p[1:]
// 	pos, p := unpackPosition(p, header)
// 	names, p := unpackSymbolSeq(p, header)
// 	values, p := unpackSeq(p, header)
// 	body, p := unpackSeq(p, header)
// 	res := &LetExpr{
// 		Position: pos,
// 		names:    names,
// 		values:   values,
// 		body:     body,
// 	}
// 	return res, p
// }

func (expr *LoopExpr) Emit(target string, env *CodeEnv) string {
	return fmt.Sprintf(`&LoopExpr{
	names: %s,
	values: %s,
	body: %s,
}`,
		emitSymbolSeq(target+".names", expr.names, env),
		emitSeq(target+".values", expr.values, env),
		emitSeq(target+".body", expr.body, env))
}

// func unpackLoopExpr(p []byte, header *EmitHeader) (*LoopExpr, []byte) {
// 	p = p[1:]
// 	pos, p := unpackPosition(p, header)
// 	names, p := unpackSymbolSeq(p, header)
// 	values, p := unpackSeq(p, header)
// 	body, p := unpackSeq(p, header)
// 	res := &LoopExpr{
// 		Position: pos,
// 		names:    names,
// 		values:   values,
// 		body:     body,
// 	}
// 	return res, p
// }

func (expr *ThrowExpr) Emit(target string, env *CodeEnv) string {
	return fmt.Sprintf(`&ThrowExpr{
	e: %s,
}`,
		noBang(expr.e.Emit(target+".e"+assertType(expr.e), env)))
}

// func unpackThrowExpr(p []byte, header *EmitHeader) (*ThrowExpr, []byte) {
// 	p = p[1:]
// 	pos, p := unpackPosition(p, header)
// 	e, p := UnpackExpr(p, header)
// 	res := &ThrowExpr{
// 		Position: pos,
// 		e:        e,
// 	}
// 	return res, p
// }

func (expr *CatchExpr) Emit(target string, env *CodeEnv) string {
	fields := []string{}

	f := noBang(expr.excType.Emit(target+".excType", env))
	if notNil(f) {
		fields = append(fields, fmt.Sprintf(`
	excType: %s,`[1:],
			f))
	}

	f = noBang(expr.excSymbol.Emit(target+".excSymbol", env))
	if f != "Symbol{}" {
		fields = append(fields, fmt.Sprintf(`
	excSymbol: %s,`[1:],
			f))
	}

	f = emitSeq(target+".body", expr.body, env)
	if f != "" {
		fields = append(fields, fmt.Sprintf(`
	body: %s,`[1:],
			f))
	}

	f = strings.Join(fields, "\n")
	if f != "" {
		f = "\n" + f + "\n"
	}

	return fmt.Sprintf(`&CatchExpr{%s}`, f)
}

// func unpackCatchExpr(p []byte, header *EmitHeader) (*CatchExpr, []byte) {
// 	p = p[1:]
// 	pos, p := unpackPosition(p, header)
// 	i, p := extractUInt16(p)
// 	typeName := header.Strings[i]
// 	excSymbol, p := unpackSymbol(p, header)
// 	body, p := unpackSeq(p, header)
// 	res := &CatchExpr{
// 		Position:  pos,
// 		excSymbol: excSymbol,
// 		body:      body,
// 		excType:   TYPES[typeName],
// 	}
// 	return res, p
// }

func (expr *TryExpr) Emit(target string, env *CodeEnv) string {
	return fmt.Sprintf(`&TryExpr{
	body: %s,
	catches: %s,
	finallyExpr: %s,
}`,
		emitSeq(target+".body", expr.body, env),
		emitCatchExprSeq(target+".catches", expr.catches, env),
		emitSeq(target+".finallyExpr", expr.finallyExpr, env))
}

// func unpackTryExpr(p []byte, header *EmitHeader) (*TryExpr, []byte) {
// 	p = p[1:]
// 	pos, p := unpackPosition(p, header)
// 	body, p := unpackSeq(p, header)
// 	catches, p := unpackCatchExprSeq(p, header)
// 	finallyExpr, p := unpackSeq(p, header)
// 	res := &TryExpr{
// 		Position:    pos,
// 		body:        body,
// 		catches:     catches,
// 		finallyExpr: finallyExpr,
// 	}
// 	return res, p
// }