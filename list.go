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
	"io/fs"
	"path/filepath"

	"github.com/mineiros-io/terramate/errors"
	"github.com/mineiros-io/terramate/stack"
	"github.com/rs/zerolog/log"
)

// ListStacks walks the project's root directory looking for terraform stacks.
// It returns a lexicographic sorted list of stack directories.
func ListStacks(loader *stack.Loader, root string) ([]Entry, error) {
	logger := log.With().
		Str("action", "ListStacks()").
		Str("path", root).
		Logger()

	entries := []Entry{}

	logger.Trace().Msg("Walk path.")
	err := filepath.Walk(
		root,
		func(path string, info fs.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if !info.IsDir() {
				return nil
			}

			if info.IsDir() && info.Name() == ".git" {
				return filepath.SkipDir
			}

			logger.Trace().Str("stack", path).Msg("Try load stack.")
			stack, found, err := loader.TryLoad(path)
			if err != nil {
				return err
			}

			if found {
				logger.Debug().Stringer("stack", stack).Msg("Found stack.")
				entries = append(entries, Entry{Stack: stack})
			}

			return nil
		},
	)

	if err != nil {
		return nil, errors.E("listing stacks", err)
	}

	return entries, nil
}
