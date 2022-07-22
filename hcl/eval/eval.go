// Copyright 2021 Mineiros GmbH
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package eval

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2/ext/customdecode"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/mineiros-io/terramate/errors"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"

	hhcl "github.com/hashicorp/hcl/v2"
	tflang "github.com/hashicorp/terraform/lang"
)

// ErrEval indicates a failure during the evaluation process
const ErrEval errors.Kind = "failed to evaluate expression"

// Context is used to evaluate HCL code.
type Context struct {
	hclctx *hhcl.EvalContext
}

// NewContext creates a new HCL evaluation context.
// The basedir is the base directory used by any interpolation functions that
// accept filesystem paths as arguments.
// The basedir must be an absolute path to a directory.
func NewContext(basedir string) (*Context, error) {
	if !filepath.IsAbs(basedir) {
		panic(fmt.Errorf("context created with relative path: %q", basedir))
	}

	st, err := os.Stat(basedir)
	if err != nil {
		return nil, errors.E(err, "failed to stat context basedir %q", basedir)
	}
	if !st.IsDir() {
		return nil, errors.E("context basedir (%s) must be a directory", basedir)
	}

	hclctx := &hhcl.EvalContext{
		Functions: newTmFunctions(basedir),
		Variables: map[string]cty.Value{},
	}
	return &Context{
		hclctx: hclctx,
	}, nil
}

// SetNamespace will set the given values inside the given namespace on the
// evaluation context.
func (c *Context) SetNamespace(name string, vals map[string]cty.Value) {
	c.hclctx.Variables[name] = cty.ObjectVal(vals)
}

// DeleteNamespace deletes the namespace name from the context.
// If name is not in the context, it's a no-op.
func (c *Context) DeleteNamespace(name string) {
	delete(c.hclctx.Variables, name)
}

// HasNamespace returns true the evaluation context knows this namespace, false otherwise.
func (c *Context) HasNamespace(name string) bool {
	_, has := c.hclctx.Variables[name]
	return has
}

// Eval will evaluate an expression given its context.
func (c *Context) Eval(expr hclsyntax.Expression) (cty.Value, error) {
	val, diag := expr.Value(c.hclctx)
	if diag.HasErrors() {
		return cty.NilVal, errors.E(ErrEval, diag)
	}
	return val, nil
}

// PartialEval evaluates only the terramate variable expressions from the list
// of tokens, leaving all the rest as-is. It returns a modified list of tokens
// with  no reference to terramate namespaced variables (globals and terramate)
// and functions (tm_ prefixed functions).
func (c *Context) PartialEval(expr hclsyntax.Expression) (hclwrite.Tokens, error) {
	exprFname := expr.Range().Filename
	filedata, err := ioutil.ReadFile(exprFname)
	if err != nil {
		return nil, errors.E(err, "reading expression from file")
	}

	tokens, err := GetExpressionTokens(filedata, exprFname, expr)
	if err != nil {
		return nil, err
	}

	engine := newPartialEvalEngine(tokens, c)
	return engine.Eval()
}

// GetExpressionTokens gets the provided expression as writable tokens.
func GetExpressionTokens(hcldoc []byte, filename string, expr hclsyntax.Expression) (hclwrite.Tokens, error) {
	exprRange := expr.Range()
	exprBytes := hcldoc[exprRange.Start.Byte:exprRange.End.Byte]
	tokens, diags := hclsyntax.LexExpression(exprBytes, filename, hhcl.Pos{})
	if diags.HasErrors() {
		return nil, errors.E(diags, "failed to scan expression")
	}
	return toWriteTokens(tokens), nil
}

func toWriteTokens(in hclsyntax.Tokens) hclwrite.Tokens {
	tokens := make([]*hclwrite.Token, len(in))
	for i, st := range in {
		tokens[i] = &hclwrite.Token{
			Type:  st.Type,
			Bytes: st.Bytes,
		}
	}
	return tokens
}

func newTmFunctions(basedir string) map[string]function.Function {
	scope := &tflang.Scope{BaseDir: basedir}
	tffuncs := scope.Functions()

	tmfuncs := map[string]function.Function{}
	for name, function := range tffuncs {
		tmfuncs["tm_"+name] = function
	}

	// fix terraform broken abspath()
	tmfuncs["tm_abspath"] = tmAbspath(basedir)
	tmfuncs["tm_try"] = tmTry(tffuncs)
	return tmfuncs
}

// tmAbspath returns the `tm_abspath()` hcl function.
func tmAbspath(basedir string) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{
				Name: "path",
				Type: cty.String,
			},
		},
		Type: function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			path := args[0].AsString()
			var abspath string
			if filepath.IsAbs(path) {
				abspath = path
			} else {
				abspath = filepath.Join(basedir, path)
			}

			return cty.StringVal(filepath.ToSlash(filepath.Clean(abspath))), nil
		},
	})
}

func tmTry(funcs map[string]function.Function) function.Function {
	return function.New(&function.Spec{
		VarParam: &function.Parameter{
			Name: "expressions",
			Type: customdecode.ExpressionClosureType,
		},
		Type: func(args []cty.Value) (cty.Type, error) {
			v, err := try(funcs, args)
			if err != nil {
				return cty.NilType, err
			}
			return v.Type(), nil
		},
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			return try(funcs, args)
		},
	})
}

func try(funcs map[string]function.Function, args []cty.Value) (cty.Value, error) {
	if len(args) == 0 {
		return cty.NilVal, errors.E("at least one argument is required")
	}

	// We'll collect up all of the diagnostics we encounter along the way
	// and report them all if none of the expressions succeed, so that the
	// user might get some hints on how to make at least one succeed.
	var diags hhcl.Diagnostics
	for _, arg := range args {
		closure := customdecode.ExpressionClosureFromVal(arg)

		err := check(funcs, closure.Expression)
		if err != nil {
			return cty.NilVal, err
		}

		if dependsOnUnknowns(closure.Expression, closure.EvalContext) {
			// We can't safely decide if this expression will succeed yet,
			// and so our entire result must be unknown until we have
			// more information.
			return cty.DynamicVal, nil
		}

		v, moreDiags := closure.Value()
		diags = append(diags, moreDiags...)
		if moreDiags.HasErrors() {
			continue // try the next one, if there is one to try
		}
		return v, nil // ignore any accumulated diagnostics if one succeeds
	}

	// If we fall out here then none of the expressions succeeded, and so
	// we must have at least one diagnostic and we'll return all of them
	// so that the user can see the errors related to whichever one they
	// were expecting to have succeeded in this case.
	//
	// Because our function must return a single error value rather than
	// diagnostics, we'll construct a suitable error message string
	// that will make sense in the context of the function call failure
	// diagnostic HCL will eventually wrap this in.
	var buf strings.Builder
	buf.WriteString("no expression succeeded:\n")
	for _, diag := range diags {
		if diag.Subject != nil {
			buf.WriteString(fmt.Sprintf("- %s (at %s)\n  %s\n", diag.Summary, diag.Subject, diag.Detail))
		} else {
			buf.WriteString(fmt.Sprintf("- %s\n  %s\n", diag.Summary, diag.Detail))
		}
	}
	buf.WriteString("\nAt least one expression must produce a successful result")
	return cty.NilVal, errors.E(buf.String())
}

// dependsOnUnknowns returns true if any of the variables that the given
// expression might access are unknown values or contain unknown values.
//
// This is a conservative result that prefers to return true if there's any
// chance that the expression might derive from an unknown value during its
// evaluation; it is likely to produce false-positives for more complex
// expressions involving deep data structures.
func dependsOnUnknowns(expr hhcl.Expression, ctx *hhcl.EvalContext) bool {
	for _, traversal := range expr.Variables() {
		val, diags := traversal.TraverseAbs(ctx)
		if diags.HasErrors() {
			// If the traversal returned a definitive error then it must
			// not traverse through any unknowns.
			continue
		}
		if !val.IsWhollyKnown() {
			// The value will be unknown if either it refers directly to
			// an unknown value or if the traversal moves through an unknown
			// collection. We're using IsWhollyKnown, so this also catches
			// situations where the traversal refers to a compound data
			// structure that contains any unknown values. That's important,
			// because during evaluation the expression might evaluate more
			// deeply into this structure and encounter the unknowns.
			return true
		}
	}
	return false
}

func check(funcs map[string]function.Function, expr hhcl.Expression) error {
	staticCall, diags := hhcl.ExprCall(expr)
	if !diags.HasErrors() {
		if _, ok := funcs[staticCall.Name]; !ok {
			return errors.E(expr.Range(),
				"no function named %s", staticCall.Name)
		}
	}

	elems, diags := hhcl.ExprList(expr)
	if !diags.HasErrors() {
		for _, elem := range elems {
			err := check(funcs, elem)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
