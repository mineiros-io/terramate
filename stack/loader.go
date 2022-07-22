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

package stack

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mineiros-io/terramate/errors"
	"github.com/mineiros-io/terramate/project"
	"github.com/rs/zerolog/log"
)

// Loader is a stack loader.
type Loader struct {
	root   string
	stacks map[string]*S
}

// NewLoader creates a new stack loader for project's root directory.
func NewLoader(root string) Loader {
	return Loader{
		root:   root,
		stacks: make(map[string]*S),
	}
}

// Load loads a stack from dir directory.
// The provided directory must be an absolute path to the stack dir.
// If the stack was previously loaded, it returns the cached one.
func (l Loader) Load(dir string) (*S, error) {
	stack, found, err := l.TryLoad(dir)
	if err != nil {
		return nil, err
	}

	if !found {
		return nil, fmt.Errorf("directory %q is not a stack", dir)
	}

	return stack, nil
}

// TryLoad tries to load a stack from directory. It returns found as true
// only in the case that path contains a stack and it was correctly parsed.
// It caches the stack for later use.
func (l Loader) TryLoad(dir string) (stack *S, found bool, err error) {
	logger := log.With().
		Str("action", "Loader.TryLoad()").
		Str("dir", dir).
		Logger()

	logger.Trace().Msg("Get project absolute stack path.")

	stackpath := project.PrjAbsPath(l.root, dir)
	if s, ok := l.stacks[stackpath]; ok {
		return s, true, nil
	}

	stack, found, err = TryLoad(l.root, dir)
	if err != nil {
		return nil, found, err
	}

	if !found {
		return nil, false, nil
	}

	l.stacks[stack.Path()] = stack
	return stack, true, nil
}

// TryLoadChanged is like TryLoad but sets the stack as changed if loaded
// successfully.
func (l Loader) TryLoadChanged(root, dir string) (stack *S, found bool, err error) {
	logger := log.With().
		Str("action", "TryLoadChanged()").
		Str("stack", dir).
		Logger()

	logger.Debug().
		Str("path", dir).
		Msg("Try load.")
	s, ok, err := l.TryLoad(dir)
	if ok {
		s.changed = true
	}
	return s, ok, err
}

// Set stacks in the loader's cache. The dir directory must be relative to
// project's root.
func (l Loader) Set(dir string, s *S) {
	l.stacks[dir] = s
}

// LoadAll loads all the stacks in the dirs paths. For each dir in dirs:
// - If it is relative, it will be considered relative to wd, path = wd + dir
// - If it is absolute, it will be considered absolute in relation to the given root, path = root + dir
// - If it's not a stack, it will recursively try to load the stacks from subfolders.
func (l Loader) LoadAll(root string, wd string, dirs ...string) (List, error) {
	logger := log.With().
		Str("action", "LoadAll()").
		Logger()

	stacks := List{}

	logger.Trace().
		Str("path", root).
		Msg("Range over directories.")

	for _, d := range dirs {
		if filepath.IsAbs(d) {
			d = filepath.Join(root, d)
		} else {
			d = filepath.Join(wd, d)
		}

		logger.Debug().
			Str("stack", d).
			Msg("Load stack.")
		stack, found, err := l.TryLoad(d)
		if err != nil {
			return nil, err
		}

		if found {
			stacks = append(stacks, stack)
		}

		subdirs, err := listDirs(d)
		if err != nil {
			return nil, err
		}

		substacks, err := l.LoadAll(root, d, subdirs...)
		if err != nil {
			return nil, errors.E(err, "loading sub stacks of %q", d)
		}
		stacks = append(stacks, substacks...)
	}
	return stacks, nil
}

func (l Loader) lookupParentStack(dir string) (stack *S, found bool, err error) {
	if l.root == dir {
		return nil, false, nil
	}
	d := filepath.Dir(dir)
	for {
		log.Debug().
			Str("action", "lookupParentStack()").
			Str("stack", dir).
			Str("path", d).
			Msg("Try load directory.")
		stack, ok, err := l.TryLoad(d)
		if err != nil {
			return nil, false, fmt.Errorf("looking for parent stacks: %w", err)
		}

		if ok {
			return stack, true, nil
		}

		if d == l.root || d == "/" {
			break
		}

		log.Trace().
			Str("action", "lookupParentStack()").
			Str("stack", dir).
			Msg("Get git path.")
		gitpath := filepath.Join(d, ".git")
		if _, err := os.Stat(gitpath); err == nil {
			// if reached root of git project, abort scanning
			break
		}

		d = filepath.Dir(d)
	}

	return nil, false, nil
}

func listDirs(dir string) ([]string, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, f := range files {
		if f.IsDir() {
			dirs = append(dirs, f.Name())
		}
	}
	return dirs, nil
}
