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

package cli_test

import (
	"testing"

	"github.com/mineiros-io/terramate"
	"github.com/mineiros-io/terramate/test/sandbox"
)

func TestCLIRunOrder(t *testing.T) {
	type testcase struct {
		name   string
		layout []string
		want   runResult
	}

	for _, tc := range []testcase{
		{
			name: "one stack",
			layout: []string{
				"s:stack-a",
			},
			want: runResult{
				Stdout: `stack-a
`,
			},
		},
		{
			name: "empty ordering",
			layout: []string{
				"s:stack:after=[]",
			},
			want: runResult{
				Stdout: `stack
`,
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
			want: runResult{
				Stdout: `1
2
3
batatinha
boom
frita
`,
			},
		},
		{
			name: "stack-b after stack-a",
			layout: []string{
				"s:stack-a",
				`s:stack-b:after=["../stack-a"]`,
			},
			want: runResult{
				Stdout: `stack-a
stack-b
`,
			},
		},
		{
			name: "stack-c after stack-b after stack-a",
			layout: []string{
				"s:stack-a",
				`s:stack-b:after=["../stack-a"]`,
				`s:stack-c:after=["../stack-b"]`,
			},
			want: runResult{
				Stdout: `stack-a
stack-b
stack-c
`,
			},
		},
		{
			name: "stack-a after stack-b after stack-c",
			layout: []string{
				"s:stack-c",
				`s:stack-b:after=["../stack-c"]`,
				`s:stack-a:after=["../stack-b"]`,
			},
			want: runResult{
				Stdout: `stack-c
stack-b
stack-a
`,
			},
		},
		{
			name: "stack-a after stack-b",
			layout: []string{
				`s:stack-a:after=["../stack-b"]`,
				`s:stack-b`,
			},
			want: runResult{
				Stdout: `stack-b
stack-a
`,
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
			want: runResult{
				Stdout: `stack-b
stack-c
stack-d
stack-a
`,
			},
		},
		{
			name: "stack-c after stack-b after stack-a, stack-d after stack-z",
			layout: []string{
				`s:stack-c:after=["../stack-b"]`,
				`s:stack-b:after=["../stack-a"]`,
				`s:stack-a`,
				`s:stack-d:after=["../stack-z"]`,
				`s:stack-z`,
			},
			want: runResult{
				Stdout: `stack-a
stack-b
stack-c
stack-z
stack-d
`,
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
			want: runResult{
				Stdout: `stack-a
stack-b
stack-c
stack-d
`,
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
			want: runResult{
				Stdout: `stack-a
stack-b
stack-c
stack-d
stack-z
`,
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
			want: runResult{
				Stdout: `stack-a
stack-b
stack-c
stack-g
stack-d
stack-z
`,
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
			want: runResult{
				Stdout: `stack-d
stack-f
stack-b
stack-g
stack-h
stack-c
stack-a
`,
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
			want: runResult{
				Stdout: `stack-b
stack-c
stack-a
stack-d
stack-z
`,
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
			want: runResult{
				Stdout: `stack-x
stack-y
stack-a
stack-b
stack-c
stack-d
stack-z
`,
			},
		},
		{
			name: "stack-a after stack-a - fails",
			layout: []string{
				`s:stack-a:after=["../stack-a"]`,
			},
			want: runResult{
				Error:        terramate.ErrRunCycleDetected,
				IgnoreStderr: true,
			},
		},
		{
			name: "stack-a after . - fails",
			layout: []string{
				`s:stack-a:after=["."]`,
			},
			want: runResult{
				Error:        terramate.ErrRunCycleDetected,
				IgnoreStderr: true,
			},
		},
		{
			name: "stack-a after stack-b after stack-c after stack-a - fails",
			layout: []string{
				`s:stack-a:after=["../stack-b"]`,
				`s:stack-b:after=["../stack-c"]`,
				`s:stack-c:after=["../stack-a"]`,
			},
			want: runResult{
				Error:        terramate.ErrRunCycleDetected,
				IgnoreStderr: true,
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
			want: runResult{
				Error:        terramate.ErrRunCycleDetected,
				IgnoreStderr: true,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := sandbox.New(t)
			s.BuildTree(tc.layout)

			cli := newCLI(t, s.BaseDir())
			assertRunResult(t, cli.run("plan", "run-order"), tc.want)
		})
	}
}
