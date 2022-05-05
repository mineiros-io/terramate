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
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madlambda/spells/assert"
	"github.com/mineiros-io/terramate/config"
	"github.com/mineiros-io/terramate/errors"
	"github.com/mineiros-io/terramate/generate"
	"github.com/mineiros-io/terramate/test/hclwrite"
	"github.com/mineiros-io/terramate/test/sandbox"
)

func TestHCLGeneration(t *testing.T) {
	type (
		generatedHCL struct {
			stack string
			hcls  map[string]fmt.Stringer
		}
		testcase struct {
			name       string
			layout     []string
			configs    []hclconfig
			workingDir string
			wantHCL    []generatedHCL
			wantReport generate.Report
		}
	)

	provider := func(builders ...hclwrite.BlockBuilder) *hclwrite.Block {
		return hclwrite.BuildBlock("provider", builders...)
	}
	requiredProviders := func(builders ...hclwrite.BlockBuilder) *hclwrite.Block {
		return hclwrite.BuildBlock("required_providers", builders...)
	}
	attr := func(name, expr string) hclwrite.BlockBuilder {
		t.Helper()
		return hclwrite.AttributeValue(t, name, expr)
	}

	tcases := []testcase{
		{
			name: "no generated HCL",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2",
			},
		},
		{
			name: "empty generate_hcl block generates nothing",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2",
			},
			configs: []hclconfig{
				{
					path: "/stacks",
					add: generateHCL(
						labels("empty"),
						content(),
					),
				},
			},
		},
		{
			name: "generate HCL for all stacks on parent",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2",
			},
			configs: []hclconfig{
				{
					path: "/stacks",
					add: hcldoc(
						generateHCL(
							labels("backend.tf"),
							content(
								backend(
									labels("test"),
									expr("prefix", "global.backend_prefix"),
								),
							),
						),
						generateHCL(
							labels("locals.tf"),
							content(
								locals(
									expr("stackpath", "terramate.path"),
									expr("local_a", "global.local_a"),
									expr("local_b", "global.local_b"),
									expr("local_c", "global.local_c"),
									expr("local_d", "tm_try(global.local_d.field, null)"),
								),
							),
						),
						generateHCL(
							labels("provider.tf"),
							content(
								provider(
									labels("name"),
									expr("data", "global.provider_data"),
								),
								terraform(
									requiredProviders(
										expr("name", `{
										source  = "integrations/name"
										version = global.provider_version
									}`),
									),
								),
								terraform(
									expr("required_version", "global.terraform_version"),
								),
							),
						),
					),
				},
				{
					path: "/stacks/stack-1",
					add: globals(
						str("local_a", "stack-1-local"),
						boolean("local_b", true),
						number("local_c", 666),
						attr("local_d", `{ field = "local_d_field"}`),
						str("backend_prefix", "stack-1-backend"),
						str("provider_data", "stack-1-provider-data"),
						str("provider_version", "stack-1-provider-version"),
						str("terraform_version", "stack-1-terraform-version"),
					),
				},
				{
					path: "/stacks/stack-2",
					add: globals(
						str("local_a", "stack-2-local"),
						boolean("local_b", false),
						number("local_c", 777),
						attr("local_d", `{ oopsie = "local_d_field"}`),
						str("backend_prefix", "stack-2-backend"),
						str("provider_data", "stack-2-provider-data"),
						str("provider_version", "stack-2-provider-version"),
						str("terraform_version", "stack-2-terraform-version"),
					),
				},
			},
			wantHCL: []generatedHCL{
				{
					stack: "/stacks/stack-1",
					hcls: map[string]fmt.Stringer{
						"backend.tf": backend(
							labels("test"),
							str("prefix", "stack-1-backend"),
						),
						"locals.tf": locals(
							str("local_a", "stack-1-local"),
							boolean("local_b", true),
							number("local_c", 666),
							str("local_d", "local_d_field"),
							str("stackpath", "/stacks/stack-1"),
						),
						"provider.tf": hcldoc(
							provider(
								labels("name"),
								str("data", "stack-1-provider-data"),
							),
							terraform(
								requiredProviders(
									attr("name", `{
										source  = "integrations/name"
										version = "stack-1-provider-version"
									}`),
								),
							),
							terraform(
								str("required_version", "stack-1-terraform-version"),
							),
						),
					},
				},
				{
					stack: "/stacks/stack-2",
					hcls: map[string]fmt.Stringer{
						"backend.tf": backend(
							labels("test"),
							str("prefix", "stack-2-backend"),
						),
						"locals.tf": locals(
							str("local_a", "stack-2-local"),
							boolean("local_b", false),
							number("local_c", 777),
							attr("local_d", "null"),
							str("stackpath", "/stacks/stack-2"),
						),
						"provider.tf": hcldoc(
							provider(
								labels("name"),
								str("data", "stack-2-provider-data"),
							),
							terraform(
								requiredProviders(
									attr("name", `{
										source  = "integrations/name"
										version = "stack-2-provider-version"
									}`),
								),
							),
							terraform(
								str("required_version", "stack-2-terraform-version"),
							),
						),
					},
				},
			},
			wantReport: generate.Report{
				Successes: []generate.Result{
					{
						StackPath: "/stacks/stack-1",
						Created:   []string{"backend.tf", "locals.tf", "provider.tf"},
					},
					{
						StackPath: "/stacks/stack-2",
						Created:   []string{"backend.tf", "locals.tf", "provider.tf"},
					},
				},
			},
		},
		{
			name: "generate HCL with traversal of unknown namespaces",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2",
			},
			configs: []hclconfig{
				{
					path: "/stacks",
					add: hcldoc(
						generateHCL(
							labels("traversal.tf"),
							content(
								block("traversal",
									expr("locals", "local.hi"),
									expr("some_anything", "something.should_work"),
									expr("multiple_traversal", "one.two.three.four.five"),
								),
							),
						),
					),
				},
			},
			wantHCL: []generatedHCL{
				{
					stack: "/stacks/stack-1",
					hcls: map[string]fmt.Stringer{
						"traversal.tf": hcldoc(
							block("traversal",
								expr("locals", "local.hi"),
								expr("multiple_traversal", "one.two.three.four.five"),
								expr("some_anything", "something.should_work"),
							),
						),
					},
				},
				{
					stack: "/stacks/stack-2",
					hcls: map[string]fmt.Stringer{
						"traversal.tf": hcldoc(
							block("traversal",
								expr("locals", "local.hi"),
								expr("multiple_traversal", "one.two.three.four.five"),
								expr("some_anything", "something.should_work"),
							),
						),
					},
				},
			},
			wantReport: generate.Report{
				Successes: []generate.Result{
					{
						StackPath: "/stacks/stack-1",
						Created:   []string{"traversal.tf"},
					},
					{
						StackPath: "/stacks/stack-2",
						Created:   []string{"traversal.tf"},
					},
				},
			},
		},
		{
			// TODO(katcipis): define a proper behavior where
			// directories are allowed but in a constrained fashion.
			// This is a quick fix to avoid creating files on arbitrary
			// places around the file system.
			name: "generate HCL with dir separators on label name fails",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2",
				"s:stacks/stack-3",
				"s:stacks/stack-4",
			},
			configs: []hclconfig{
				{
					path: "/stacks/stack-1",
					add: hcldoc(
						generateHCL(
							labels("/name.tf"),
							content(
								block("something"),
							),
						),
					),
				},
				{
					path: "/stacks/stack-2",
					add: hcldoc(
						generateHCL(
							labels("./name.tf"),
							content(
								block("something"),
							),
						),
					),
				},
				{
					path: "/stacks/stack-3",
					add: hcldoc(
						generateHCL(
							labels("./dir/name.tf"),
							content(
								block("something"),
							),
						),
					),
				},
				{
					path: "/stacks/stack-4",
					add: hcldoc(
						generateHCL(
							labels("dir/name.tf"),
							content(
								block("something"),
							),
						),
					),
				},
			},
			wantReport: generate.Report{
				Failures: []generate.FailureResult{
					{
						Result: generate.Result{
							StackPath: "/stacks/stack-1",
						},
						Error: errors.E(generate.ErrInvalidFilePath),
					},
					{
						Result: generate.Result{
							StackPath: "/stacks/stack-2",
						},
						Error: errors.E(generate.ErrInvalidFilePath),
					},
					{
						Result: generate.Result{
							StackPath: "/stacks/stack-3",
						},
						Error: errors.E(generate.ErrInvalidFilePath),
					},
					{
						Result: generate.Result{
							StackPath: "/stacks/stack-4",
						},
						Error: errors.E(generate.ErrInvalidFilePath),
					},
				},
			},
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			s := sandbox.New(t)
			s.BuildTree(tcase.layout)

			for _, cfg := range tcase.configs {
				cfg.append(t, s.RootDir())
			}

			assertGeneratedHCLs := func(t *testing.T) {
				t.Helper()

				for _, wantDesc := range tcase.wantHCL {
					stackRelPath := wantDesc.stack[1:]
					stack := s.StackEntry(stackRelPath)

					for name, wantHCL := range wantDesc.hcls {
						want := wantHCL.String()
						got := stack.ReadGeneratedHCL(name)

						assertHCLEquals(t, got, want)
					}
				}
			}

			workingDir := filepath.Join(s.RootDir(), tcase.workingDir)
			report := generate.Do(s.StackLoader(), s.RootDir(), workingDir)
			assertEqualReports(t, report, tcase.wantReport)

			assertGeneratedHCLs(t)

			// piggyback on the tests to validate that regeneration doesnt
			// delete files or fail and has identical results.
			t.Run("regenerate", func(t *testing.T) {
				report := generate.Do(s.StackLoader(), s.RootDir(), workingDir)
				// since we just generated everything, report should only contain
				// the same failures as previous code generation.
				assertEqualReports(t, report, generate.Report{
					Failures: tcase.wantReport.Failures,
				})
				assertGeneratedHCLs(t)
			})

			// Check we don't have extraneous/unwanted files
			// We remove wanted/expected generated code
			// So we should have only basic terramate configs left
			// There is potential to extract this for other code generation tests.
			for _, wantDesc := range tcase.wantHCL {
				stackRelPath := wantDesc.stack[1:]
				stack := s.StackEntry(stackRelPath)
				for name := range wantDesc.hcls {
					stack.RemoveGeneratedHCL(name)
				}
			}
			err := filepath.WalkDir(s.RootDir(), func(path string, d fs.DirEntry, err error) error {
				t.Helper()

				assert.NoError(t, err, "checking for unwanted generated files")
				if d.IsDir() {
					if d.Name() == ".git" {
						return filepath.SkipDir
					}
					return nil
				}

				// sandbox create README.md inside test dirs
				if d.Name() == config.DefaultFilename || d.Name() == "README.md" {
					return nil
				}

				t.Errorf("unwanted file %q", path)
				return nil
			})

			assert.NoError(t, err)
		})
	}
}

func TestWontOverwriteManuallyDefinedTerraform(t *testing.T) {
	const (
		genFilename  = "test.tf"
		manualTfCode = "some manual stuff, doesn't matter"
	)

	generateHCLConfig := generateHCL(
		labels(genFilename),
		content(
			terraform(
				str("required_version", "1.11"),
			),
		),
	)

	s := sandbox.New(t)
	s.BuildTree([]string{
		fmt.Sprintf("f:%s:%s", config.DefaultFilename, generateHCLConfig.String()),
		"s:stack",
		fmt.Sprintf("f:stack/%s:%s", genFilename, manualTfCode),
	})

	report := generate.Do(s.StackLoader(), s.RootDir(), s.RootDir())
	assert.EqualInts(t, 0, len(report.Successes), "want no success")
	assert.EqualInts(t, 1, len(report.Failures), "want single failure")
	assertReportHasError(t, report, errors.E(generate.ErrManualCodeExists))

	stack := s.StackEntry("stack")
	actualTfCode := stack.ReadGeneratedHCL(genFilename)
	assert.EqualStrings(t, manualTfCode, actualTfCode, "tf code altered by generate")
}

func TestGenerateHCLOverwriting(t *testing.T) {
	const genFilename = "test.tf"

	firstConfig := generateHCL(
		labels(genFilename),
		content(
			terraform(
				str("required_version", "1.11"),
			),
		),
	)
	firstWant := terraform(
		str("required_version", "1.11"),
	)

	s := sandbox.New(t)
	stack := s.CreateStack("stack")
	rootEntry := s.DirEntry(".")
	rootConfig := rootEntry.CreateConfig(firstConfig.String())

	report := s.Generate()
	assertEqualReports(t, report, generate.Report{
		Successes: []generate.Result{
			{
				StackPath: "/stack",
				Created:   []string{genFilename},
			},
		},
	})

	got := stack.ReadGeneratedHCL(genFilename)
	assertHCLEquals(t, got, firstWant.String())

	secondConfig := generateHCL(
		labels(genFilename),
		content(
			terraform(
				str("required_version", "2.0"),
			),
		),
	)
	secondWant := terraform(
		str("required_version", "2.0"),
	)

	rootConfig.Write(secondConfig.String())

	report = s.Generate()
	assertEqualReports(t, report, generate.Report{
		Successes: []generate.Result{
			{
				StackPath: "/stack",
				Changed:   []string{genFilename},
			},
		},
	})

	got = stack.ReadGeneratedHCL(genFilename)
	assertHCLEquals(t, got, secondWant.String())
	assertEqualReports(t, s.Generate(), generate.Report{})
}

func TestGeneratedHCLHeaders(t *testing.T) {
	const (
		rootFilename        = "root.tf"
		stackFilename       = "stack.tf"
		traceHeaderTemplate = "TERRAMATE: originated from generate_hcl block on %s"
	)

	s := sandbox.New(t)
	stackEntry := s.CreateStack("stack")
	rootEntry := s.DirEntry(".")

	rootEntry.CreateConfig(
		generateHCL(
			labels(rootFilename),
			content(
				block("root",
					str("attr", "root"),
				),
			),
		).String(),
	)

	stackEntry.CreateConfig(
		hcldoc(
			stack(),
			generateHCL(
				labels(stackFilename),
				content(
					block("stack",
						str("attr", "stack"),
					),
				),
			),
		).String(),
	)

	s.Generate()

	stackGen := stackEntry.ReadGeneratedHCL(stackFilename)
	stackHeader := fmt.Sprintf(traceHeaderTemplate, filepath.Join("/stack", config.DefaultFilename))
	if !strings.Contains(stackGen, stackHeader) {
		t.Errorf("wanted header %q\n\ngenerated file:\n%s\n", stackHeader, stackGen)
	}

	rootGen := stackEntry.ReadGeneratedHCL(rootFilename)
	rootHeader := fmt.Sprintf(traceHeaderTemplate, "/"+config.DefaultFilename)
	if !strings.Contains(rootGen, rootHeader) {
		t.Errorf("wanted header %q\n\ngenerated file:\n%s\n", rootHeader, rootGen)
	}
}

func TestGenerateHCLCleanupOldFiles(t *testing.T) {
	s := sandbox.New(t)
	stackEntry := s.CreateStack("stack")
	rootEntry := s.DirEntry(".")
	rootConfig := rootEntry.CreateConfig(
		hcldoc(
			generateHCL(
				labels("file1.tf"),
				content(
					block("block1",
						boolean("whatever", true),
					),
				),
			),
			generateHCL(
				labels("file2.tf"),
				content(
					block("block2",
						boolean("whatever", true),
					),
				),
			),
		).String(),
	)

	report := s.Generate()
	assertEqualReports(t, report, generate.Report{
		Successes: []generate.Result{
			{
				StackPath: "/stack",
				Created:   []string{"file1.tf", "file2.tf"},
			},
		},
	})

	got := stackEntry.ListGenFiles()
	assertEqualStringList(t, got, []string{"file1.tf", "file2.tf"})

	// Lets change one of the files, but delete the other
	rootConfig.Write(
		hcldoc(
			generateHCL(
				labels("file1.tf"),
				content(
					block("changed",
						boolean("newstuff", true),
					),
				),
			),
		).String(),
	)

	report = s.Generate()
	assertEqualReports(t, report, generate.Report{
		Successes: []generate.Result{
			{
				StackPath: "/stack",
				Changed:   []string{"file1.tf"},
				Deleted:   []string{"file2.tf"},
			},
		},
	})

	got = stackEntry.ListGenFiles()
	assertEqualStringList(t, got, []string{"file1.tf"})

	// Empty block generates no code, so it gets deleted
	rootConfig.Write(
		hcldoc(
			generateHCL(
				labels("file1.tf"),
				content(),
			),
		).String(),
	)

	report = s.Generate()
	assertEqualReports(t, report, generate.Report{
		Successes: []generate.Result{
			{
				StackPath: "/stack",
				Deleted:   []string{"file1.tf"},
			},
		},
	})

	got = stackEntry.ListGenFiles()
	assertEqualStringList(t, got, []string{})

	assertEqualReports(t, s.Generate(), generate.Report{})
}
