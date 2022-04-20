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
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mineiros-io/terramate"
	"github.com/mineiros-io/terramate/errors"
	"github.com/mineiros-io/terramate/generate/genhcl"
	"github.com/mineiros-io/terramate/stack"
	"github.com/rs/zerolog/log"
)

const (
	// ErrLoadingGlobals indicates failure loading globals during code generation.
	ErrLoadingGlobals errors.Kind = "loading globals"

	// ErrManualCodeExists indicates code generation would replace code that
	// was not previously generated by Terramate.
	ErrManualCodeExists errors.Kind = "manually defined code found"

	// ErrConflictingConfig indicates that two code generation configurations
	// are conflicting, like both generates a file with the same name
	// and would overwrite each other.
	ErrConflictingConfig errors.Kind = "conflicting config detected"

	// ErrInvalidFilePath indicates that code generation configuration
	// has an invalid filepath as the target to save the generated code.
	ErrInvalidFilePath errors.Kind = "invalid filepath"
)

const (
	// Header is the current header string used by Terramate code generation.
	Header = "// TERRAMATE: GENERATED AUTOMATICALLY DO NOT EDIT"

	// HeaderV0 is the deprecated header string used by Terramate code generation.
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
// It will return a report including details of which stacks succeed and failed
// on code generation, any failure found is added to the report but does not abort
// the overall code generation process, so partial results can be obtained and the
// report needs to be inspected to check.
func Do(root string, workingDir string) Report {
	return forEachStack(root, workingDir, func(
		stack stack.S,
		globals terramate.Globals,
	) stackReport {
		stackpath := stack.HostPath()
		logger := log.With().
			Str("action", "generate.Do()").
			Str("path", root).
			Str("stackpath", stackpath).
			Logger()

		genfiles := []genfile{}
		report := stackReport{}

		logger.Trace().Msg("Generate stack terraform.")

		stackHCLsCode, err := generateStackHCLCode(root, stackpath, stack, globals)
		if err != nil {
			report.err = err
			return report
		}
		genfiles = append(genfiles, stackHCLsCode...)

		logger.Trace().Msg("Checking for invalid paths on generated files.")
		if err := checkGeneratedFilesPaths(genfiles); err != nil {
			report.err = errors.E(ErrInvalidFilePath, err)
			return report
		}

		logger.Trace().Msg("Checking for conflicts on generated files.")

		if err := checkGeneratedFilesConflicts(genfiles); err != nil {
			report.err = errors.E(ErrConflictingConfig, err)
			return report
		}

		logger.Trace().Msg("Removing outdated generated files.")

		var removedFiles map[string]string

		failureReport := func(r stackReport, err error) stackReport {
			r.err = err
			for filename := range removedFiles {
				r.addDeletedFile(filename)
			}
			return r
		}

		removedFiles, err = removeStackGeneratedFiles(stack)
		if err != nil {
			return failureReport(
				report,
				errors.E(err, "removing old generated files"),
			)
		}

		logger.Trace().Msg("Saving generated files.")

		for _, genfile := range genfiles {
			path := filepath.Join(stackpath, genfile.name)
			logger := logger.With().
				Str("filename", genfile.name).
				Logger()

			// We don't want to generate files just with a header inside.
			if genfile.body == "" {
				logger.Trace().Msg("ignoring empty code")
				continue
			}

			logger.Trace().Msg("saving generated file")

			err := writeGeneratedCode(path, genfile.body)
			if err != nil {
				return failureReport(
					report,
					errors.E(err, "saving file %q", genfile.name),
				)
			}

			// Change detection + remove code that got deleted but
			// was re-generated from the removed files map
			removedFileBody, ok := removedFiles[genfile.name]
			if !ok {
				report.addCreatedFile(genfile.name)
			} else {
				if genfile.body != removedFileBody {
					report.addChangedFile(genfile.name)
				}
				delete(removedFiles, genfile.name)
			}
			logger.Trace().Msg("saved generated file")
		}

		for filename := range removedFiles {
			report.addDeletedFile(filename)
		}
		return report
	})
}

// ListStackGenFiles will list the filenames of all generated code inside
// a stack.  The filenames are ordered lexicographically.
func ListStackGenFiles(stack stack.S) ([]string, error) {
	logger := log.With().
		Str("action", "generate.ListStackGenFiles()").
		Stringer("stack", stack).
		Logger()

	logger.Trace().Msg("listing stack dir files")

	dirEntries, err := os.ReadDir(stack.HostPath())
	if err != nil {
		return nil, errors.E(err, "listing stack files")
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

		filepath := filepath.Join(stack.HostPath(), dirEntry.Name())
		data, err := os.ReadFile(filepath)
		if err != nil {
			return nil, errors.E(err, "checking if file is generated %q", filepath)
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

	logger.Trace().Msg("Load stack code generation config.")

	logger.Trace().Msg("Loading globals for stack.")

	globals, err := terramate.LoadStackGlobals(root, stack)
	if err != nil {
		return nil, errors.E(err, "checking for outdated code")
	}

	logger.Trace().Msg("Listing current generated files.")

	generatedFiles, err := ListStackGenFiles(stack)
	if err != nil {
		return nil, errors.E(err, "checking for outdated code")
	}

	// We start with the assumption that all gen files on the stack
	// are outdated and then update the outdated files set as we go.
	outdatedFiles := newStringSet(generatedFiles...)
	stackpath := stack.HostPath()
	err = generatedHCLOutdatedFiles(
		root,
		stackpath,
		stack,
		globals,
		outdatedFiles,
	)

	if err != nil {
		return nil, errors.E(err, "checking for outdated exported terraform")
	}

	outdated := outdatedFiles.slice()
	sort.Strings(outdated)
	return outdated, nil
}

type genfile struct {
	name string
	body string
}

func generatedHCLOutdatedFiles(
	root, stackpath string,
	stackMeta stack.Metadata,
	globals terramate.Globals,
	outdatedFiles *stringSet,
) error {
	logger := log.With().
		Str("action", "generate.generateHCLOutdatedFiles()").
		Str("root", root).
		Str("stackpath", stackpath).
		Logger()

	logger.Trace().Msg("Checking for outdated generated_hcl code on stack.")

	stackHCLs, err := genhcl.Load(root, stackMeta, globals)
	if err != nil {
		return err
	}

	logger.Trace().Msg("Loaded generated_hcl code, checking")

	for filename, genHCL := range stackHCLs.GeneratedHCLs() {
		targetpath := filepath.Join(stackpath, filename)
		logger := logger.With().
			Str("blockName", filename).
			Str("targetpath", targetpath).
			Logger()

		logger.Trace().Msg("Checking if code is updated.")

		currentHCLcode, codeFound, err := loadGeneratedCode(targetpath)
		if err != nil {
			return err
		}
		if !codeFound && genHCL.String() == "" {
			logger.Trace().Msg("Not outdated since file not found and generated_hcl is empty")
			continue
		}

		genHCLCode := prependGenHCLHeader(genHCL.Origin(), genHCL.String())
		if genHCLCode != currentHCLcode {
			logger.Trace().Msg("generate_hcl code is outdated")
			outdatedFiles.add(filename)
		} else {
			logger.Trace().Msg("generate_hcl code is updated")
			outdatedFiles.remove(filename)
		}
	}

	return nil
}

func generateStackHCLCode(
	root string,
	stackpath string,
	meta stack.Metadata,
	globals terramate.Globals,
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
	_, _, err := loadGeneratedCode(path)
	return err
}

// loadGeneratedCode will load the generated code at the given path.
// It returns an error if it can't read the file or if the file is not
// a Terramate generated file.
//
// The returned boolean indicates if the file exists, so the contents of
// the file + true is returned if a file is found, but if no file is found
// it will return an empty string and false indicating that the file doesn't exist.
func loadGeneratedCode(path string) (string, bool, error) {
	logger := log.With().
		Str("action", "loadGeneratedCode()").
		Str("path", path).
		Logger()

	logger.Trace().Msg("Get file information.")

	_, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, errors.E(err, "loading code: can't stat %q", path)
	}

	logger.Trace().Msg("Read file.")

	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, errors.E(err, "loading code, can't read %q", path)
	}

	logger.Trace().Msg("Check if code has terramate header.")

	if hasTerramateHeader(data) {
		return string(data), true, nil
	}

	return "", false, errors.E(ErrManualCodeExists, "check file %q", path)
}

type forEachStackFunc func(stack.S, terramate.Globals) stackReport

func forEachStack(root, workingDir string, fn forEachStackFunc) Report {
	logger := log.With().
		Str("action", "generate.forEachStack()").
		Str("root", root).
		Str("workingDir", workingDir).
		Logger()

	report := Report{}

	logger.Trace().Msg("List stacks.")

	stackEntries, err := terramate.ListStacks(root)
	if err != nil {
		report.BootstrapErr = err
		return report
	}

	for _, entry := range stackEntries {
		stack := entry.Stack

		logger := logger.With().
			Stringer("stack", stack).
			Logger()

		if !strings.HasPrefix(stack.HostPath(), workingDir) {
			logger.Trace().Msg("discarding stack outside working dir")
			continue
		}

		logger.Trace().Msg("Load stack globals.")

		globals, err := terramate.LoadStackGlobals(root, stack)
		if err != nil {
			report.addFailure(stack, errors.E(ErrLoadingGlobals, err))
			continue
		}

		logger.Trace().Msg("Calling stack callback.")

		report.addStackReport(stack, fn(stack, globals))
	}
	report.sortFilenames()
	return report
}

func removeStackGeneratedFiles(stack stack.S) (map[string]string, error) {
	logger := log.With().
		Str("action", "generate.removeStackGeneratedFiles()").
		Stringer("stack", stack).
		Logger()

	logger.Trace().Msg("listing generated files")

	removedFiles := map[string]string{}

	files, err := ListStackGenFiles(stack)
	if err != nil {
		return nil, err
	}

	logger.Trace().Msg("deleting all Terramate generated files")

	for _, filename := range files {
		logger := logger.With().
			Str("filename", filename).
			Logger()

		logger.Trace().Msg("reading current file before removal")

		path := filepath.Join(stack.HostPath(), filename)
		body, err := os.ReadFile(path)
		if err != nil {
			return removedFiles, errors.E(err, "reading gen file before removal")
		}

		logger.Trace().Msg("removing file")

		if err := os.Remove(path); err != nil {
			return removedFiles, errors.E(err, "removing gen file")
		}

		removedFiles[filename] = string(body)
	}
	return removedFiles, nil
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

func checkGeneratedFilesConflicts(genfiles []genfile) error {
	observed := newStringSet()
	for _, genf := range genfiles {
		if observed.has(genf.name) {
			// TODO(katcipis): improve error with origin info
			// Right now it is not as nice/easy as I would like :-(.
			return errors.E("two configurations produce same file %q", genf.name)
		}
		observed.add(genf.name)
	}
	return nil
}

func checkGeneratedFilesPaths(genfiles []genfile) error {
	for _, gen := range genfiles {
		if strings.Contains(gen.name, "/") {
			// TODO(katcipis): improve error with origin info
			return errors.E("'/' not allowed but found on %q", gen.name)
		}
	}
	return nil
}

type stringSet struct {
	vals map[string]struct{}
}

func newStringSet(vals ...string) *stringSet {
	ss := &stringSet{
		vals: map[string]struct{}{},
	}
	for _, v := range vals {
		ss.add(v)
	}
	return ss
}

func (ss *stringSet) remove(val string) {
	delete(ss.vals, val)
}

func (ss *stringSet) has(val string) bool {
	_, ok := ss.vals[val]
	return ok
}

func (ss *stringSet) add(val string) {
	ss.vals[val] = struct{}{}
}

func (ss *stringSet) slice() []string {
	res := make([]string, 0, len(ss.vals))
	for k := range ss.vals {
		res = append(res, k)
	}
	return res
}
