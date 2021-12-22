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

package terramate

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	tflang "github.com/hashicorp/terraform/lang"
	"github.com/madlambda/spells/errutil"
	"github.com/mineiros-io/terramate/config"
	"github.com/mineiros-io/terramate/hcl"
	"github.com/zclconf/go-cty/cty"
)

// Globals represents a globals block.
type Globals struct {
	evaluated   map[string]cty.Value
	nonEvaluted map[string]hclsyntax.Expression
}

const ErrGlobalRedefined errutil.Error = "global redefined"

// LoadStackGlobals loads from the file system all globals defined for
// a given stack. It will navigate the file system from the stack dir until
// it reaches rootdir, loading globals and merging them appropriately.
//
// More specific globals (closer or at the stack) have precedence over
// less specific globals (closer or at the root dir).
//
// Metadata for the stack is used on the evaluation of globals, defined on stackmeta.
// The rootdir MUST be an absolute path.
func LoadStackGlobals(rootdir string, meta StackMetadata) (*Globals, error) {
	if !filepath.IsAbs(rootdir) {
		return nil, fmt.Errorf("%q is not absolute path", rootdir)
	}

	globals, err := loadStackGlobals(rootdir, meta.Path)
	if err != nil {
		return nil, err
	}
	if err := globals.Eval(meta); err != nil {
		return nil, err
	}

	return globals, nil
}

// Iter iterates the globals. There is no order guarantee on the iteration.
func (g *Globals) Iter(iter func(name string, val cty.Value)) {
	for name, val := range g.evaluated {
		iter(name, val)
	}
}

// Eval evaluates any pending expressions on the context of a specific stack.
// It is safe to call Eval with the same metadata multiple times.
func (g *Globals) Eval(meta StackMetadata) error {

	// TODO(katcipis): add BaseDir on Scope.
	tfscope := &tflang.Scope{}
	evalctx, err := newHCLEvalContext(meta, tfscope)
	if err != nil {
		return err
	}

	for k, expr := range g.nonEvaluted {
		val, err := expr.Value(evalctx)
		if err != nil {
			return err
		}
		g.evaluated[k] = val
	}
	g.nonEvaluted = map[string]hclsyntax.Expression{}
	return nil
}

func (g *Globals) merge(other *Globals) {
	for k, v := range other.nonEvaluted {
		_, ok := g.nonEvaluted[k]
		if !ok {
			g.nonEvaluted[k] = v
		}
	}
}

func loadStackGlobals(rootdir string, cfgdir string) (*Globals, error) {
	cfgpath := filepath.Join(rootdir, cfgdir, config.Filename)
	blocks, err := hcl.ParseGlobalsBlocks(cfgpath)

	if os.IsNotExist(err) {
		parentcfg, ok := parentDir(cfgdir)
		if !ok {
			return newGlobals(), nil
		}
		return loadStackGlobals(rootdir, parentcfg)

	}

	if err != nil {
		return nil, err
	}

	globals := newGlobals()

	for _, block := range blocks {
		for name, attr := range block.Body.Attributes {
			if _, ok := globals.nonEvaluted[name]; ok {
				return nil, fmt.Errorf("%w: global %q already defined", ErrGlobalRedefined, name)
			}
			globals.nonEvaluted[name] = attr.Expr
		}
	}

	parentcfg, ok := parentDir(cfgdir)
	if !ok {
		return globals, nil
	}

	parentGlobals, err := loadStackGlobals(rootdir, parentcfg)

	if err != nil {
		return nil, err
	}

	globals.merge(parentGlobals)
	return globals, nil
}

func parentDir(dir string) (string, bool) {
	parent := filepath.Dir(dir)
	return parent, parent != dir
}

func newGlobals() *Globals {
	return &Globals{
		evaluated:   map[string]cty.Value{},
		nonEvaluted: map[string]hclsyntax.Expression{},
	}
}
