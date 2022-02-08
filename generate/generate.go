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

package generate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/madlambda/spells/errutil"
	"github.com/mineiros-io/terramate"
	"github.com/mineiros-io/terramate/config"
	"github.com/mineiros-io/terramate/generate/genhcl"
	"github.com/mineiros-io/terramate/hcl"
	"github.com/mineiros-io/terramate/hcl/eval"
	"github.com/mineiros-io/terramate/stack"
	"github.com/rs/zerolog/log"
)

const (
	ErrBackendConfigGen   errutil.Error = "generating backend config"
	ErrExportingLocalsGen errutil.Error = "generating locals"
	ErrLoadingGlobals     errutil.Error = "loading globals"
	ErrLoadingStackCfg    errutil.Error = "loading stack code gen config"
	ErrManualCodeExists   errutil.Error = "manually defined code found"
	ErrConflictingConfig  errutil.Error = "conflicting config detected"
)

const (
	// Current header used by terramate code generation
	Header = "// TERRAMATE: GENERATED AUTOMATICALLY DO NOT EDIT"

	// First header used by terramate code generation
	HeaderV0 = "// GENERATED BY TERRAMATE: DO NOT EDIT"
)

// Do will walk all the stacks inside the given working dir
// generating code for any stack it finds as it goes along.
//
// Code is generated based on configuration files spread around the entire
// project until it reaches the given root. So even though a configuration
// file may be outside the given working dir it may be used on code generation
// if it is in a dir that is a parent of a stack found inside the working dir.
//
// The provided root must be the project's root directory as an absolute path.
// The provided working dir must be an absolute path that is a child of the
// provided root (or the same as root, indicating that working dir is the project root).
//
// It will return an error if it finds any invalid Terramate configuration files
// or if it can't generate the files properly for some reason.
func Do(root string, workingDir string) error {
	errs := forEachStack(root, workingDir, func(
		stack stack.S,
		globals *terramate.Globals,
		cfg StackCfg,
	) error {
		stackpath := stack.AbsPath()
		logger := log.With().
			Str("action", "generate.Do()").
			Str("path", root).
			Str("stackpath", stackpath).
			Logger()

		genfiles := []genfile{}
		stackMeta := stack.Meta()

		logger.Trace().Msg("Generate stack backend config.")

		stackBackendCfgCode, err := generateBackendCfgCode(root, stackpath, stackMeta, globals, stackpath)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrBackendConfigGen, err)
		}
		genfiles = append(genfiles, genfile{name: cfg.BackendCfgFilename, body: stackBackendCfgCode})

		logger.Trace().Msg("Generate stack locals.")

		stackLocalsCode, err := generateStackLocalsCode(root, stackpath, stackMeta, globals)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrExportingLocalsGen, err)
		}
		genfiles = append(genfiles, genfile{name: cfg.LocalsFilename, body: stackLocalsCode})

		logger.Trace().Msg("Generate stack terraform.")

		stackHCLsCode, err := generateStackHCLCode(root, stackpath, stackMeta, globals, cfg)
		if err != nil {
			return err
		}
		genfiles = append(genfiles, stackHCLsCode...)

		logger.Trace().Msg("Removing outdated generated files.")

		if err := removeStackGeneratedFiles(stack); err != nil {
			return fmt.Errorf("removing old files to generate new ones: %v", err)
		}

		logger.Trace().Msg("Saving generated files.")

		// FIXME(katcipis): fail if different code gen mechanism have config
		// to save on same file. Right now one overwrites the other.

		for _, genfile := range genfiles {
			path := filepath.Join(stackpath, genfile.name)
			logger := logger.With().
				Str("filepath", path).
				Logger()

			// Empty results are used when doing the outdated code
			// generation detection. A config previously generating
			// some code could now be generating nothing
			// (like an empty block on gen_hcl, or an empty export_as_locals)
			// And we don't want to generate files just with a header inside.
			if genfile.body == "" {
				logger.Trace().Msg("ignoring empty code")
				continue
			}

			logger.Trace().Msg("saving generated file")

			err := writeGeneratedCode(path, genfile.body)
			if err != nil {
				return fmt.Errorf("saving file %q: %w", genfile.name, err)
			}

			logger.Trace().Msg("saved generated file")
		}

		return nil
	})

	// FIXME(katcipis): errutil.Chain produces a very hard to read string representation
	// for this case, we have a possibly big list of errors here, not an
	// actual chain (multiple levels of calls).
	// We do need the error wrapping for the error handling on tests (for now at least).
	if err := errutil.Chain(errs...); err != nil {
		return fmt.Errorf("failed to generate code: %w", err)
	}

	return nil
}

// ListStackGenFiles will list the filenames of all generated code inside
// a stack.  The filenames are ordered lexicographically.
func ListStackGenFiles(stack stack.S) ([]string, error) {
	logger := log.With().
		Str("action", "generate.ListStackGenFiles()").
		Stringer("stack", stack).
		Logger()

	logger.Trace().Msg("listing stack dir files")

	dirEntries, err := os.ReadDir(stack.AbsPath())
	if err != nil {
		return nil, fmt.Errorf("listing stack files: %v", err)
	}

	genfiles := []string{}

	for _, dirEntry := range dirEntries {
		if dirEntry.IsDir() {
			continue
		}
		logger := logger.With().
			Str("filename", dirEntry.Name()).
			Logger()

		logger.Trace().Msg("Checking if file is generated by terramate")

		filepath := filepath.Join(stack.AbsPath(), dirEntry.Name())
		data, err := os.ReadFile(filepath)
		if err != nil {
			return nil, fmt.Errorf("checking if file is generated %q: %v", filepath, err)
		}

		logger.Trace().Msg("File read, checking for terramate headers")

		if hasTerramateHeader(data) {
			logger.Trace().Msg("Terramate header detected")
			genfiles = append(genfiles, dirEntry.Name())
		}
	}

	logger.Trace().Msg("Done listing stack generated files")
	return genfiles, nil
}

// CheckStack will verify if a given stack has outdated code and return a list
// of filenames that are outdated, ordered lexicographically.
// If the stack has an invalid configuration it will return an error.
//
// The provided root must be the project's root directory as an absolute path.
func CheckStack(root string, stack stack.S) ([]string, error) {
	logger := log.With().
		Str("action", "generate.CheckStack()").
		Str("path", root).
		Stringer("stack", stack).
		Logger()

	outdated := []string{}

	logger.Trace().Msg("Load stack code generation config.")

	cfg, err := LoadStackCfg(root, stack)
	if err != nil {
		return nil, fmt.Errorf("checking for outdated code: %v", err)
	}

	logger.Trace().Msg("Loading globals for stack.")

	globals, err := terramate.LoadStackGlobals(root, stack.Meta())
	if err != nil {
		return nil, fmt.Errorf("checking for outdated code: %v", err)
	}

	stackpath := stack.AbsPath()
	stackMeta := stack.Meta()

	outdatedBackendFiles, err := backendConfigOutdatedFiles(root, stackpath, stackMeta, globals, cfg)
	if err != nil {
		return nil, fmt.Errorf("checking for outdated backend config: %v", err)
	}
	outdated = append(outdated, outdatedBackendFiles...)

	outdatedLocalsFiles, err := exportedLocalsOutdatedFiles(root, stackpath, stackMeta, globals, cfg)
	if err != nil {
		return nil, fmt.Errorf("checking for outdated exported locals: %v", err)
	}
	outdated = append(outdated, outdatedLocalsFiles...)

	outdatedTerraformFiles, err := generatedHCLOutdatedFiles(root, stackpath, stackMeta, globals, cfg)
	if err != nil {
		return nil, fmt.Errorf("checking for outdated exported terraform: %v", err)
	}
	outdated = append(outdated, outdatedTerraformFiles...)

	sort.Strings(outdated)

	return outdated, nil
}

type genfile struct {
	name string
	body string
}

func backendConfigOutdatedFiles(
	root, stackpath string,
	stackMeta stack.Metadata,
	globals *terramate.Globals,
	cfg StackCfg,
) ([]string, error) {
	logger := log.With().
		Str("action", "generate.backendConfigOutdatedFiles()").
		Str("root", root).
		Str("stackpath", stackpath).
		Logger()

	logger.Trace().Msg("Generating backend cfg code for stack.")

	genbackend, err := generateBackendCfgCode(root, stackpath, stackMeta, globals, stackpath)
	if err != nil {
		return nil, err
	}

	stackBackendCfgFile := filepath.Join(stackpath, cfg.BackendCfgFilename)
	currentbackend, err := loadGeneratedCode(stackBackendCfgFile)
	if err != nil {
		return nil, err
	}

	logger.Trace().Msg("Checking for outdated backend cfg code on stack.")

	if string(genbackend) != string(currentbackend) {
		logger.Trace().Msg("Detected outdated backend cfg.")
		return []string{cfg.BackendCfgFilename}, nil
	}

	logger.Trace().Msg("backend cfg is updated.")
	return nil, nil
}

func generatedHCLOutdatedFiles(
	root, stackpath string,
	stackMeta stack.Metadata,
	globals *terramate.Globals,
	cfg StackCfg,
) ([]string, error) {
	logger := log.With().
		Str("action", "generate.generateHCLOutdatedFiles()").
		Str("root", root).
		Str("stackpath", stackpath).
		Logger()

	logger.Trace().Msg("Checking for outdated exported terraform code on stack.")

	loadedStackTf, err := genhcl.Load(root, stackMeta, globals)
	if err != nil {
		return nil, err
	}

	logger.Trace().Msg("Loaded exported terraform code, checking")

	outdated := []string{}

	for filename, genHCL := range loadedStackTf.GeneratedHCLs() {
		targetpath := filepath.Join(stackpath, filename)
		logger := logger.With().
			Str("blockName", filename).
			Str("targetpath", targetpath).
			Logger()

		logger.Trace().Msg("Checking if code is updated.")

		tfcode := prependGenHCLHeader(genHCL.Origin(), genHCL.String())
		currentTfCode, err := loadGeneratedCode(targetpath)
		if err != nil {
			return nil, err
		}

		if tfcode != string(currentTfCode) {
			logger.Trace().Msg("Outdated HCL code detected.")
			outdated = append(outdated, filename)
		}
	}

	return outdated, nil
}

func exportedLocalsOutdatedFiles(
	root, stackpath string,
	stackMeta stack.Metadata,
	globals *terramate.Globals,
	cfg StackCfg,
) ([]string, error) {
	logger := log.With().
		Str("action", "generate.exportedLocalsOutdatedFiles()").
		Str("root", root).
		Str("stackpath", stackpath).
		Logger()

	logger.Trace().Msg("Checking for outdated exported locals code on stack.")

	genlocals, err := generateStackLocalsCode(root, stackpath, stackMeta, globals)
	if err != nil {
		return nil, err
	}

	stackLocalsFile := filepath.Join(stackpath, cfg.LocalsFilename)
	currentlocals, err := loadGeneratedCode(stackLocalsFile)
	if err != nil {
		return nil, err
	}

	if string(genlocals) != string(currentlocals) {
		logger.Trace().Msg("Detected outdated exported locals.")
		return []string{cfg.LocalsFilename}, nil
	}

	logger.Trace().Msg("exported locals are updated.")

	return nil, nil
}

func generateStackHCLCode(
	root string,
	stackpath string,
	meta stack.Metadata,
	globals *terramate.Globals,
	cfg StackCfg,
) ([]genfile, error) {
	logger := log.With().
		Str("action", "generateStackHCLCode()").
		Str("root", root).
		Str("stackpath", stackpath).
		Logger()

	logger.Trace().Msg("generating HCL code.")

	stackGeneratedHCL, err := genhcl.Load(root, meta, globals)
	if err != nil {
		return nil, err
	}

	logger.Trace().Msg("generated HCL code.")

	files := []genfile{}

	for name, generatedHCL := range stackGeneratedHCL.GeneratedHCLs() {
		targetpath := filepath.Join(stackpath, name)
		logger := logger.With().
			Str("blockName", name).
			Str("targetpath", targetpath).
			Logger()

		hclCode := generatedHCL.String()
		if hclCode == "" {
			files = append(files, genfile{name: name, body: hclCode})
			continue
		}

		hclCode = prependGenHCLHeader(generatedHCL.Origin(), hclCode)
		files = append(files, genfile{name: name, body: hclCode})

		logger.Debug().Msg("stack HCL code loaded.")
	}

	return files, nil
}

func generateStackLocalsCode(
	rootdir string,
	stackpath string,
	metadata stack.Metadata,
	globals *terramate.Globals,
) (string, error) {
	logger := log.With().
		Str("action", "generateStackLocals()").
		Str("stack", stackpath).
		Logger()

	logger.Trace().Msg("Load stack exported locals.")

	stackLocals, err := terramate.LoadStackExportedLocals(rootdir, metadata, globals)
	if err != nil {
		return "", err
	}

	logger.Trace().Msg("Get stack attributes.")

	localsAttrs := stackLocals.Attributes()
	if len(localsAttrs) == 0 {
		return "", nil
	}

	logger.Trace().Msg("Sort attributes.")

	sortedAttrs := make([]string, 0, len(localsAttrs))
	for name := range localsAttrs {
		sortedAttrs = append(sortedAttrs, name)
	}
	// Avoid generating code with random attr order (map iteration is random)
	sort.Strings(sortedAttrs)

	logger.Trace().
		Msg("Append locals block to file.")
	gen := hclwrite.NewEmptyFile()
	body := gen.Body()
	localsBlock := body.AppendNewBlock("locals", nil)
	localsBody := localsBlock.Body()

	logger.Trace().
		Msg("Set attribute values.")
	for _, name := range sortedAttrs {
		localsBody.SetAttributeValue(name, localsAttrs[name])
	}

	tfcode := prependHeader(string(gen.Bytes()))
	return tfcode, nil
}

func generateBackendCfgCode(
	root string,
	stackpath string,
	stackMetadata stack.Metadata,
	globals *terramate.Globals,
	configdir string,
) (string, error) {
	logger := log.With().
		Str("action", "loadStackBackendConfig()").
		Str("configDir", configdir).
		Logger()

	logger.Trace().
		Msg("Check if config dir outside of root dir.")

	if !strings.HasPrefix(configdir, root) {
		// check if we are outside of project's root, time to stop
		return "", nil
	}

	logger.Trace().
		Msg("Get config file path.")
	configfile := filepath.Join(configdir, config.Filename)

	logger = logger.With().
		Str("configFile", configfile).
		Logger()

	logger.Trace().
		Msg("Load stack backend config.")
	if _, err := os.Stat(configfile); err != nil {
		// FIXME(katcipis): use  os.IsNotExist(err) to handle errors properly.
		// Unknown stat errors will be ignored right now.
		return generateBackendCfgCode(root, stackpath, stackMetadata, globals, filepath.Dir(configdir))
	}

	logger.Debug().
		Msg("Read config file.")
	config, err := os.ReadFile(configfile)
	if err != nil {
		return "", fmt.Errorf("reading config: %v", err)
	}

	logger.Debug().
		Msg("Parse config file.")
	parsedConfig, err := hcl.Parse(configfile, config)
	if err != nil {
		return "", fmt.Errorf("parsing config: %w", err)
	}

	logger.Trace().
		Msg("Check if parsed is empty.")
	parsed := parsedConfig.Terramate
	if parsed == nil || parsed.Backend == nil {
		return generateBackendCfgCode(root, stackpath, stackMetadata, globals, filepath.Dir(configdir))
	}

	evalctx := eval.NewContext(stackpath)

	logger.Trace().Msg("Add stack metadata evaluation namespace.")

	err = evalctx.SetNamespace("terramate", stackMetadata.ToCtyMap())
	if err != nil {
		return "", fmt.Errorf("setting terramate namespace on eval context for stack %q: %v",
			stackpath, err)
	}

	logger.Trace().Msg("Add global evaluation namespace.")

	if err := evalctx.SetNamespace("global", globals.Attributes()); err != nil {
		return "", fmt.Errorf("setting global namespace on eval context for stack %q: %v",
			stackpath, err)
	}

	logger.Debug().Msg("Create new file and append parsed blocks.")

	gen := hclwrite.NewEmptyFile()
	rootBody := gen.Body()
	tfBlock := rootBody.AppendNewBlock("terraform", nil)
	tfBody := tfBlock.Body()
	backendBlock := tfBody.AppendNewBlock(parsed.Backend.Type, parsed.Backend.Labels)
	backendBody := backendBlock.Body()

	if err := hcl.CopyBody(backendBody, parsed.Backend.Body, evalctx); err != nil {
		return "", err
	}

	return prependHeader(string(gen.Bytes())), nil
}

func prependHeader(code string) string {
	return Header + "\n\n" + code
}

func prependGenHCLHeader(origin, code string) string {
	return fmt.Sprintf(
		"%s\n// TERRAMATE: originated from generate_hcl block on %s\n\n%s",
		Header,
		origin,
		code,
	)
}

func writeGeneratedCode(target string, code string) error {
	logger := log.With().
		Str("action", "writeGeneratedCode()").
		Str("file", target).
		Logger()

	logger.Trace().Msg("Checking code can be written.")

	if err := checkFileCanBeOverwritten(target); err != nil {
		return err
	}

	logger.Trace().Msg("Writing code")
	return os.WriteFile(target, []byte(code), 0666)
}

func checkFileCanBeOverwritten(path string) error {
	_, err := loadGeneratedCode(path)
	return err
}

func loadGeneratedCode(path string) ([]byte, error) {
	logger := log.With().
		Str("action", "loadGeneratedCode()").
		Str("path", path).
		Logger()

	logger.Trace().Msg("Get file information.")

	_, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("loading code: can't stat %q: %v", path, err)
	}

	logger.Trace().Msg("Read file.")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("loading code, can't read %q: %v", path, err)
	}

	logger.Trace().Msg("Check if code has terramate header.")

	if hasTerramateHeader(data) {
		return data, nil
	}

	return nil, fmt.Errorf("%w: at %q", ErrManualCodeExists, path)
}

type forEachStackFunc func(stack.S, *terramate.Globals, StackCfg) error

func forEachStack(root, workingDir string, fn forEachStackFunc) []error {
	logger := log.With().
		Str("action", "generate.forEachStack()").
		Str("root", root).
		Str("workingDir", workingDir).
		Logger()

	logger.Trace().Msg("List stacks.")

	stackEntries, err := terramate.ListStacks(root)
	if err != nil {
		return []error{err}
	}

	var errs []error

	for _, entry := range stackEntries {
		stack := entry.Stack

		logger := logger.With().
			Stringer("stack", stack).
			Logger()

		if !strings.HasPrefix(stack.AbsPath(), workingDir) {
			logger.Trace().Msg("discarding stack outside working dir")
			continue
		}

		logger.Trace().Msg("Load stack code generation config.")

		cfg, err := LoadStackCfg(root, stack)
		if err != nil {
			errs = append(errs, fmt.Errorf(
				"stack %q: %w: %v",
				stack.AbsPath(),
				ErrLoadingStackCfg,
				err))
			continue
		}

		logger.Trace().Msg("Load stack globals.")

		globals, err := terramate.LoadStackGlobals(root, stack.Meta())
		if err != nil {
			errs = append(errs, fmt.Errorf(
				"stack %q: %w: %v",
				stack.AbsPath(),
				ErrLoadingGlobals,
				err))
			continue
		}

		logger.Trace().Msg("Calling stack callback.")
		if err := fn(stack, globals, cfg); err != nil {
			errs = append(errs, err)
		}
	}

	return errs
}

func removeStackGeneratedFiles(stack stack.S) error {
	logger := log.With().
		Str("action", "generate.removeStackGeneratedFiles()").
		Stringer("stack", stack).
		Logger()

	logger.Trace().Msg("listing generated files")

	files, err := ListStackGenFiles(stack)
	if err != nil {
		return err
	}

	for _, file := range files {
		path := filepath.Join(stack.AbsPath(), file)
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("removing gen file: %v", err)
		}
	}
	return nil
}

func hasTerramateHeader(code []byte) bool {
	// When changing headers we need to support old ones (or break).
	// For now keeping them here, to avoid breaks.
	for _, header := range []string{Header, HeaderV0} {
		if strings.HasPrefix(string(code), header) {
			return true
		}
	}
	return false
}
