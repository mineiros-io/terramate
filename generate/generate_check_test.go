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

package generate_test

import (
	"testing"

	"github.com/madlambda/spells/assert"
	"github.com/mineiros-io/terramate/errors"
	"github.com/mineiros-io/terramate/generate"
	"github.com/mineiros-io/terramate/hcl"
	"github.com/mineiros-io/terramate/test/sandbox"
)

func TestCheckReturnsOutdatedStackFilenamesForGeneratedHCL(t *testing.T) {
	s := sandbox.New(t)

	stackEntry := s.CreateStack("stacks/stack")
	stack := stackEntry.Load()

	assertOutdatedFiles := func(want []string) {
		t.Helper()

		got, err := generate.CheckStack(s.RootDir(), stack)
		assert.NoError(t, err)
		assertEqualStringList(t, got, want)
	}

	// Checking detection when there is no config generated yet
	assertOutdatedFiles([]string{})
	stackEntry.CreateConfig(
		stackConfig(
			generateHCL(
				labels("test.tf"),
				content(
					terraform(
						str("required_version", "1.10"),
					),
				),
			),
		).String())
	assertOutdatedFiles([]string{"test.tf"})

	s.Generate()

	assertOutdatedFiles([]string{})

	// Now checking when we have code + it gets outdated.
	stackEntry.CreateConfig(
		stackConfig(
			generateHCL(
				labels("test.tf"),
				content(
					terraform(
						str("required_version", "1.11"),
					),
				),
			),
		).String())

	assertOutdatedFiles([]string{"test.tf"})

	s.Generate()

	// Changing generated filenames will trigger detection,
	// with new + old filenames.
	stackEntry.CreateConfig(
		stackConfig(
			generateHCL(
				labels("testnew.tf"),
				content(
					terraform(
						str("required_version", "1.11"),
					),
				),
			),
		).String())

	assertOutdatedFiles([]string{"test.tf", "testnew.tf"})

	// Adding new filename to generation trigger detection
	stackEntry.CreateConfig(
		stackConfig(
			generateHCL(
				labels("testnew.tf"),
				content(
					terraform(
						str("required_version", "1.11"),
					),
				),
			),
			generateHCL(
				labels("another.tf"),
				content(
					backend(
						labels("type"),
					),
				),
			),
		).String())

	assertOutdatedFiles([]string{"another.tf", "test.tf", "testnew.tf"})

	s.Generate()

	assertOutdatedFiles([]string{})

	// Detects configurations that have been removed.
	stackEntry.CreateConfig(stackConfig().String())

	assertOutdatedFiles([]string{"another.tf", "testnew.tf"})

	s.Generate()

	assertOutdatedFiles([]string{})
}

func TestCheckOutdatedIgnoresEmptyGenerateHCLBlocks(t *testing.T) {
	s := sandbox.New(t)

	stackEntry := s.CreateStack("stacks/stack")
	stack := stackEntry.Load()

	assertOutdatedFiles := func(want []string) {
		t.Helper()

		got, err := generate.CheckStack(s.RootDir(), stack)
		assert.NoError(t, err)
		assertEqualStringList(t, got, want)
	}

	// Checking detection when the config is empty at first
	stackEntry.CreateConfig(
		stackConfig(
			generateHCL(
				labels("test.tf"),
				content(),
			),
		).String())

	assertOutdatedFiles([]string{})

	// Checking detection when the config isnt empty, is generated and then becomes empty
	stackEntry.CreateConfig(
		stackConfig(
			generateHCL(
				labels("test.tf"),
				content(
					block("whatever"),
				),
			),
		).String())

	assertOutdatedFiles([]string{"test.tf"})

	s.Generate()

	assertOutdatedFiles([]string{})

	stackEntry.CreateConfig(
		stackConfig(
			generateHCL(
				labels("test.tf"),
				content(),
			),
		).String())

	assertOutdatedFiles([]string{"test.tf"})

	s.Generate()

	assertOutdatedFiles([]string{})
}

func TestCheckFailsWithInvalidConfig(t *testing.T) {
	invalidConfigs := []string{
		hcldoc(
			generateHCL(
				expr("undefined", "terramate.undefined"),
			),
			stack(),
		).String(),

		hcldoc(
			generateHCL(
				labels("test.tf"),
			),
			stack(),
		).String(),

		hcldoc(
			generateHCL(
				labels("test.tf"),
				block("content"),
				expr("unrecognized", `"value"`),
			),
			stack(),
		).String(),
	}

	for _, invalidConfig := range invalidConfigs {
		s := sandbox.New(t)

		stackEntry := s.CreateStack("stack")
		stackEntry.CreateConfig(invalidConfig)

		loader := s.StackLoader()
		_, err := loader.Load(stackEntry.Path())
		assert.IsError(t, err, errors.E(hcl.ErrTerramateSchema))
	}
}
