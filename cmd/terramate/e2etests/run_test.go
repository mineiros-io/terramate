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

package e2etest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mineiros-io/terramate/cmd/terramate/cli"
	"github.com/mineiros-io/terramate/run/dag"
	"github.com/mineiros-io/terramate/test"
	"github.com/mineiros-io/terramate/test/hclwrite"
	"github.com/mineiros-io/terramate/test/sandbox"
)

func TestCLIRunOrder(t *testing.T) {
	type testcase struct {
		name       string
		layout     []string
		workingDir string
		want       runExpected
	}

	for _, tc := range []testcase{
		{
			name: "one stack",
			layout: []string{
				"s:stack-a",
			},
			want: runExpected{
				Stdout: listStacks("stack-a"),
			},
		},
		{
			name: "empty ordering",
			layout: []string{
				"s:stack:after=[]",
			},
			want: runExpected{
				Stdout: listStacks(`stack`),
			},
		},
		{
			name: "after non-existent path",
			layout: []string{
				fmt.Sprintf("s:stack:after=[%q]", test.NonExistingDir(t)),
			},
			want: runExpected{
				Stdout: listStacks(`stack`),
			},
		},
		{
			name: "after regular file",
			layout: []string{
				fmt.Sprintf("s:stack:after=[%q]", test.WriteFile(t, "", "test.txt", `bleh`)),
			},
			want: runExpected{
				Stdout: listStacks(`stack`),
			},
		},
		{
			name: "independent stacks, consistent ordering (lexicographic)",
			layout: []string{
				"s:batatinha",
				"s:frita",
				"s:1",
				"s:2",
				"s:3",
				"s:boom",
			},
			want: runExpected{
				Stdout: listStacks("1", "2", "3", "batatinha", "boom", "frita"),
			},
		},
		{
			name: "independent stacks inside other stacks gives consistent ordering (lexicographic by path)",
			layout: []string{
				"s:stacks",
				"s:stacks/A",
				"s:stacks/B",
				"s:stacks/A/AA",
				"s:stacks/B/BA",
				"s:stacks/A/AA/AAA",
			},
			want: runExpected{
				Stdout: listStacks(
					"stacks",
					"A",
					"AA",
					"AAA",
					"B",
					"BA",
				),
			},
		},
		{
			name: "stack-b after stack-a (relpaths)",
			layout: []string{
				"s:stack-a",
				`s:stack-b:after=["../stack-a"]`,
			},
			want: runExpected{
				Stdout: listStacks("stack-a", "stack-b"),
			},
		},
		{
			name: "stack-b after stack-a (abspaths)",
			layout: []string{
				"s:stack-a",
				`s:stack-b:after=["/stack-a"]`,
			},
			want: runExpected{
				Stdout: listStacks("stack-a", "stack-b"),
			},
		},
		{
			name: "stack-c after stack-b after stack-a (relpaths)",
			layout: []string{
				"s:stack-a",
				`s:stack-b:after=["../stack-a"]`,
				`s:stack-c:after=["../stack-b"]`,
			},
			want: runExpected{
				Stdout: listStacks("stack-a", "stack-b", "stack-c"),
			},
		},
		{
			name: "stack-c after stack-b after stack-a (abspaths)",
			layout: []string{
				"s:stack-a",
				`s:stack-b:after=["/stack-a"]`,
				`s:stack-c:after=["/stack-b"]`,
			},
			want: runExpected{
				Stdout: listStacks("stack-a", "stack-b", "stack-c"),
			},
		},
		{
			name: "stack-a after stack-b after stack-c (relpaths)",
			layout: []string{
				"s:stack-c",
				`s:stack-b:after=["../stack-c"]`,
				`s:stack-a:after=["../stack-b"]`,
			},
			want: runExpected{
				Stdout: listStacks("stack-c", "stack-b", "stack-a"),
			},
		},
		{
			name: "stack-a after stack-b after stack-c (abspaths)",
			layout: []string{
				"s:stack-c",
				`s:stack-b:after=["/stack-c"]`,
				`s:stack-a:after=["/stack-b"]`,
			},
			want: runExpected{
				Stdout: listStacks("stack-c", "stack-b", "stack-a"),
			},
		},
		{
			name: "stack-a after stack-b (relpaths)",
			layout: []string{
				`s:stack-a:after=["../stack-b"]`,
				`s:stack-b`,
			},
			want: runExpected{
				Stdout: listStacks("stack-b", "stack-a"),
			},
		},
		{
			name: "stack-a after (stack-b, stack-c, stack-d)",
			layout: []string{
				`s:stack-a:after=["../stack-b", "../stack-c", "../stack-d"]`,
				`s:stack-b`,
				`s:stack-c`,
				`s:stack-d`,
			},
			want: runExpected{
				Stdout: listStacks("stack-b", "stack-c", "stack-d", "stack-a"),
			},
		},
		{
			name: "stack-a after (stack-b, stack-c, stack-d) (abspaths)",
			layout: []string{
				`s:stack-a:after=["/stack-b", "/stack-c", "/stack-d"]`,
				`s:stack-b`,
				`s:stack-c`,
				`s:stack-d`,
			},
			want: runExpected{
				Stdout: listStacks(
					"stack-b",
					"stack-c",
					"stack-d",
					"stack-a",
				),
			},
		},
		{
			name: "stack-c after stack-b after stack-a, stack-d after stack-z (relpaths)",
			layout: []string{
				`s:stack-c:after=["../stack-b"]`,
				`s:stack-b:after=["../stack-a"]`,
				`s:stack-a`,
				`s:stack-d:after=["../stack-z"]`,
				`s:stack-z`,
			},
			want: runExpected{
				Stdout: listStacks(
					"stack-a",
					"stack-b",
					"stack-c",
					"stack-z",
					"stack-d",
				),
			},
		},
		{
			name: "stack-c after stack-b after stack-a, stack-d after stack-z (abspaths)",
			layout: []string{
				`s:stack-c:after=["/stack-b"]`,
				`s:stack-b:after=["/stack-a"]`,
				`s:stack-a`,
				`s:stack-d:after=["/stack-z"]`,
				`s:stack-z`,
			},
			want: runExpected{
				Stdout: listStacks(
					"stack-a",
					"stack-b",
					"stack-c",
					"stack-z",
					"stack-d",
				),
			},
		},
		{
			name: "stack-c after stack-b after stack-a, stack-d after stack-b",
			layout: []string{
				`s:stack-c:after=["../stack-b"]`,
				`s:stack-b:after=["../stack-a"]`,
				`s:stack-a`,
				`s:stack-d:after=["../stack-b"]`,
			},
			want: runExpected{
				Stdout: listStacks(
					"stack-a",
					"stack-b",
					"stack-c",
					"stack-d",
				),
			},
		},
		{
			name: "stack-c after stack-b after stack-a, stack-z after stack-d after stack-b",
			layout: []string{
				`s:stack-c:after=["../stack-b"]`,
				`s:stack-b:after=["../stack-a"]`,
				`s:stack-a`,
				`s:stack-z:after=["../stack-d"]`,
				`s:stack-d:after=["../stack-b"]`,
			},
			want: runExpected{
				Stdout: listStacks(
					"stack-a",
					"stack-b",
					"stack-c",
					"stack-d",
					"stack-z",
				),
			},
		},
		{
			name: "stack-g after stack-c after stack-b after stack-a, stack-z after stack-d after stack-b",
			layout: []string{
				`s:stack-g:after=["../stack-c"]`,
				`s:stack-c:after=["../stack-b"]`,
				`s:stack-b:after=["../stack-a"]`,
				`s:stack-a`,
				`s:stack-z:after=["../stack-d"]`,
				`s:stack-d:after=["../stack-b"]`,
			},
			want: runExpected{
				Stdout: listStacks(
					"stack-a",
					"stack-b",
					"stack-c",
					"stack-d",
					"stack-g",
					"stack-z",
				),
			},
		},
		{
			name: "stack-a after (stack-b, stack-c), stack-b after (stack-d, stack-f), stack-c after (stack-g, stack-h)",
			layout: []string{
				`s:stack-a:after=["../stack-b", "../stack-c"]`,
				`s:stack-b:after=["../stack-d", "../stack-f"]`,
				`s:stack-c:after=["../stack-g", "../stack-h"]`,
				`s:stack-d`,
				`s:stack-f`,
				`s:stack-g`,
				`s:stack-h`,
			},
			want: runExpected{
				Stdout: listStacks(
					"stack-d",
					"stack-f",
					"stack-b",
					"stack-g",
					"stack-h",
					"stack-c",
					"stack-a",
				),
			},
		},
		{
			name: "stack-z after (stack-a, stack-b, stack-c, stack-d), stack-a after (stack-b, stack-c)",
			layout: []string{
				`s:stack-z:after=["../stack-a", "../stack-b", "../stack-c", "../stack-d"]`,
				`s:stack-a:after=["../stack-b", "../stack-c"]`,
				`s:stack-b`,
				`s:stack-c`,
				`s:stack-d`,
			},
			want: runExpected{
				Stdout: listStacks(
					"stack-b",
					"stack-c",
					"stack-a",
					"stack-d",
					"stack-z",
				),
			},
		},
		{
			name: "stack-z after (stack-a, stack-b, stack-c, stack-d), stack-a after (stack-x, stack-y)",
			layout: []string{
				`s:stack-z:after=["../stack-a", "../stack-b", "../stack-c", "../stack-d"]`,
				`s:stack-a:after=["../stack-x", "../stack-y"]`,
				`s:stack-b`,
				`s:stack-c`,
				`s:stack-d`,
				`s:stack-x`,
				`s:stack-y`,
			},
			want: runExpected{
				Stdout: listStacks(
					"stack-x",
					"stack-y",
					"stack-a",
					"stack-b",
					"stack-c",
					"stack-d",
					"stack-z",
				),
			},
		},
		{
			name: "stack-a after stack-a - fails",
			layout: []string{
				`s:stack-a:after=["../stack-a"]`,
			},
			want: runExpected{
				Status:      defaultErrExitStatus,
				StderrRegex: string(dag.ErrCycleDetected),
			},
		},
		{
			name: "stack-a after . - fails",
			layout: []string{
				`s:stack-a:after=["."]`,
			},
			want: runExpected{
				Status:      defaultErrExitStatus,
				StderrRegex: string(dag.ErrCycleDetected),
			},
		},
		{
			name: "stack-a after stack-b after stack-c after stack-a - fails",
			layout: []string{
				`s:stack-a:after=["../stack-b"]`,
				`s:stack-b:after=["../stack-c"]`,
				`s:stack-c:after=["../stack-a"]`,
			},
			want: runExpected{
				Status:      defaultErrExitStatus,
				StderrRegex: string(dag.ErrCycleDetected),
			},
		},
		{
			name: "1 after 4 after 20 after 1 - fails",
			layout: []string{
				`s:1:after=["../2", "../3", "../4", "../5", "../6", "../7"]`,
				`s:2:after=["../12", "../13", "../14", "../15", "../16"]`,
				`s:3:after=["../2", "../4"]`,
				`s:4:after=["../6", "../20"]`,
				`s:5`,
				`s:6`,
				`s:7`,
				`s:8`,
				`s:9`,
				`s:10`,
				`s:11`,
				`s:12`,
				`s:13`,
				`s:14`,
				`s:15`,
				`s:16`,
				`s:17`,
				`s:18`,
				`s:19`,
				`s:20:after=["../10", "../1"]`,
			},
			want: runExpected{
				Status:      defaultErrExitStatus,
				StderrRegex: string(dag.ErrCycleDetected),
			},
		},
		{
			name: `stack-z after (stack-b, stack-c, stack-d)
				   stack-a after stack-c
				   stack-b before stack-a`,
			layout: []string{
				`s:stack-z:after=["../stack-b", "../stack-c", "../stack-d"]`,
				`s:stack-a:after=["../stack-c"]`,
				`s:stack-b:before=["../stack-a"]`,
				`s:stack-c`,
				`s:stack-d`,
			},
			want: runExpected{
				Stdout: listStacks(
					"stack-b",
					"stack-c",
					"stack-a",
					"stack-d",
					"stack-z",
				),
			},
		},
		{
			name: `run order selects only stacks inside working dir`,
			layout: []string{
				`s:stacks/stack-a:after=["/stacks/stack-b", "/parent-stack"]`,
				`s:stacks/stack-b:before=["/parent-stack"]`,
				`s:parent-stack`,
			},
			workingDir: "stacks",
			want: runExpected{
				Stdout: listStacks("stack-b", "stack-a"),
			},
		},
		{
			name: "stack-b after stack-a after parent (implicit)",
			layout: []string{
				`s:parent`,
				`s:parent/stack-a`,
				`s:parent/stack-b:after=["/parent/stack-a"]`,
			},
			want: runExpected{
				Stdout: listStacks("parent", "stack-a", "stack-b"),
			},
		},
		{
			name: "grand parent before parent before child (implicit)",
			layout: []string{
				`s:grand-parent`,
				`s:grand-parent/parent`,
				`s:grand-parent/parent/child`,
			},
			want: runExpected{
				Stdout: listStacks("grand-parent", "parent", "child"),
			},
		},
		{
			name: "child stack CANNOT have explicit after clause to parent",
			layout: []string{
				`s:stacks`,
				`s:stacks/child:after=["/stacks"]`,
			},
			want: runExpected{
				Status:      defaultErrExitStatus,
				StderrRegex: string(dag.ErrCycleDetected),
			},
		},
		{
			name: "child stack can never run before the parent - cycle",
			layout: []string{
				`s:stacks`,
				`s:stacks/child:before=["/stacks"]`,
			},
			want: runExpected{
				Status:      defaultErrExitStatus,
				StderrRegex: string(dag.ErrCycleDetected),
			},
		},
		{
			name: "parent stack can never run after the child - cycle",
			layout: []string{
				`s:stacks:after=["/stacks/child"]`,
				`s:stacks/child`,
			},
			want: runExpected{
				Status:      defaultErrExitStatus,
				StderrRegex: string(dag.ErrCycleDetected),
			},
		},
		{
			name: "after directory containing stacks",
			layout: []string{
				`d:dir`,
				`s:dir/s1`,
				`s:dir/s2`,
				`s:dir/s3`,
				`s:stack:after=["/dir"]`,
			},
			want: runExpected{
				Stdout: listStacks("s1", "s2", "s3", "stack"),
			},
		},
		{
			name: "after stack containing sub-stacks",
			layout: []string{
				`s:parent`,
				`s:parent/s1`,
				`s:parent/s2`,
				`s:parent/s3`,
				`s:stack:after=["/parent"]`,
			},
			want: runExpected{
				Stdout: listStacks("parent", "s1", "s2", "s3", "stack"),
			},
		},
		{
			name: "after sub-stack of parent",
			layout: []string{
				`s:parent`,
				`s:parent/s1`,
				`s:parent/s2`,
				`s:parent/s3`,
				`s:stack:after=["/parent/s2"]`,
			},
			want: runExpected{
				Stdout: listStacks("parent", "s1", "s2", "s3", "stack"),
			},
		},
		{
			name: "before directory containing stacks",
			layout: []string{
				`d:dir`,
				`s:dir/s1`,
				`s:dir/s2`,
				`s:dir/s3`,
				`s:stack:before=["/dir"]`,
			},
			want: runExpected{
				Stdout: listStacks("stack", "s1", "s2", "s3"),
			},
		},
		{
			name: "after directory containing stacks in deep directories",
			layout: []string{
				`d:dir`,
				`s:dir/A/B/C/D/Z-stack`,
				`s:A-stack:after=["/dir"]`,
			},
			want: runExpected{
				Stdout: listStacks("Z-stack", "A-stack"),
			},
		},
		{
			name: "before directory containing no stacks does nothing",
			layout: []string{
				`d:dir`,
				`d:dir/dir2`,
				`s:stack:before=["/dir"]`,
				`s:stack2`,
			},
			want: runExpected{
				Stdout: listStacks("stack", "stack2"),
			},
		},
		{
			name: "after directory containing no stacks does nothing",
			layout: []string{
				`d:dir`,
				`d:dir/dir2`,
				`s:stack:after=["/dir"]`,
				`s:stack2`,
			},
			want: runExpected{
				Stdout: listStacks("stack", "stack2"),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sandboxes := []sandbox.S{
				sandbox.New(t),
				sandbox.NoGit(t),
			}

			for _, s := range sandboxes {
				s.BuildTree(tc.layout)
				test.WriteRootConfig(t, s.RootDir())

				wd := s.RootDir()
				if tc.workingDir != "" {
					wd = filepath.Join(wd, tc.workingDir)
				}

				cli := newCLI(t, wd)
				assertRunResult(t, cli.stacksRunOrder(), tc.want)
			}
		})
	}
}

func TestRunWants(t *testing.T) {
	type testcase struct {
		name   string
		layout []string
		wd     string
		want   runExpected
	}

	for _, tc := range []testcase{
		{
			/* this works but gives a warning */
			name: "stack-a wants stack-a",
			layout: []string{
				`s:stack-a:wants=["/stack-a"]`,
			},
			want: runExpected{
				Stdout: listStacks("stack-a"),
			},
		},
		{
			name: "stack-a wants stack-b",
			layout: []string{
				`s:stack-a:wants=["/stack-b"]`,
				`s:stack-b`,
			},
			want: runExpected{
				Stdout: listStacks("stack-a", "stack-b"),
			},
		},
		{
			name: "stack-b wants stack-a (same ordering)",
			layout: []string{
				`s:stack-b:wants=["/stack-a"]`,
				`s:stack-a`,
			},
			want: runExpected{
				Stdout: listStacks("stack-a", "stack-b"),
			},
		},
		{
			name: "stack-a wants stack-b (from inside stack-a)",
			layout: []string{
				`s:stack-a:wants=["/stack-b"]`,
				`s:stack-b`,
			},
			wd: "/stack-a",
			want: runExpected{
				Stdout: listStacks("stack-a", "stack-b"),
			},
		},
		{
			name: "stack-a wants stack-b (from inside stack-b)",
			layout: []string{
				`s:stack-a:wants=["/stack-b"]`,
				`s:stack-b`,
			},
			wd: "/stack-b",
			want: runExpected{
				Stdout: listStacks("stack-b"),
			},
		},
		{
			name: "stack-b wants stack-a (same ordering) (from inside stack-b)",
			layout: []string{
				`s:stack-b:wants=["/stack-a"]`,
				`s:stack-a`,
			},
			wd: "/stack-b",
			want: runExpected{
				Stdout: listStacks("stack-a", "stack-b"),
			},
		},
		{
			name: "stack-b wants (stack-a, stack-c) (from inside stack-b)",
			layout: []string{
				`s:stack-b:wants=["/stack-a", "/stack-c"]`,
				`s:stack-a`,
				`s:stack-c`,
			},
			wd: "/stack-b",
			want: runExpected{
				Stdout: listStacks("stack-a", "stack-b", "stack-c"),
			},
		},
		{
			name: "stack-b wants (stack-a, stack-c), stack-c wants stack-a (from inside stack-b)",
			layout: []string{
				`s:stack-b:wants=["/stack-a", "/stack-c"]`,
				`s:stack-a`,
				`s:stack-c:wants=["/stack-a"]`,
			},
			wd: "/stack-b",
			want: runExpected{
				Stdout: listStacks("stack-a", "stack-b", "stack-c"),
			},
		},
		{
			name: `stack-a wants (stack-b, stack-c) and stack-b wants (stack-d, stack-e)
					(from inside stack-a) - recursive`,
			layout: []string{
				`s:stack-a:wants=["/stack-b", "/stack-c"]`,
				`s:stack-b:wants=["/stack-d", "/stack-e"]`,
				`s:stack-c`,
				`s:stack-d`,
				`s:stack-e`,
			},
			wd: "/stack-a",
			want: runExpected{
				Stdout: listStacks(
					"stack-a",
					"stack-b",
					"stack-c",
					"stack-d",
					"stack-e",
				),
			},
		},
		{
			name: `stack-a wants (stack-b, stack-c) and stack-b wants (stack-d, stack-e)
					(from inside stack-b) - not recursive`,
			layout: []string{
				`s:stack-a:wants=["/stack-b", "/stack-c"]`,
				`s:stack-b:wants=["/stack-d", "/stack-e"]`,
				`s:stack-c`,
				`s:stack-d`,
				`s:stack-e`,
			},
			wd: "/stack-b",
			want: runExpected{
				Stdout: listStacks(
					"stack-b",
					"stack-d",
					"stack-e",
				),
			},
		},
		{
			name: `	stack-a wants (stack-b, stack-c)
					stack-b wants (stack-d, stack-e)
					stack-e wants (stack-a, stack-z)
					(from inside stack-b) - recursive, *circular*
					must pull all stacks`,
			layout: []string{
				`s:stack-a:wants=["/stack-b", "/stack-c"]`,
				`s:stack-b:wants=["/stack-d", "/stack-e"]`,
				`s:stack-c`,
				`s:stack-d`,
				`s:stack-e:wants=["/stack-a", "/stack-z"]`,
				`s:stack-z`,
			},
			wd: "/stack-b",
			want: runExpected{
				Stdout: listStacks(
					"stack-a",
					"stack-b",
					"stack-c",
					"stack-d",
					"stack-e",
					"stack-z",
				),
			},
		},
		{
			name: `wants+order - stack-a after stack-b / stack-d before stack-a
	* stack-a wants (stack-b, stack-c)
	* stack-b wants (stack-d, stack-e)
	* stack-e wants (stack-a, stack-z) (from inside stack-b) - recursive, *circular*`,
			layout: []string{
				`s:stack-a:wants=["/stack-b", "/stack-c"];after=["/stack-b"]`,
				`s:stack-b:wants=["/stack-d", "/stack-e"]`,
				`s:stack-c`,
				`s:stack-d:before=["/stack-a"]`,
				`s:stack-e:wants=["/stack-a", "/stack-z"]`,
				`s:stack-z`,
			},
			wd: "/stack-b",
			want: runExpected{
				Stdout: listStacks(
					"stack-b",
					"stack-d",
					"stack-a",
					"stack-c",
					"stack-e",
					"stack-z",
				),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sandboxes := []sandbox.S{
				sandbox.New(t),
				sandbox.NoGit(t),
			}

			for _, s := range sandboxes {
				s.BuildTree(tc.layout)
				test.WriteRootConfig(t, s.RootDir())

				cli := newCLI(t, filepath.Join(s.RootDir(), tc.wd))
				assertRunResult(t, cli.stacksRunOrder(), tc.want)

				if s.IsGit() {
					// required because `terramate run` requires a clean repo.
					git := s.Git()
					git.CommitAll("everything")
				}

				// TODO(i4k): not portable
				assertRunResult(t, cli.run("run", "sh", "-c", "pwd | xargs basename"), tc.want)
			}
		})
	}
}

func TestRunOrderNotChangedStackIgnored(t *testing.T) {
	const (
		mainTfFileName = "main.tf"
		mainTfContents = "# change is the eternal truth of the universe"
	)

	s := sandbox.New(t)

	// stack must run after stack2 but stack2 didn't change.
	s.BuildTree([]string{
		`s:stack:after=["/stack2"]`,
		"s:stack2",
	})

	stack := s.DirEntry("stack")
	stackMainTf := stack.CreateFile(mainTfFileName, "# some code")

	git := s.Git()
	git.CommitAll("first commit")
	git.Push("main")
	git.CheckoutNew("change-stack")

	stackMainTf.Write(mainTfContents)
	git.CommitAll("stack changed")

	cli := newCLI(t, s.RootDir())

	wantList := stack.RelPath() + "\n"
	assertRunResult(t, cli.listChangedStacks(), runExpected{Stdout: wantList})

	cat := test.LookPath(t, "cat")
	wantRun := mainTfContents

	assertRunResult(t, cli.run(
		"run",
		"--changed",
		cat,
		mainTfFileName,
	), runExpected{Stdout: wantRun})

	cli = newCLI(t, stack.Path())
	assertRunResult(t, cli.run(
		"run",
		"--changed",
		cat,
		mainTfFileName,
	), runExpected{Stdout: wantRun})

	cli = newCLI(t, filepath.Join(s.RootDir(), "stack2"))
	assertRunResult(t, cli.run(
		"run",
		"--changed",
		cat,
		mainTfFileName,
	), runExpected{})
}

func TestRunReverseExecution(t *testing.T) {
	const testfile = "testfile"

	s := sandbox.New(t)
	cat := test.LookPath(t, "cat")
	cli := newCLI(t, s.RootDir())
	assertRunOrder := func(stacks ...string) {
		t.Helper()

		want := strings.Join(stacks, "\n")
		if want != "" {
			want += "\n"
		}

		assertRunResult(t, cli.run(
			"run",
			"--reverse",
			cat,
			testfile,
		), runExpected{Stdout: want})

		assertRunResult(t, cli.run(
			"run",
			"--reverse",
			"--changed",
			cat,
			testfile,
		), runExpected{Stdout: want})
	}
	addStack := func(stack string) {
		s.BuildTree([]string{
			"s:" + stack,
			fmt.Sprintf("f:%s/%s:%s\n", stack, testfile, stack),
		})
	}

	git := s.Git()
	git.CheckoutNew("changes")

	assertRunOrder()

	addStack("stack-1")
	git.CommitAll("commit")
	assertRunOrder("stack-1")

	addStack("stack-2")
	git.CommitAll("commit")
	assertRunOrder("stack-2", "stack-1")

	addStack("stack-3")
	git.CommitAll("commit")
	assertRunOrder("stack-3", "stack-2", "stack-1")
}

func TestRunIgnoresAfterBeforeStackRefsOutsideWorkingDir(t *testing.T) {
	const testfile = "testfile"

	s := sandbox.New(t)

	s.BuildTree([]string{
		"s:parent-stack",
		`s:stacks/stack-1:before=["/parent-stack"]`,
		`s:stacks/stack-2:after=["/parent-stack"]`,
		fmt.Sprintf("f:parent-stack/%s:parent-stack\n", testfile),
		fmt.Sprintf("f:stacks/stack-1/%s:stack-1\n", testfile),
		fmt.Sprintf("f:stacks/stack-2/%s:stack-2\n", testfile),
	})

	git := s.Git()
	git.CommitAll("first commit")

	cat := test.LookPath(t, "cat")
	assertRun := func(wd string, want string) {
		cli := newCLI(t, filepath.Join(s.RootDir(), wd))

		assertRunResult(t, cli.run(
			"run",
			cat,
			testfile,
		), runExpected{Stdout: want})

		assertRunResult(t, cli.run(
			"run",
			"--changed",
			cat,
			testfile,
		), runExpected{Stdout: want})
	}

	assertRun(".", listStacks("stack-1", "parent-stack", "stack-2"))
	assertRun("stacks", listStacks("stack-1", "stack-2"))
	assertRun("stacks/stack-1", listStacks("stack-1"))
	assertRun("stacks/stack-2", listStacks("stack-2"))
}

func TestRunOrderAllChangedStacksExecuted(t *testing.T) {
	const (
		mainTfFileName = "main.tf"
		mainTfContents = "# change is the eternal truth of the universe"
	)

	s := sandbox.New(t)

	// stack2 must run after stack and both changed.
	s.BuildTree([]string{
		`s:stack:after=["/stack2"]`,
		"s:stack2",
	})

	stack2 := s.DirEntry("stack2")
	stack2MainTf := stack2.CreateFile(mainTfFileName, "# some code")

	stack := s.DirEntry("stack")
	stackMainTf := stack.CreateFile(mainTfFileName, "# some code")

	git := s.Git()
	git.CommitAll("first commit")
	git.Push("main")
	git.CheckoutNew("change-stack")

	stackMainTf.Write(mainTfContents)
	stack2MainTf.Write(mainTfContents)
	git.CommitAll("stack changed")

	cli := newCLI(t, s.RootDir())

	wantList := stack.RelPath() + "\n" + stack2.RelPath() + "\n"
	assertRunResult(t, cli.listChangedStacks(), runExpected{Stdout: wantList})

	cat := test.LookPath(t, "cat")
	wantRun := fmt.Sprintf(
		"%s%s",
		mainTfContents,
		mainTfContents,
	)

	assertRunResult(t, cli.run(
		"run",
		"--changed",
		cat,
		mainTfFileName,
	), runExpected{Stdout: wantRun})
}

func TestRunFailIfGitSafeguardUntracked(t *testing.T) {
	const (
		mainTfFileName = "main.tf"
		mainTfContents = "# some code"
	)

	s := sandbox.New(t)

	stack := s.CreateStack("stack")
	stack.CreateFile(mainTfFileName, mainTfContents)

	git := s.Git()
	git.CommitAll("first commit")
	git.Push("main")
	git.CheckoutNew("change-stack")

	stack.CreateFile("untracked-file.txt", `# something`)

	cli := newCLI(t, s.RootDir())
	cat := test.LookPath(t, "cat")

	// check untracked with --changed
	assertRunResult(t, cli.run(
		"run",
		"--changed",
		cat,
		mainTfFileName,
	), runExpected{
		Status:      defaultErrExitStatus,
		StderrRegex: "repository has untracked files",
	})

	// check untracked *without* --changed
	assertRunResult(t, cli.run(
		"run",
		cat,
		mainTfFileName,
	), runExpected{
		Status:      defaultErrExitStatus,
		StderrRegex: "repository has untracked files",
	})

	// disabling the check must work for both with and without --changed

	t.Run("disable check using cmd args", func(t *testing.T) {
		assertRun(t, cli.run(
			"run",
			"--changed",
			"--disable-check-git-untracked",
			cat,
			mainTfFileName,
		))

		assertRunResult(t, cli.run(
			"run",
			"--disable-check-git-untracked",
			cat,
			mainTfFileName,
		), runExpected{
			Stdout: mainTfContents,
		})
	})

	t.Run("disable check using env vars", func(t *testing.T) {
		cli := newCLI(t, s.RootDir())
		cli.env = append([]string{
			"TM_DISABLE_CHECK_GIT_UNTRACKED=true",
		}, os.Environ()...)
		assertRun(t, cli.run(
			"run",
			"--changed",
			cat,
			mainTfFileName,
		))

		assertRunResult(t, cli.run(
			"run",
			cat,
			mainTfFileName,
		), runExpected{
			Stdout: mainTfContents,
		})
	})

	t.Run("disable check using hcl config", func(t *testing.T) {
		const rootConfig = "terramate.tm.hcl"

		s.RootEntry().CreateFile(rootConfig, `
			terramate {
			  config {
			    git {
			      check_untracked = false
			    }
			  }
			}
		`)
		defer s.RootEntry().RemoveFile(rootConfig)

		assertRun(t, cli.run(
			"run",
			"--changed",
			cat,
			mainTfFileName,
		))

		assertRunResult(t, cli.run(
			"run",
			cat,
			mainTfFileName,
		), runExpected{
			Stdout: mainTfContents,
		})
	})
}

func TestRunFailIfGeneratedCodeIsOutdated(t *testing.T) {
	const generateFile = "generate.tm.hcl"

	s := sandbox.New(t)
	stack := s.CreateStack("stack")

	// So we can list the stack as changed
	stack.CreateFile(generateFile, "")
	git := s.Git()
	git.CommitAll("first commit")
	git.Push("main")
	git.CheckoutNew("generate-code")

	generateFileBody := generateHCL(
		labels("test.tf"),
		content(
			str("test", "test"),
		),
	).String()
	stack.CreateFile(generateFile, generateFileBody)

	git.CommitAll("generating some code commit")

	tmcli := newCLI(t, s.RootDir())
	cat := test.LookPath(t, "cat")

	// check with --changed
	assertRunResult(t, tmcli.run(
		"run",
		"--changed",
		cat,
		generateFile,
	), runExpected{
		Status:      defaultErrExitStatus,
		StderrRegex: string(cli.ErrOutdatedGenCodeDetected),
	})

	// check without --changed
	assertRunResult(t, tmcli.run(
		"run",
		cat,
		generateFile,
	), runExpected{
		Status:      defaultErrExitStatus,
		StderrRegex: string(cli.ErrOutdatedGenCodeDetected),
	})

	// disabling the check must work for both with and without --changed

	t.Run("disable check using cmd args", func(t *testing.T) {
		assertRunResult(t, tmcli.run(
			"run",
			"--changed",
			"--disable-check-gen-code",
			cat,
			generateFile,
		), runExpected{
			Stdout: generateFileBody,
		})

		assertRunResult(t, tmcli.run(
			"run",
			"--disable-check-gen-code",
			cat,
			generateFile,
		), runExpected{
			Stdout: generateFileBody,
		})
	})

	t.Run("disable check using env vars", func(t *testing.T) {
		tmcli := newCLI(t, s.RootDir())
		tmcli.env = append([]string{
			"TM_DISABLE_CHECK_GEN_CODE=true",
		}, os.Environ()...)

		assertRunResult(t, tmcli.run("run", "--changed", cat, generateFile), runExpected{
			Stdout: generateFileBody,
		})
		assertRunResult(t, tmcli.run("run", cat, generateFile), runExpected{
			Stdout: generateFileBody,
		})
	})

	t.Run("disable check using hcl config", func(t *testing.T) {
		const rootConfig = "terramate.tm.hcl"

		s.RootEntry().CreateFile(rootConfig, `
			terramate {
			  config {
			    run {
			      check_gen_code = false
			    }
			  }
			}
		`)

		git.Add(rootConfig)
		git.Commit("commit root config")

		assertRunResult(t, tmcli.run("run", "--changed", cat, generateFile), runExpected{
			Stdout: generateFileBody,
		})
		assertRunResult(t, tmcli.run("run", cat, generateFile), runExpected{
			Stdout: generateFileBody,
		})
	})

}

func TestRunFailIfGitSafeguardUncommitted(t *testing.T) {
	const (
		mainTfFileName        = "main.tf"
		mainTfInitialContents = "# some code"
		mainTfAlteredContents = "# other code"
	)

	s := sandbox.New(t)

	stack := s.CreateStack("stack")
	file := stack.CreateFile(mainTfFileName, mainTfInitialContents)

	git := s.Git()
	git.CommitAll("first commit")

	cli := newCLI(t, s.RootDir())
	cat := test.LookPath(t, "cat")

	// everything committed, repo is clean
	assertRunResult(t, cli.run(
		"run",
		cat,
		mainTfFileName,
	), runExpected{Stdout: mainTfInitialContents})

	assertRunResult(t, cli.run(
		"run",
		"--changed",
		cat,
		mainTfFileName,
	), runExpected{Stdout: mainTfInitialContents})

	// make it uncommitted
	file.Write(mainTfAlteredContents)

	assertRunResult(t, cli.run(
		"run",
		cat,
		mainTfFileName,
	), runExpected{
		Status:      defaultErrExitStatus,
		StderrRegex: "repository has uncommitted files",
	})

	assertRunResult(t, cli.run(
		"run",
		"--changed",
		cat,
		mainTfFileName,
	), runExpected{
		Status:      defaultErrExitStatus,
		StderrRegex: "repository has uncommitted files",
	})

	// --dry-run ignore safeguards
	assertRunResult(t, cli.run(
		"run",
		"--dry-run",
		cat,
		mainTfFileName,
	), runExpected{
		IgnoreStdout: true,
	})

	// disable uncommitted check

	t.Run("disable check using cmd args", func(t *testing.T) {
		assertRunResult(t, cli.run(
			"--disable-check-git-uncommitted",
			"run",
			cat,
			mainTfFileName,
		), runExpected{
			Stdout: mainTfAlteredContents,
		})

		assertRunResult(t, cli.run(
			"--disable-check-git-uncommitted",
			"--changed",
			"run",
			cat,
			mainTfFileName,
		), runExpected{
			Stdout: mainTfAlteredContents,
		})
	})

	t.Run("disable check using env vars", func(t *testing.T) {
		cli := newCLI(t, s.RootDir())
		cli.env = append([]string{
			"TM_DISABLE_CHECK_GIT_UNCOMMITTED=true",
		}, os.Environ()...)

		assertRunResult(t, cli.run("run", cat, mainTfFileName), runExpected{
			Stdout: mainTfAlteredContents,
		})
		assertRunResult(t, cli.run("--changed", "run", cat, mainTfFileName), runExpected{
			Stdout: mainTfAlteredContents,
		})
	})

	t.Run("disable check using hcl config", func(t *testing.T) {
		const rootConfig = "terramate.tm.hcl"

		s.RootEntry().CreateFile(rootConfig, `
			terramate {
			  config {
			    git {
			      check_uncommitted = false
			    }
			  }
			}
		`)

		git.Add(rootConfig)
		git.Commit("commit root config")

		assertRunResult(t, cli.run("run", cat, mainTfFileName), runExpected{
			Stdout: mainTfAlteredContents,
		})
		assertRunResult(t, cli.run("--changed", "run", cat, mainTfFileName), runExpected{
			Stdout: mainTfAlteredContents,
		})
	})
}

func TestRunFailIfStackGeneratedCodeIsOutdated(t *testing.T) {
	const (
		testFilename   = "test.txt"
		contentsStack1 = "stack-1 file"
		contentsStack2 = "stack-2 file"
	)
	s := sandbox.New(t)

	stack1 := s.CreateStack("stacks/stack-1")
	stack2 := s.CreateStack("stacks/stack-2")

	stack1.CreateFile(testFilename, contentsStack1)
	stack2.CreateFile(testFilename, contentsStack2)

	git := s.Git()
	git.CommitAll("first commit")
	git.Push("main")

	tmcli := newCLI(t, s.RootDir())
	cat := test.LookPath(t, "cat")

	assertRunResult(t, tmcli.run("run", cat, testFilename), runExpected{
		Stdout: contentsStack1 + contentsStack2,
	})

	stack1.CreateConfig(`
		generate_hcl "test.tf" {
		  content {
		    test = terramate.path
		  }
		}
	`)

	git.CheckoutNew("adding-stack1-config")
	git.CommitAll("adding stack-1 config")

	assertRunResult(t, tmcli.run("run", cat, testFilename), runExpected{
		Status:      defaultErrExitStatus,
		StderrRegex: string(cli.ErrOutdatedGenCodeDetected),
	})

	assertRunResult(t, tmcli.run("run", "--changed", cat, testFilename), runExpected{
		Status:      defaultErrExitStatus,
		StderrRegex: string(cli.ErrOutdatedGenCodeDetected),
	})

	// Check that if inside cwd it should work
	// Ignoring the other stack that has outdated code
	tmcli = newCLI(t, stack2.Path())

	assertRunResult(t, tmcli.run("run", cat, testFilename), runExpected{
		Stdout: contentsStack2,
	})
}

func TestRunLogsUserCommand(t *testing.T) {
	s := sandbox.New(t)

	stack := s.CreateStack("stack")
	testfile := stack.CreateFile("test", "")

	git := s.Git()
	git.CommitAll("first commit")
	git.Push("main")

	cli := newCLIWithLogLevel(t, s.RootDir(), "info")
	assertRunResult(t, cli.run("run", "cat", testfile.HostPath()), runExpected{
		StderrRegex: `cmd="cat /`,
	})
}

func TestRunContinueOnError(t *testing.T) {
	s := sandbox.New(t)

	s.BuildTree([]string{
		`s:s1`,
		`s:s2`,
	})

	const expectedOutput = "# no code"

	s2 := s.StackEntry("s2")
	s2.CreateFile("main.tf", expectedOutput)

	git := s.Git()
	git.CommitAll("first commit")
	git.Push("main")

	cli := newCLI(t, s.RootDir())
	assertRunResult(t, cli.run("run", "cat", "main.tf"), runExpected{
		IgnoreStderr: true,
		Status:       1,
	})

	assertRunResult(t, cli.run("run", "--continue-on-error", "cat", "main.tf"), runExpected{
		IgnoreStderr: true,
		Stdout:       expectedOutput,
		Status:       1,
	})
}

func TestRunNoRecursive(t *testing.T) {
	s := sandbox.New(t)

	s.BuildTree([]string{
		`f:file.txt:root`,
		`s:parent`,
		`s:parent/child1`,
		`s:parent/child2`,
	})

	parent := s.StackEntry("parent")
	parent.CreateFile("file.txt", "parent")

	child1 := s.StackEntry("parent/child1")
	child1.CreateFile("file.txt", "child1")

	child2 := s.StackEntry("parent/child2")
	child2.CreateFile("file.txt", "child2")

	git := s.Git()
	git.CommitAll("first commit")
	git.Push("main")

	cli := newCLI(t, s.RootDir())
	assertRunResult(t, cli.run("run", "cat", "file.txt"), runExpected{
		Stdout: `parentchild1child2`,
	})

	cli = newCLI(t, parent.Path())
	assertRunResult(t, cli.run("run", "--no-recursive", "cat", "file.txt"),
		runExpected{
			Stdout: `parent`,
		},
	)

	cli = newCLI(t, child1.Path())
	assertRunResult(t, cli.run("run", "--no-recursive", "cat", "file.txt"),
		runExpected{
			Stdout: `child1`,
		},
	)

	cli = newCLI(t, s.RootDir())
	assertRunResult(t, cli.run("run", "--no-recursive", "cat", "file.txt"),
		runExpected{
			Status:       1,
			IgnoreStderr: true,
		},
	)
}

func TestRunDisableGitCheckRemote(t *testing.T) {
	s := sandbox.New(t)

	stack := s.CreateStack("stack")
	fileContents := "# whatever"
	someFile := stack.CreateFile("main.tf", fileContents)

	tmcli := newCLI(t, s.RootDir())

	git := s.Git()

	git.Add(".")
	git.Commit("all")

	setupLocalMainBranchBehindOriginMain(git, func() {
		stack.CreateFile("some-new-file", "testing")
	})

	cat := test.LookPath(t, "cat")

	t.Run("disable check using cmd args", func(t *testing.T) {
		assertRunResult(t, tmcli.run(
			"run",
			"--disable-check-git-remote",
			cat,
			someFile.HostPath(),
		), runExpected{Stdout: fileContents})
	})

	t.Run("disable check using env vars", func(t *testing.T) {
		ts := newCLI(t, s.RootDir())
		ts.env = append([]string{
			"TM_DISABLE_CHECK_GIT_REMOTE=true",
		}, os.Environ()...)

		assertRunResult(t, ts.run("run", cat, someFile.HostPath()), runExpected{
			Stdout: fileContents,
		})
	})

	t.Run("disable check using hcl config", func(t *testing.T) {
		const rootConfig = "terramate.tm.hcl"

		s.RootEntry().CreateFile(rootConfig, `
			terramate {
			  config {
			    git {
			      check_remote = false
			    }
			  }
			}
		`)
		defer s.RootEntry().RemoveFile(rootConfig)

		git.Add(rootConfig)
		git.Commit("commit root config")

		assertRunResult(t, tmcli.run("run", cat, someFile.HostPath()), runExpected{
			Stdout: fileContents,
		})
	})
}

func TestRunFailsIfCurrentBranchIsMainAndItIsOutdated(t *testing.T) {
	s := sandbox.New(t)

	stack := s.CreateStack("stack-1")
	mainTfFile := stack.CreateFile("main.tf", "# no code")

	ts := newCLI(t, s.RootDir())

	git := s.Git()

	git.Add(".")
	git.Commit("all")

	setupLocalMainBranchBehindOriginMain(git, func() {
		stack.CreateFile("tempfile", "any content")
	})

	wantRes := runExpected{
		Status:      1,
		StderrRegex: string(cli.ErrOutdatedLocalRev),
	}

	cat := test.LookPath(t, "cat")
	// terramate run should also check if local default branch is updated with remote
	assertRunResult(t, ts.run(
		"run",
		cat,
		mainTfFile.HostPath(),
	), wantRes)
}

func TestRunWithoutGitRemoteCheckWorksWithoutNetworking(t *testing.T) {
	// Regression test to guarantee that all git checks
	// are disabled and no git operation will be performed on this case.
	// So running terramate run --disable-check-git-remote will
	// not fail if there is no networking.
	// Some people like to get some coding done on airplanes :-)
	const (
		fileContents   = "body"
		nonExistentGit = "http://non-existent/terramate.git"
	)

	s := sandbox.New(t)

	stack := s.CreateStack("stack-1")
	stackFile := stack.CreateFile("main.tf", fileContents)

	git := s.Git()
	git.Add(".")
	git.CommitAll("first commit")

	git.SetRemoteURL("origin", nonExistentGit)

	tm := newCLI(t, s.RootDir())

	cat := test.LookPath(t, "cat")
	assertRunResult(t, tm.run(
		"run",
		cat,
		stackFile.HostPath(),
	), runExpected{
		Status:      1,
		StderrRegex: "Could not resolve host: non-existent",
	})
	assertRunResult(t, tm.run(
		"run",
		"--disable-check-git-remote",
		cat,
		stackFile.HostPath(),
	), runExpected{
		Stdout: fileContents,
	})
}
func TestRunWitCustomizedEnv(t *testing.T) {
	run := func(builders ...hclwrite.BlockBuilder) *hclwrite.Block {
		return hclwrite.BuildBlock("run", builders...)
	}
	env := func(builders ...hclwrite.BlockBuilder) *hclwrite.Block {
		return hclwrite.BuildBlock("env", builders...)
	}

	const (
		stackName             = "stack"
		stackGlobal           = "global from stack"
		exportedTerramateTest = "set on terramate test process"
		newTerramateOverriden = "newValue"
	)

	s := sandbox.New(t)

	root := s.RootEntry()
	stack := s.CreateStack(stackName)

	root.CreateFile("env.tm",
		terramate(
			config(
				run(
					env(
						expr("FROM_META", "terramate.stack.name"),
						expr("FROM_GLOBAL", "global.env"),
						expr("FROM_ENV", "env.TERRAMATE_TEST"),
						str("TERRAMATE_OVERRIDDEN", newTerramateOverriden),
					),
				),
			),
		).String(),
	)
	stack.CreateFile("globals.tm", globals(
		str("env", stackGlobal),
	).String())

	git := s.Git()
	git.Add(".")
	git.CommitAll("first commit")

	hostenv := os.Environ()
	clienv := append(hostenv,
		"TERRAMATE_OVERRIDDEN=oldValue",
		fmt.Sprintf("TERRAMATE_TEST=%s", exportedTerramateTest),
	)

	tm := newCLI(t, s.RootDir())
	tm.env = clienv

	res := tm.run("run", testHelperBin, "env")
	if res.Status != 0 {
		t.Errorf("unexpected status code %d", res.Status)
		t.Logf("stdout:\n%s", res.Stdout)
		t.Logf("stderr:\n%s", res.Stderr)
		return
	}

	wantenv := append(hostenv,
		fmt.Sprintf("FROM_META=%s", stackName),
		fmt.Sprintf("FROM_GLOBAL=%s", stackGlobal),
		fmt.Sprintf("FROM_ENV=%s", exportedTerramateTest),
		fmt.Sprintf("TERRAMATE_TEST=%s", exportedTerramateTest),
		fmt.Sprintf("TERRAMATE_OVERRIDDEN=%s", newTerramateOverriden),
	)
	gotenv := strings.Split(strings.Trim(res.Stdout, "\n"), "\n")

	sort.Strings(gotenv)
	sort.Strings(wantenv)

	test.AssertDiff(t, gotenv, wantenv)

	t.Run("ExperimentalRunEnv", func(t *testing.T) {
		want := fmt.Sprintf(`
stack "/stack":
	FROM_ENV=%s
	FROM_GLOBAL=%s
	FROM_META=%s
	TERRAMATE_OVERRIDDEN=%s
`, exportedTerramateTest, stackGlobal, stackName, newTerramateOverriden)

		assertRunResult(t, tm.run("experimental", "run-env"), runExpected{
			Stdout: want})
	})
}

func listStacks(stacks ...string) string {
	return strings.Join(stacks, "\n") + "\n"
}
