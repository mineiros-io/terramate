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

package genfile

import (
	"path/filepath"
	"strings"

	"github.com/mineiros-io/terramate"
	"github.com/mineiros-io/terramate/errors"
	"github.com/mineiros-io/terramate/hcl"
	"github.com/mineiros-io/terramate/hcl/eval"
	"github.com/mineiros-io/terramate/project"
	"github.com/mineiros-io/terramate/stack"
	"github.com/rs/zerolog/log"
)

// StackFiles represents all generated files for a stack,
// mapping the generated file filename to the actual file body.
type StackFiles struct {
	files map[string]File
}

// File represents generated file from a single generate_file block.
type File struct {
	origin string
	body   string
}

// Body returns the file body.
func (f File) Body() string {
	return f.body
}

// Origin returns the path, relative to the project root,
// of the configuration that originated the file.
func (f File) Origin() string {
	return f.origin
}

// GeneratedFiles returns all generated files, mapping the name to
// the file description.
func (s StackFiles) GeneratedFiles() map[string]File {
	cp := map[string]File{}
	for k, v := range s.files {
		cp[k] = v
	}
	return cp
}

// Load loads and parse from the file system all generate_file blocks for
// a given stack. It will navigate the file system from the stack dir until
// it reaches rootdir, loading generate_file blocks found on Terramate
// configuration files.
//
// All generate_file blocks must have unique labels, even ones at different
// directories. Any conflicts will be reported as an error.
//
// Metadata and globals for the stack are used on the evaluation of the
// generate_file blocks.
//
// The rootdir MUST be an absolute path.
func Load(rootdir string, sm stack.Metadata, globals terramate.Globals) (StackFiles, error) {
	stackpath := filepath.Join(rootdir, sm.Path())
	logger := log.With().
		Str("action", "genfile.Load()").
		Str("path", stackpath).
		Logger()

	logger.Trace().Msg("loading generate_file blocks")

	loadedGenFileBlocks, err := loadGenFileBlocks(rootdir, stackpath)
	if err != nil {
		return StackFiles{}, errors.E("loading generate_file", err)
	}

	evalctx, err := newEvalCtx(stackpath, sm, globals)
	if err != nil {
		return StackFiles{}, errors.E(err, "creating eval context")
	}

	logger.Trace().Msg("generating files")

	res := StackFiles{
		files: map[string]File{},
	}

	for name, loadedGenFileBlock := range loadedGenFileBlocks {
		logger := logger.With().
			Str("block", name).
			Logger()

		logger.Trace().Msg("evaluating contents")

		value, err := evalctx.Eval(loadedGenFileBlock.block.Content.Expr)
		if err != nil {
			return StackFiles{}, errors.E(sm, "origin: %s: evaluating block %s", loadedGenFileBlock.origin, name)
		}

		// TODO(katcipis): check for value underlying type (or not, still discussing)
		res.files[name] = File{
			origin: loadedGenFileBlock.origin,
			body:   value.AsString(),
		}
	}

	logger.Trace().Msg("evaluated all blocks with success.")

	return res, nil
}

type genFileBlock struct {
	origin string
	block  hcl.GenFileBlock
}

// loadGenFileBlocks will load all generate_file blocks.
// The returned map maps the name of the block (its label)
// to the original block and the path (relative to project root) of the config
// from where it was parsed.
func loadGenFileBlocks(rootdir string, cfgdir string) (map[string]genFileBlock, error) {
	logger := log.With().
		Str("action", "genfile.loadGenFileBlocks()").
		Str("root", rootdir).
		Str("configDir", cfgdir).
		Logger()

	logger.Trace().Msg("Parsing generate_hcl blocks.")

	if !strings.HasPrefix(cfgdir, rootdir) {
		logger.Trace().Msg("config dir outside root, nothing to do")
		return nil, nil
	}

	genFileBlocks, err := hcl.ParseGenerateFileBlocks(cfgdir)
	if err != nil {
		return nil, errors.E(err, "cfgdir %q", cfgdir)
	}

	logger.Trace().Msg("Parsed generate_file blocks.")
	res := map[string]genFileBlock{}

	for filename, blocks := range genFileBlocks {
		for _, block := range blocks {
			name := block.Label
			// TODO(katcipis): test name conflict, doesn't look like parse error
			//if _, ok := res[name]; ok {
			//return nil, errors.E(
			//ErrParsing,
			//genhclBlock.LabelRanges[0],
			//"found two blocks with same label %q", name,
			//)
			//}

			res[name] = genFileBlock{
				origin: project.PrjAbsPath(rootdir, filename),
				block:  block,
			}

			logger.Trace().Msg("loaded generate_file block.")
		}
	}

	parentRes, err := loadGenFileBlocks(rootdir, filepath.Dir(cfgdir))
	if err != nil {
		return nil, err
	}

	_ = join(res, parentRes)

	// TODO(katcipis): test error handling
	//if err := join(res, parentRes); err != nil {
	//return nil, errors.E(ErrMultiLevelConflict, err)
	//}

	logger.Trace().Msg("loaded generate_file blocks with success.")
	return res, nil
}

func newEvalCtx(stackpath string, sm stack.Metadata, globals terramate.Globals) (*eval.Context, error) {
	// TODO(katcipis): duplicated on genhcl, extract soon
	logger := log.With().
		Str("action", "genfile.newEvalCtx()").
		Str("path", stackpath).
		Logger()

	evalctx := eval.NewContext(stackpath)

	logger.Trace().Msg("Add stack metadata evaluation namespace.")

	err := evalctx.SetNamespace("terramate", stack.MetaToCtyMap(sm))
	if err != nil {
		return nil, errors.E(sm, err, "setting terramate namespace on eval context")
	}

	logger.Trace().Msg("Add global evaluation namespace.")

	if err := evalctx.SetNamespace("global", globals.Attributes()); err != nil {
		return nil, errors.E(sm, err, "setting global namespace on eval context")
	}

	return evalctx, nil
}

func join(target, src map[string]genFileBlock) error {
	for blockLabel, srcHCL := range src {
		if targetHCL, ok := target[blockLabel]; ok {
			return errors.E(
				"found label %q at %q and %q",
				blockLabel,
				srcHCL.origin,
				targetHCL.origin,
			)
		}
		target[blockLabel] = srcHCL
	}
	return nil
}