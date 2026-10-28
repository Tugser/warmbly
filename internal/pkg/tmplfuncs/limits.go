package tmplfuncs

import (
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"
	"text/template/parse"
)

// MaxOutput bounds what one render of a user-authored template may write.
const MaxOutput = 1 << 20

// MaxSteps bounds how many writes one render may make, which bounds range iterations.
const MaxSteps = 1 << 18

// MaxRangeDepth bounds how deeply {{range}} blocks may nest.
const MaxRangeDepth = 2

// MaxVariables bounds how many variables one template may declare.
const MaxVariables = 64

// MaxArgs bounds how many arguments one command may pass.
const MaxArgs = 32

// ErrOutputLimit is returned when a render passes MaxOutput.
var ErrOutputLimit = errors.New("template output exceeds the size limit")

// ErrStepLimit is returned when a render passes MaxSteps.
var ErrStepLimit = errors.New("template render exceeds the step limit")

var (
	errRangeSource = errors.New("range only iterates over a field, like {{range .items}}")
	errRangeDepth  = fmt.Errorf("range blocks may nest at most %d deep", MaxRangeDepth)
	errTemplateUse = errors.New("{{template}} and {{block}} are not supported")
	errRootAssign  = errors.New("$ cannot be reassigned")
	errVariables   = fmt.Errorf("a template may declare at most %d variables", MaxVariables)
	errArgs        = fmt.Errorf("a function may take at most %d arguments", MaxArgs)
)

// Compile parses a user-authored template with the shared funcs and absent keys
// rendering as their zero value, refusing it when CheckLimits does.
func Compile(name, src string) (*template.Template, error) {
	t, err := template.New(name).Funcs(funcs).Option("missingkey=zero").Parse(src)
	if err != nil {
		return nil, err
	}
	if err := CheckLimits(t); err != nil {
		return nil, err
	}
	for _, tt := range t.Templates() {
		meterRanges(tt.Tree)
	}
	return t, nil
}

// CompileHTML is Compile for an HTML document: html/template escapes every
// value for the context it lands in, under the same funcs and limits.
func CompileHTML(name, src string) (*htmltemplate.Template, error) {
	t, err := htmltemplate.New(name).Funcs(funcs).Option("missingkey=zero").Parse(src)
	if err != nil {
		return nil, err
	}
	trees := make([]*parse.Tree, 0, 1)
	for _, tt := range t.Templates() {
		trees = append(trees, tt.Tree)
	}
	if err := checkTrees(trees); err != nil {
		return nil, err
	}
	for _, tree := range trees {
		meterRanges(tree)
	}
	return t, nil
}

// CheckLimits refuses constructs whose cost is not bounded by the data: a
// {{range}} over anything but a field of the data, {{range}} nested past
// MaxRangeDepth, {{template}} calls, reassigning $, more than MaxVariables
// declarations and commands with more than MaxArgs arguments.
func CheckLimits(t *template.Template) error {
	trees := make([]*parse.Tree, 0, 1)
	for _, tt := range t.Templates() {
		trees = append(trees, tt.Tree)
	}
	return checkTrees(trees)
}

func checkTrees(trees []*parse.Tree) error {
	vars := 0
	for _, tree := range trees {
		if tree == nil || tree.Root == nil {
			continue
		}
		if err := checkNode(tree.Root, 0, true, &vars); err != nil {
			return err
		}
	}
	return nil
}

// checkNode walks n; dotIsData is whether dot can only hold a value from the data.
func checkNode(n parse.Node, depth int, dotIsData bool, vars *int) error {
	switch x := n.(type) {
	case *parse.ListNode:
		if x == nil {
			return nil
		}
		for _, c := range x.Nodes {
			if err := checkNode(c, depth, dotIsData, vars); err != nil {
				return err
			}
		}
	case *parse.ActionNode:
		return checkPipe(x.Pipe, vars)
	case *parse.RangeNode:
		if err := checkPipe(x.Pipe, vars); err != nil {
			return err
		}
		if depth+1 > MaxRangeDepth {
			return errRangeDepth
		}
		if !isDataPath(x.Pipe, dotIsData) {
			return errRangeSource
		}
		if err := checkNode(x.List, depth+1, true, vars); err != nil {
			return err
		}
		return checkNode(x.ElseList, depth, dotIsData, vars)
	case *parse.IfNode:
		if err := checkPipe(x.Pipe, vars); err != nil {
			return err
		}
		if err := checkNode(x.List, depth, dotIsData, vars); err != nil {
			return err
		}
		return checkNode(x.ElseList, depth, dotIsData, vars)
	case *parse.WithNode:
		if err := checkPipe(x.Pipe, vars); err != nil {
			return err
		}
		if err := checkNode(x.List, depth, isDataPath(x.Pipe, dotIsData), vars); err != nil {
			return err
		}
		return checkNode(x.ElseList, depth, dotIsData, vars)
	case *parse.TemplateNode:
		return errTemplateUse
	}
	return nil
}

// checkPipe refuses a pipeline that declares or assigns $, declares past
// MaxVariables, or passes a command more than MaxArgs arguments.
func checkPipe(p *parse.PipeNode, vars *int) error {
	if p == nil {
		return nil
	}
	for _, v := range p.Decl {
		if len(v.Ident) > 0 && v.Ident[0] == "$" {
			return errRootAssign
		}
		if !p.IsAssign {
			*vars++
		}
	}
	if *vars > MaxVariables {
		return errVariables
	}
	for _, c := range p.Cmds {
		if len(c.Args) > MaxArgs+1 {
			return errArgs
		}
		for _, a := range c.Args {
			if sub, ok := a.(*parse.PipeNode); ok {
				if err := checkPipe(sub, vars); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// isDataPath reports whether a pipeline is a single reference into the data
// (.x, .x.y, $.x, $v.x), so ranging over it iterates at most the data's length.
func isDataPath(p *parse.PipeNode, dotIsData bool) bool {
	if p == nil || len(p.Cmds) != 1 || len(p.Cmds[0].Args) != 1 {
		return false
	}
	return isPathNode(p.Cmds[0].Args[0], dotIsData)
}

func isPathNode(n parse.Node, dotIsData bool) bool {
	switch x := n.(type) {
	case *parse.DotNode, *parse.FieldNode:
		return dotIsData
	case *parse.VariableNode:
		// A bare variable may hold a literal; one indexed into must be a map.
		return x.Ident[0] == "$" || len(x.Ident) > 1
	case *parse.ChainNode:
		return isPathNode(x.Node, dotIsData)
	}
	return false
}

// meterRanges opens every range body with an empty write, so each iteration
// counts against MaxSteps even when the body writes nothing.
func meterRanges(tree *parse.Tree) {
	if tree == nil {
		return
	}
	var walk func(n parse.Node)
	walk = func(n parse.Node) {
		switch x := n.(type) {
		case *parse.ListNode:
			if x == nil {
				return
			}
			for _, c := range x.Nodes {
				walk(c)
			}
		case *parse.RangeNode:
			walk(x.List)
			walk(x.ElseList)
			if x.List != nil {
				x.List.Nodes = append([]parse.Node{emptyText()}, x.List.Nodes...)
			}
		case *parse.IfNode:
			walk(x.List)
			walk(x.ElseList)
		case *parse.WithNode:
			walk(x.List)
			walk(x.ElseList)
		}
	}
	walk(tree.Root)
}

// emptyText returns a text node owned by a parsed tree, so copying it is safe.
func emptyText() parse.Node {
	trees, err := parse.Parse("step", " ", "", "")
	if err != nil {
		panic(err)
	}
	n := trees["step"].Root.Nodes[0].(*parse.TextNode)
	n.Text = n.Text[:0]
	return n
}

// Executor is a compiled text or HTML template.
type Executor interface {
	Execute(w io.Writer, data any) error
}

// Execute renders t into a string, failing once the output passes MaxOutput
// or the render passes MaxSteps.
func Execute(t Executor, data any) (string, error) {
	return ExecuteLimit(t, data, MaxOutput)
}

// ExecuteLimit is Execute with a smaller output budget, for callers sharing one.
func ExecuteLimit(t Executor, data any, limit int) (string, error) {
	w := &cappedWriter{max: min(limit, MaxOutput)}
	if err := t.Execute(w, data); err != nil {
		return "", err
	}
	return w.b.String(), nil
}

type cappedWriter struct {
	b     strings.Builder
	max   int
	steps int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	w.steps++
	if w.steps > MaxSteps {
		return 0, ErrStepLimit
	}
	if w.b.Len()+len(p) > w.max {
		return 0, ErrOutputLimit
	}
	return w.b.Write(p)
}

// IsLimit reports whether err is a render passing one of the limits.
func IsLimit(err error) bool {
	return errors.Is(err, ErrOutputLimit) || errors.Is(err, ErrStepLimit)
}

// CacheCap bounds how many compiled templates a Cache keeps.
const CacheCap = 4096

// Cache maps a template source to its compiled text template.
type Cache = TypedCache[*template.Template]

// TypedCache maps a template source to its compiled form, nil marking a source
// that failed to compile. Past CacheCap a miss is recompiled, not stored.
type TypedCache[T any] struct {
	m sync.Map
	n atomic.Int64
}

// Load returns the cached template for src and whether src was cached.
func (c *TypedCache[T]) Load(src string) (T, bool) {
	v, ok := c.m.Load(src)
	if !ok {
		var zero T
		return zero, false
	}
	t, _ := v.(T)
	return t, true
}

// Store caches t (nil for a known-bad source) unless the cache is full.
func (c *TypedCache[T]) Store(src string, t T) {
	if c.n.Load() >= CacheCap {
		return
	}
	if _, loaded := c.m.LoadOrStore(src, t); !loaded {
		c.n.Add(1)
	}
}
