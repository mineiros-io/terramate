// Copyright 2022 Mineiros GmbH
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

package genfilehcl

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/madlambda/spells/errutil"
	"github.com/mineiros-io/terramate"
	"github.com/mineiros-io/terramate/hcl"
	"github.com/mineiros-io/terramate/hcl/eval"
	"github.com/mineiros-io/terramate/project"
	"github.com/mineiros-io/terramate/stack"
	"github.com/rs/zerolog/log"
)

// StackHCLs represents all generated HCL code for a stack,
// mapping the generated code filename to the actual HCL code.
type StackHCLs struct {
	hcls map[string]HCL
}

// HCL represents generated HCL code from a single block.
// Is contains parsed and evaluated code on it and information
// about the origin of the generated code.
type HCL struct {
	origin string
	body   []byte
}

const (
	ErrMultiLevelConflict errutil.Error = "conflicting generate_file blocks"
	ErrParsing            errutil.Error = "parsing generate_file block"
	ErrEval               errutil.Error = "evaluating generate_file block"
)

// GeneratedHCLs returns all generated code, mapping the name to its
// equivalent generated code.
func (s StackHCLs) GeneratedHCLs() map[string]HCL {
	cp := map[string]HCL{}
	for k, v := range s.hcls {
		cp[k] = v
	}
	return cp
}

// String returns a string representation of the HCL code
// or an empty string if the config itself is empty.
func (b HCL) String() string {
	return string(b.body)
}

// Origin returns the path, relative to the project root,
// of the configuration that originated the code.
func (b HCL) Origin() string {
	return b.origin
}

// Load loads from the file system all generate_file for
// a given stack. It will navigate the file system from the stack dir until
// it reaches rootdir, loading generate_file and merging them appropriately.
//
// More specific definitions (closer or at the stack) have precedence over
// less specific ones (closer or at the root dir).
//
// Metadata and globals for the stack are used on the evaluation of the
// generate_file blocks.
//
// The returned result only contains evaluated values.
//
// The rootdir MUST be an absolute path.
func Load(rootdir string, sm stack.Metadata, globals terramate.Globals) (StackHCLs, error) {
	stackpath := filepath.Join(rootdir, sm.Path)
	logger := log.With().
		Str("action", "genfile.Load()").
		Str("path", stackpath).
		Logger()

	logger.Trace().Msg("loading generate_file blocks.")

	loadedFileBlocks, err := loadGenFileBlocks(rootdir, stackpath)
	if err != nil {
		return StackHCLs{}, fmt.Errorf("loading generate_file: %w", err)
	}

	evalctx, err := newEvalCtx(stackpath, sm, globals)
	if err != nil {
		return StackHCLs{}, fmt.Errorf("%w: creating eval context: %v", ErrEval, err)
	}

	logger.Trace().Msg("generating HCL code.")

	res := StackHCLs{
		hcls: map[string]HCL{},
	}

	for name, loadedFileBlock := range loadedFileBlocks {
		logger := logger.With().
			Str("block", name).
			Logger()

		logger.Trace().Msg("evaluating block.")

		value, err := evalctx.Eval(loadedFileBlock.content.Expr)
		if err != nil {
			evalErr := fmt.Errorf(
				"%w: stack %q block %q",
				ErrEval,
				stackpath,
				name,
			)
			return StackHCLs{}, errutil.Chain(evalErr, err)
		}

		val := ""

		if !value.IsNull() {
			val = value.AsString()
		}

		res.hcls[name] = HCL{
			origin: loadedFileBlock.origin,
			body:   hclwrite.Format([]byte(val)),
		}
	}

	logger.Trace().Msg("evaluated all blocks with success.")

	return res, nil
}

func newEvalCtx(stackpath string, sm stack.Metadata, globals terramate.Globals) (*eval.Context, error) {
	logger := log.With().
		Str("action", "genfile.newEvalCtx()").
		Str("path", stackpath).
		Logger()

	evalctx := eval.NewContext(stackpath)

	logger.Trace().Msg("Add stack metadata evaluation namespace.")

	err := evalctx.SetNamespace("terramate", sm.ToCtyMap())
	if err != nil {
		return nil, fmt.Errorf("setting terramate namespace on eval context for stack %q: %v",
			stackpath, err)
	}

	logger.Trace().Msg("Add global evaluation namespace.")

	if err := evalctx.SetNamespace("global", globals.Attributes()); err != nil {
		return nil, fmt.Errorf("setting global namespace on eval context for stack %q: %v",
			stackpath, err)
	}

	return evalctx, nil
}

type loadedFile struct {
	origin  string
	content *hclsyntax.Attribute
}

// loadGenFileBlocks will load all generate_file blocks.
// The returned map maps the name of the block (its label)
// to the original block and the path (relative to project root) of the config
// from where it was parsed.
func loadGenFileBlocks(rootdir string, cfgdir string) (map[string]loadedFile, error) {
	logger := log.With().
		Str("action", "genfile.loadGenFileBlocks()").
		Str("root", rootdir).
		Str("configDir", cfgdir).
		Logger()

	logger.Trace().Msg("Parsing generate_file blocks.")

	if !strings.HasPrefix(cfgdir, rootdir) {
		logger.Trace().Msg("config dir outside root, nothing to do")
		return nil, nil
	}

	bodySchema := hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{
				Name:     "content",
				Required: true,
			},
		},
		Blocks: []hcl.BlockSchema{},
	}

	schema := hcl.MakeBodySchema(bodySchema)

	generateFileBlocks, err := hcl.ParseGenerateBlocks(cfgdir, "generate_file", func(block *hclsyntax.Block) error {
		// Don't seem like I can use hcl.BodySchema to check for any non-empty
		// label, only specific label values.

		if len(block.Labels) != 1 {
			return fmt.Errorf(
				"generate_file must have single label instead got %v",
				block.Labels,
			)
		}
		if block.Labels[0] == "" {
			return errors.New("generate_file label can't be empty")
		}
		// Schema check passes if no block is present, so check for amount of blocks
		if len(block.Body.Attributes) != 1 {
			return fmt.Errorf("generate_file must have one 'content' attribute, got %d attributes", len(block.Body.Attributes))
		}
		_, diags := block.Body.Content(&schema)
		if diags.HasErrors() {
			return diags
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: cfgdir %q: %v", ErrParsing, cfgdir, err)
	}

	logger.Trace().Msg("Parsed generate_file blocks.")

	res := map[string]loadedFile{}

	for filename, genFileBlocks := range generateFileBlocks {
		for _, genhFileBlock := range genFileBlocks {
			name := genhFileBlock.Labels[0]
			if _, ok := res[name]; ok {
				return nil, fmt.Errorf(
					"%w: found two blocks with same label %q",
					ErrParsing,
					name,
				)
			}

			contentAttr := genhFileBlock.Body.Attributes["content"]
			res[name] = loadedFile{
				origin:  project.PrjAbsPath(rootdir, filename),
				content: contentAttr,
			}

			logger.Trace().Msg("loaded generate_file block.")
		}
	}

	parentRes, err := loadGenFileBlocks(rootdir, filepath.Dir(cfgdir))
	if err != nil {
		return nil, err
	}
	if err := join(res, parentRes); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMultiLevelConflict, err)
	}

	logger.Trace().Msg("loaded generate_file blocks with success.")
	return res, nil
}

func join(target, src map[string]loadedFile) error {
	for blockLabel, srcFile := range src {
		if targetFile, ok := target[blockLabel]; ok {
			return fmt.Errorf(
				"found label %q at %q and %q",
				blockLabel,
				srcFile.origin,
				targetFile.origin,
			)
		}
		target[blockLabel] = srcFile
	}
	return nil
}