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

// Package sandbox provides an easy way to setup isolated terramate projects
// that can be used on testing, acting like sandboxes.
//
// It helps with:
//
// - git initialization/operations
// - Terraform module creation
// - Terramate stack creation
package sandbox

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/madlambda/spells/assert"
	"github.com/mineiros-io/terramate"
	"github.com/mineiros-io/terramate/config"
	"github.com/mineiros-io/terramate/generate"
	"github.com/mineiros-io/terramate/hcl"
	"github.com/mineiros-io/terramate/stack"
	"github.com/mineiros-io/terramate/test"
)

// S is a full sandbox with its own base dir that is an initialized git repo for
// test purposes.
type S struct {
	t           *testing.T
	git         *Git
	rootdir     string
	stackLoader *stack.Loader
}

// DirEntry represents a directory and can be used to create files inside the
// directory.
type DirEntry struct {
	t        *testing.T
	rootpath string
	abspath  string
	relpath  string
}

// StackEntry represents a directory that's also a stack.
// It extends a DirEntry with stack specific functionality.
type StackEntry struct {
	loader *stack.Loader
	DirEntry
}

// FileEntry represents a file and can be used to manipulate the file contents.
// It is optimized for reading/writing all contents, not stream programming
// (io.Reader/io.Writer).
// It has limited usefulness but it is easier to work with for testing.
type FileEntry struct {
	t    *testing.T
	path string
}

// New creates a new complete test sandbox.
// The git repository is set up with sane defaults.
//
// It is a programming error to use a test env created with a *testing.T other
// than the one of the test using the test env, for a new test/sub-test always
// create a new test env for it.
func New(t *testing.T) S {
	s := NoGit(t)

	s.git = NewGit(t, s.RootDir())
	s.git.Init()
	s.stackLoader = stack.NewLoaderWithGit(s.rootdir, s.git.Unwrap())
	return s
}

// NewWithGitConfig creates a new sandbox using the cfg configuration for the
// git repository.
func NewWithGitConfig(t *testing.T, cfg GitConfig) S {
	s := NoGit(t)

	cfg.repoDir = s.RootDir()

	s.git = NewGitWithConfig(t, cfg)
	s.git.Init()
	return s
}

// NoGit creates a new test sandbox with no git repository.
//
// It is a programming error to use a test env created with a *testing.T other
// than the one of the test using the test env, for a new test/sub-test always
// create a new test env for it.
func NoGit(t *testing.T) S {
	t.Helper()

	rootdir := test.CanonPath(t, t.TempDir())
	return S{
		t:       t,
		rootdir: rootdir,
	}
}

// BuildTree builds a tree layout based on the layout specification, defined
// below:
// Each string in the slice represents a filesystem operation, and each
// operation has the format below:
//   <kind>:<relative path>[:data]
// Where kind is one of the below:
//   "d" for directory creation.
//   "s" for initialized stacks.
//   "f" for file creation.
//   "t" for terramate block.
// The data field is required only for operation "f" and "s":
//   For "f" data is the content of the file to be created.
//   For "s" data is a key value pair of the form:
//     <attr1>=<val1>[;<attr2>=<val2>]
// Where attrN is a string attribute of the terramate block of the stack.
// TODO(i4k): document empty data field.
//
// Example:
//   s:name-of-the-stack:version=1.0;after=["other-stack"]
//
// This is an internal mini-lang used to simplify testcases, so it expects well
// formed layout specification.
func (s S) BuildTree(layout []string) {
	t := s.t
	t.Helper()

	parsePathData := func(spec string) (string, string) {
		tmp := spec[2:]
		if len(tmp) == 0 {
			// relative to s.rootdir
			return ".", ""
		}
		index := strings.IndexByte(tmp, ':')
		if index == -1 {
			return tmp, ""
		}
		path := tmp[0:index]
		data := tmp[index+1:]
		return path, data
	}

	gentmfile := func(relpath, data string) {
		attrs := strings.Split(data, ";")

		cfgdir := filepath.Join(s.RootDir(), relpath)
		test.MkdirAll(t, cfgdir)
		cfg, err := hcl.NewConfig(cfgdir)
		assert.NoError(t, err)

		cfg.Stack = &hcl.Stack{}
		cfg.Terramate = &hcl.Terramate{}

		for _, attr := range attrs {
			parts := strings.Split(attr, "=")
			name := parts[0]
			value := parts[1]
			switch name {
			case "after":
				cfg.Stack.After = parseListSpec(t, name, value)
			case "before":
				cfg.Stack.Before = parseListSpec(t, name, value)
			case "wants":
				cfg.Stack.Wants = parseListSpec(t, name, value)
			case "description":
				cfg.Stack.Description = value
			default:
				t.Fatalf("attribute " + parts[0] + " not supported.")
			}
		}

		assert.NoError(t, cfg.Save(config.DefaultFilename),
			"BuildTree() failed to generate config file.")
	}

	for _, spec := range layout {
		path, data := parsePathData(spec)

		specKind := string(spec[0:2])
		switch specKind {
		case "d:":
			test.MkdirAll(t, filepath.Join(s.rootdir, spec[2:]))
		case "s:":
			if data == "" {
				s.CreateStack(path)
				continue
			}

			gentmfile(path, data)
		case "f:":
			test.WriteFile(t, s.rootdir, path, data)
		default:
			t.Fatalf("unknown spec kind: %q", specKind)
		}
	}
}

// Git returns a git wrapper that is useful to run git commands safely inside
// the test env repo.
func (s S) Git() *Git {
	if s.git == nil {
		s.t.Fatal("git not initialized for the sandbox")
	}

	return s.git
}

// Generate generates code for all stacks on the sandbox
func (s S) Generate() generate.Report {
	t := s.t
	t.Helper()

	report := generate.Do(s.StackLoader(), s.RootDir(), s.RootDir())
	for _, failure := range report.Failures {
		t.Errorf("Generate unexpected failure: %v", failure)
	}
	return report
}

// LoadStacks load all stacks from sandbox rootdir.
func (s S) LoadStacks() []stack.S {
	s.t.Helper()

	entries, err := terramate.ListStacks(s.StackLoader(), s.rootdir)
	assert.NoError(s.t, err)

	var stacks []stack.S
	for _, entry := range entries {
		stacks = append(stacks, entry.Stack)
	}

	return stacks
}

// LoadStackGlobals loads globals for specific stack on the sandbox.
// Fails the caller test if an error is found.
func (s S) LoadStackGlobals(sm stack.Metadata) stack.Globals {
	s.t.Helper()

	g, err := stack.LoadGlobals(s.RootDir(), sm)
	assert.NoError(s.t, err)
	return g
}

// RootDir returns the root directory of the test env. All dirs/files created
// through the test env will be included inside this dir.
//
// It is a programming error to delete this dir, it will be automatically
// removed when the test finishes.
func (s S) RootDir() string {
	return s.rootdir
}

// RootEntry returns a DirEntry for the root directory of the test env.
func (s S) RootEntry() DirEntry {
	return s.DirEntry(".")
}

// CreateModule will create a module dir with the given relative path, returning
// a directory entry that can be used to create files inside the module dir.
func (s S) CreateModule(relpath string) DirEntry {
	t := s.t
	t.Helper()

	if filepath.IsAbs(relpath) {
		t.Fatalf("CreateModule() needs a relative path but given %q", relpath)
	}

	return newDirEntry(s.t, s.rootdir, relpath)
}

// CreateDir will create a dir with the given relative path and returns the dir
// entry for it.
//
// If the path is absolute, it will be considered in relation to the sandbox
// root dir.
func (s S) CreateDir(relpath string) DirEntry {
	t := s.t
	t.Helper()

	if filepath.IsAbs(relpath) {
		relpath = relpath[1:]
	}

	return newDirEntry(t, s.RootDir(), relpath)
}

// CreateStack will create a stack dir with the given relative path and
// initializes the stack, returning a stack entry that can be used
// to create files inside the stack dir.
//
// If the path is absolute, it will be considered in relation to the sandbox
// root dir.
func (s S) CreateStack(relpath string) StackEntry {
	t := s.t
	t.Helper()

	if filepath.IsAbs(relpath) {
		relpath = relpath[1:]
	}

	stack := newStackEntry(t, s, relpath)
	assert.NoError(t, terramate.Init(s.StackLoader(), s.RootDir(), stack.Path()))
	return stack
}

// StackEntry gets the stack entry of the stack identified by relpath.
// The stack must exist (previously created).
func (s S) StackEntry(relpath string) StackEntry {
	return newStackEntry(s.t, s, relpath)
}

// DirEntry gets the dir entry for relpath.
// The dir must exist and must be a relative path to the sandbox root dir.
func (s S) DirEntry(relpath string) DirEntry {
	t := s.t
	t.Helper()

	if filepath.IsAbs(relpath) {
		t.Fatalf("DirEntry() needs a relative path but given %q", relpath)
	}

	abspath := filepath.Join(s.rootdir, relpath)
	stat, err := os.Stat(abspath)
	if err != nil {
		t.Fatalf("DirEntry(): dir must exist: %v", err)
	}

	if !stat.IsDir() {
		t.Fatalf("DirEntry(): %q is not directory", abspath)
	}

	return DirEntry{
		t:       t,
		abspath: abspath,
		relpath: relpath,
	}
}

func (s S) StackLoader() *stack.Loader {
	return s.stackLoader
}

// CreateFile will create a file inside this dir entry with the given name and
// the given body. The body can be plain text or a format string identical to
// what is defined on Go fmt package.
//
// It returns a file entry that can be used to further manipulate the created
// file, like replacing its contents. The file entry is optimized for always
// replacing the file contents, not streaming (using file as io.Writer).
//
// If the file already exists its contents will be truncated, like os.Create
// behavior: https://pkg.go.dev/os#Create
func (de DirEntry) CreateFile(name, body string, args ...interface{}) FileEntry {
	de.t.Helper()

	fe := FileEntry{
		t:    de.t,
		path: filepath.Join(de.abspath, name),
	}
	fe.Write(body, args...)

	return fe
}

// CreateConfig will create a terramate config file inside this dir entry with
// the given body.
//
// It returns a file entry that can be used to further manipulate the created
// file, like replacing its contents. The file entry is optimized for always
// replacing the file contents, not streaming (using file as io.Writer).
//
// If the file already exists its contents will be truncated, like os.Create
// behavior: https://pkg.go.dev/os#Create
func (de DirEntry) CreateConfig(body string) FileEntry {
	de.t.Helper()

	fe := FileEntry{
		t:    de.t,
		path: filepath.Join(de.abspath, config.DefaultFilename),
	}
	fe.Write(body)
	return fe
}

// CreateDir creates a directory inside the dir entry directory. The relpath
// must be relative to the stack directory.
func (de DirEntry) CreateDir(relpath string) DirEntry {
	return newDirEntry(de.t, de.abspath, relpath)
}

// ReadFile will read a file inside this dir entry with the given name.
// It will fail the test if the file doesn't exist, since it assumes an
// expectation on the file being there.
func (de DirEntry) ReadFile(name string) []byte {
	de.t.Helper()
	return test.ReadFile(de.t, de.abspath, name)
}

// RemoveFile will delete a file inside this dir entry with the given name.
// It will succeeds if the file already doesn't exist.
func (de DirEntry) RemoveFile(name string) {
	de.t.Helper()
	test.RemoveFile(de.t, de.abspath, name)
}

// Path returns the absolute path of the directory entry.
func (de DirEntry) Path() string {
	return de.abspath
}

// RelPath returns the relative path of the directory entry.
func (de DirEntry) RelPath() string {
	return de.relpath
}

// Write writes the given text body on the file, replacing its contents.
// The body can be plain text or a format string identical to what is defined on
// Go fmt package.
//
// It behaves like os.WriteFile: https://pkg.go.dev/os#WriteFile
func (fe FileEntry) Write(body string, args ...interface{}) {
	fe.t.Helper()

	body = fmt.Sprintf(body, args...)

	if err := os.WriteFile(fe.path, []byte(body), 0700); err != nil {
		fe.t.Fatalf("os.WriteFile(%q) = %v", fe.path, err)
	}
}

// Path returns the absolute path of the file.
func (fe FileEntry) Path() string {
	return fe.path
}

// ModSource returns the relative import path for the module with the given
// module dir entry. The path is relative to stack dir itself (hence suitable to
// be a module source path).
func (se StackEntry) ModSource(mod DirEntry) string {
	relpath, err := filepath.Rel(se.abspath, mod.abspath)
	assert.NoError(se.t, err)
	return relpath
}

// WriteConfig will create a terramate configuration file on the stack
// or replace the current config if there is one.
func (se StackEntry) WriteConfig(cfg hcl.Config) {
	var out bytes.Buffer
	assert.NoError(se.t, hcl.PrintConfig(&out, cfg))
	se.DirEntry.CreateFile(config.DefaultFilename, out.String())
}

// ListGenFiles lists all files generated by Terramate for this stack.
func (se StackEntry) ListGenFiles() []string {
	se.t.Helper()
	files, err := generate.ListStackGenFiles(se.Load())
	assert.NoError(se.t, err, "listing stack generated files")
	return files
}

// ReadGeneratedHCL will read code that was generated by Terramate for this stack
// using generate_hcl blocks.
// The given name is the name of the generate_hcl block as indicated by its label.
//
// It will fail the test if there is no generated code available on the stack,
// since it assumes generated code is expected to be there.
func (se StackEntry) ReadGeneratedHCL(name string) string {
	se.t.Helper()
	return string(se.DirEntry.ReadFile(name))
}

// RemoveGeneratedHCL will delete the file with generated code from
// generate_hcl blocks.
// The given name is the name of the generate_hcl block as indicated by its label.
func (se StackEntry) RemoveGeneratedHCL(name string) {
	se.t.Helper()
	se.DirEntry.RemoveFile(name)
}

// Load loads the terramate stack instance for this stack dir entry.
func (se StackEntry) Load() stack.S {
	se.t.Helper()

	loadedStack, err := se.loader.Load(se.Path())
	assert.NoError(se.t, err)
	return loadedStack
}

// Path returns the absolute path of the stack.
func (se StackEntry) Path() string {
	return se.DirEntry.abspath
}

// RelPath returns the relative path of the stack. It is relative to the base
// dir of the test environment that created this stack.
func (se StackEntry) RelPath() string {
	return se.DirEntry.relpath
}

func newDirEntry(t *testing.T, rootdir string, relpath string) DirEntry {
	t.Helper()

	abspath := filepath.Join(rootdir, relpath)
	test.MkdirAll(t, abspath)

	return DirEntry{
		t:        t,
		rootpath: rootdir,
		abspath:  abspath,
		relpath:  relpath,
	}
}

func newStackEntry(t *testing.T, s S, relpath string) StackEntry {
	return StackEntry{
		DirEntry: newDirEntry(t, s.RootDir(), relpath),
		loader:   s.StackLoader(),
	}
}

func parseListSpec(t *testing.T, name, value string) []string {
	if !strings.HasPrefix(value, "[") ||
		!strings.HasSuffix(value, "]") {
		t.Fatalf("malformed %q value: %q", name, value)
	}
	quotedList := strings.Split(value[1:len(value)-1], ",")
	list := make([]string, 0, len(quotedList))
	for _, l := range quotedList {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}

		if !strings.HasPrefix(l, `"`) {
			t.Fatalf("expect quoted strings but given %q", l)
		}

		var err error
		val, err := strconv.Unquote(l)
		assert.NoError(t, err, "list item not properly quoted")

		list = append(list, val)
	}

	return list
}
