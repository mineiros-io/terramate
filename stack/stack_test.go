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

package stack_test

import (
	"path/filepath"
	"testing"

	"github.com/madlambda/spells/assert"
	"github.com/mineiros-io/terramate/errors"
	"github.com/mineiros-io/terramate/stack"
	"github.com/mineiros-io/terramate/test/sandbox"
	"github.com/rs/zerolog"
)

func TestLeafDetectionIgnoresGitIgnoredDirs(t *testing.T) {
	s := sandbox.New(t)

	// To create stacks inside stacks we need to skip
	// stack initialization, since it has a protection
	// against it.
	for _, stack := range []string{
		"/stacks/stack",
		"/stacks/stack/substack",
		"/stacks/stack/substack/more-sub-stacks",
	} {
		dirEntry := s.CreateDir(stack)
		dirEntry.CreateFile("stack.tm", "stack{}")
	}

	stackAbsPath := filepath.Join(s.RootDir(), "/stacks/stack")

	loader := stack.NewLoaderWithGit(s.RootDir(), s.Git().Unwrap())

	assertStackIsLeaf := func(wantIsLeaf bool, wantErr error) {
		t.Helper()

		got, err := loader.IsLeafStack(stackAbsPath)
		assert.IsError(t, err, wantErr)

		if got != wantIsLeaf {
			t.Fatalf("got isLeaf %t != want %t", got, wantIsLeaf)
		}
	}

	assertStackIsLeaf(false, errors.E(stack.ErrHasSubstacks))

	s.RootEntry().CreateFile(".gitignore", "**/substack/*")

	assertStackIsLeaf(true, nil)
}

func init() {
	zerolog.SetGlobalLevel(zerolog.Disabled)
}
