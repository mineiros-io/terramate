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
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mineiros-io/terramate/errors"
	"github.com/mineiros-io/terramate/git"
	"github.com/mineiros-io/terramate/hcl"
	"github.com/mineiros-io/terramate/project"
	"github.com/rs/zerolog/log"
)

// Loader is a stack loader.
type Loader struct {
	root   string
	stacks map[string]S
	git    *git.Git
}

const (
	// ErrHasSubstacks indicates that a stack has a sub-stack (not leaf)
	ErrHasSubstacks errors.Kind = "stack has substacks"
)

// NewLoader creates a new stack loader for project's root directory.
func NewLoader(root string) *Loader {
	return NewLoaderWithGit(root, nil)
}

// NewLoader creates a new stack loader for project's root directory with git
// support.
func NewLoaderWithGit(root string, git *git.Git) *Loader {
	return &Loader{
		root:   root,
		stacks: make(map[string]S),
		git:    git,
	}
}

// Load loads a stack from dir directory.
// The provided directory must be an absolute path to the stack dir.
// If the stack was previously loaded, it returns the cached one.
func (l *Loader) Load(dir string) (S, error) {
	stack, found, err := l.TryLoad(dir)
	if err != nil {
		return S{}, err
	}

	if !found {
		return S{}, fmt.Errorf("directory %q is not a stack", dir)
	}

	return stack, nil
}

// TryLoad tries to load a stack from directory. It returns found as true
// only in the case that path contains a stack and it was correctly parsed.
// It caches the stack for later use.
func (l *Loader) TryLoad(dir string) (stack S, found bool, err error) {
	logger := log.With().
		Str("action", "Loader.TryLoad()").
		Str("dir", dir).
		Logger()

	logger.Trace().Msg("Get project absolute stack path.")

	stackpath := project.PrjAbsPath(l.root, dir)
	if s, ok := l.stacks[stackpath]; ok {
		return s, true, nil
	}

	stack, found, err = l.tryLoad(dir)
	if !found {
		return S{}, found, err
	}

	l.stacks[stack.Path()] = stack
	return stack, true, nil
}

// TryLoadChanged is like TryLoad but sets the stack as changed if loaded
// successfully.
func (l *Loader) TryLoadChanged(root, dir string) (stack S, found bool, err error) {
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
func (l *Loader) Set(dir string, s S) {
	l.stacks[dir] = s
}

// LoadAll loads all the stacks in the dirs paths. For each dir in dirs:
// - If it is relative, it will be considered relative to wd, path = wd + dir
// - If it is absolute, it will be considered absolute in relation to the given root, path = root + dir
func (l *Loader) LoadAll(root string, wd string, dirs ...string) ([]S, error) {
	logger := log.With().
		Str("action", "LoadAll()").
		Logger()

	stacks := []S{}

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
		stack, err := l.Load(d)
		if err != nil {
			return nil, err
		}

		stacks = append(stacks, stack)
	}
	return stacks, nil
}

// IsLeafStack returns true if dir is a leaf stack.
func (l *Loader) IsLeafStack(dir string) (bool, error) {
	isValid := true
	log.Trace().
		Str("action", "IsLeafStack()").
		Str("stack", dir).
		Msg("Walk directory.")
	err := filepath.Walk(
		dir,
		func(path string, info fs.FileInfo, err error) error {
			if !isValid {
				return filepath.SkipDir
			}
			if err != nil {
				return err
			}
			if path == dir {
				return nil
			}
			if l.git != nil {
				relpath := project.PrjAbsPath(l.root, path)
				isIgnored, err := l.git.IsIgnored(relpath[1:]) // skip the / in the beginning
				if err != nil {
					return err
				}

				fmt.Printf("path %s (%t)\n", relpath, isIgnored)

				if isIgnored {
					return filepath.SkipDir
				}
			}
			if info.IsDir() {
				if strings.HasSuffix(path, "/.git") {
					return filepath.SkipDir
				}

				log.Trace().
					Str("action", "IsLeafStack()").
					Str("stack", dir).
					Str("path", path).
					Msg("Try load.")
				_, found, err := l.TryLoad(path)
				if err != nil {
					return err
				}

				isValid = !found
				return nil
			}
			return nil
		},
	)
	if err != nil {
		return false, err
	}

	return isValid, nil
}

// LookupParentStack returns parent stack of given dir.
// Returns false, nil if the given dir has no parent stack.
func (l Loader) LookupParentStack(dir string) (stack S, found bool, err error) {
	if l.root == dir {
		return S{}, false, nil
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
			return S{}, false, fmt.Errorf("looking for parent stacks: %w", err)
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

	return S{}, false, nil
}

// TryLoad tries to load a single stack from dir. It sets found as true in case
// the stack was successfully loaded.
func (l *Loader) tryLoad(absdir string) (stack S, found bool, err error) {
	logger := log.With().
		Str("action", "tryLoad()").
		Str("dir", absdir).
		Logger()

	if !strings.HasPrefix(absdir, l.root) {
		return S{}, false, errors.E(
			fmt.Sprintf("directory %q is not inside project root %q", absdir, l.root))
	}

	if l.git != nil {
		isIgnored, err := l.git.IsIgnored(absdir)
		if err != nil {
			return S{}, false, err
		}

		if isIgnored {
			return S{}, false, nil
		}
	}

	logger.Debug().Msg("Parsing configuration.")
	cfg, err := hcl.ParseDir(absdir)
	if err != nil {
		return S{}, false, err
	}

	if cfg.Stack == nil {
		return S{}, false, nil
	}

	ok, err := l.IsLeafStack(absdir)
	if err != nil {
		return S{}, false, err
	}

	if !ok {
		return S{}, false, errors.E(ErrHasSubstacks, fmt.Sprintf("dir %q", absdir))
	}

	logger.Debug().Msg("Create a new stack")
	return New(l.root, cfg), true, nil
}

func (l *Loader) Git() *git.Git { return l.git }
