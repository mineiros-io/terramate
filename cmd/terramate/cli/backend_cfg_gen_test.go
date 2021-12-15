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
	"bytes"
	"path/filepath"
	"testing"
	"text/template"

	"github.com/google/go-cmp/cmp"
	"github.com/mineiros-io/terramate"
	"github.com/mineiros-io/terramate/hcl"
	"github.com/mineiros-io/terramate/test"
	"github.com/mineiros-io/terramate/test/sandbox"
)

// TODO(katcipis):
// - unknown metadata on config
// - unknown namespace on config

func TestBackendConfigGeneration(t *testing.T) {
	type (
		stackcode struct {
			relpath string
			code    string
		}

		backendconfig struct {
			relpath string
			config  string
		}

		want struct {
			res    runResult
			stacks []stackcode
		}

		testcase struct {
			name    string
			layout  []string
			configs []backendconfig
			want    want
		}

		metadata struct {
			Stack struct {
				Name string
				Path string
			}
		}
	)
	tcases := []testcase{
		{
			name: "multiple stacks with no config",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2",
				"s:stacks/stack-3",
			},
		},
		{
			name:   "fails on single stack with invalid config",
			layout: []string{"s:stack"},
			configs: []backendconfig{
				{
					relpath: "stack",
					config: `terramate {
  required_version = "~> 0.0.0"
  backend {}
}`,
				},
			},
			want: want{
				res: runResult{
					Error:        hcl.ErrMalformedTerramateConfig,
					IgnoreStdout: true,
				},
			},
		},
		{
			name: "multiple stacks and one has invalid config fails",
			layout: []string{
				"s:stack-invalid-backend",
				"s:stack-ok-backend",
			},
			configs: []backendconfig{
				{
					relpath: "stack-invalid-backend",
					config: `terramate {
  required_version = "~> 0.0.0"
  backend {}
}`,
				},
				{
					relpath: "stack-ok-backend",
					config: `terramate {
  required_version = "~> 0.0.0"
  backend "valid" {}
}`,
				},
			},
			want: want{
				res: runResult{
					Error:        hcl.ErrMalformedTerramateConfig,
					IgnoreStdout: true,
				},
			},
		},
		{
			name:   "single stack with config on stack and empty config",
			layout: []string{"s:stack"},
			configs: []backendconfig{
				{
					relpath: "stack",
					config: `terramate {
  required_version = "~> 0.0.0"
  backend "sometype" {}
}

stack {}`,
				},
			},
			want: want{
				stacks: []stackcode{
					{
						relpath: "stack",
						code: `terraform {
  backend "sometype" {
  }
}
`,
					},
				},
				res: runResult{IgnoreStdout: true},
			},
		},
		{
			name:   "single stack with config on stack and empty config label",
			layout: []string{"s:stack"},
			configs: []backendconfig{
				{
					relpath: "stack",
					config: `terramate {
  required_version = "~> 0.0.0"
  backend "" {}
}

stack {}`,
				},
			},
			want: want{
				stacks: []stackcode{
					{
						relpath: "stack",
						code: `terraform {
  backend "" {
  }
}
`,
					},
				},
				res: runResult{IgnoreStdout: true},
			},
		},
		{
			name:   "single stack with config on stack and config with 1 attr",
			layout: []string{"s:stack"},
			configs: []backendconfig{
				{
					relpath: "stack",
					config: `terramate {
  required_version = "~> 0.0.0"
  backend "sometype" {
    attr = "value"
  }
}

stack {}`,
				},
			},
			want: want{
				stacks: []stackcode{
					{
						relpath: "stack",
						code: `terraform {
  backend "sometype" {
    attr = "value"
  }
}
`,
					},
				},
				res: runResult{IgnoreStdout: true},
			},
		},
		{
			name:   "multiple stacks with config on each stack",
			layout: []string{"s:stack-1"},
			configs: []backendconfig{
				{
					relpath: "stack-1",
					config: `terramate {
  required_version = "~> 0.0.0"
  backend "1" {
    attr = "hi"
  }
}

stack {}`,
				},
				{
					relpath: "stack-2",
					config: `terramate {
  required_version = "~> 0.0.0"
  backend "2" {
    somebool = true
  }
}

stack {}`,
				},
				{
					relpath: "stack-3",
					config: `terramate {
  required_version = "~> 0.0.0"
  backend "3" {
    somelist = ["m", "i", "n", "e", "i", "r", "o", "s"]
  }
}

stack {}`,
				},
			},
			want: want{
				stacks: []stackcode{
					{
						relpath: "stack-1",
						code: `terraform {
  backend "1" {
    attr = "hi"
  }
}
`,
					},
					{
						relpath: "stack-2",
						code: `terraform {
  backend "2" {
    somebool = true
  }
}
`,
					},
					{
						relpath: "stack-3",
						code: `terraform {
  backend "3" {
    somelist = ["m", "i", "n", "e", "i", "r", "o", "s"]
  }
}
`,
					},
				},
				res: runResult{IgnoreStdout: true},
			},
		},
		{
			name:   "single stack with config on stack with config N attrs",
			layout: []string{"s:stack"},
			configs: []backendconfig{
				{
					relpath: "stack",
					config: `terramate {
  required_version = "~> 0.0.0"
  backend "lotsoftypes" {
    attr = "value"
    attrnumber = 5
    attrbool = true
    somelist = ["hi", "again"]
  }
}

stack {}
`,
				},
			},
			want: want{
				stacks: []stackcode{
					{
						relpath: "stack",
						code: `terraform {
  backend "lotsoftypes" {
    attr       = "value"
    attrbool   = true
    attrnumber = 5
    somelist   = ["hi", "again"]
  }
}
`,
					},
				},
				res: runResult{IgnoreStdout: true},
			},
		},
		{
			name:   "single stack with config on stack with subblock",
			layout: []string{"s:stack"},
			configs: []backendconfig{
				{
					relpath: "stack",
					config: `terramate {
  required_version = "~> 0.0.0"
  backend "lotsoftypes" {
    attr = "value"
    block {
      attrbool   = true
      attrnumber = 5
      somelist   = ["hi", "again"]
    }
  }
}

stack {}`,
				},
			},
			want: want{
				stacks: []stackcode{
					{
						relpath: "stack",
						code: `terraform {
  backend "lotsoftypes" {
    attr = "value"
    block {
      attrbool   = true
      attrnumber = 5
      somelist   = ["hi", "again"]
    }
  }
}
`,
					},
				},
				res: runResult{IgnoreStdout: true},
			},
		},
		{
			name:   "single stack with config parent dir",
			layout: []string{"s:stacks/stack"},
			configs: []backendconfig{
				{
					relpath: "stacks",
					config: `terramate {
  backend "fromparent" {
    attr = "value"
  }
}`,
				},
			},
			want: want{
				stacks: []stackcode{
					{
						relpath: "stacks/stack",
						code: `terraform {
  backend "fromparent" {
    attr = "value"
  }
}
`,
					},
				},
				res: runResult{IgnoreStdout: true},
			},
		},
		{
			name:   "single stack with config on basedir",
			layout: []string{"s:stacks/stack"},
			configs: []backendconfig{
				{
					relpath: ".",
					config: `terramate {
  backend "basedir_config" {
    attr = 666
  }
}`,
				},
			},
			want: want{
				stacks: []stackcode{
					{
						relpath: "stacks/stack",
						code: `terraform {
  backend "basedir_config" {
    attr = 666
  }
}
`,
					},
				},
				res: runResult{IgnoreStdout: true},
			},
		},
		{
			name: "multiple stacks with config on basedir",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2",
			},
			configs: []backendconfig{
				{
					relpath: ".",
					config: `terramate {
  backend "basedir_config" {
    attr = "test"
  }
}`,
				},
			},
			want: want{
				stacks: []stackcode{
					{
						relpath: "stacks/stack-1",
						code: `terraform {
  backend "basedir_config" {
    attr = "test"
  }
}
`,
					},
					{
						relpath: "stacks/stack-2",
						code: `terraform {
  backend "basedir_config" {
    attr = "test"
  }
}
`,
					},
				},
				res: runResult{IgnoreStdout: true},
			},
		},
		{
			name: "stacks on different envs with per env config",
			layout: []string{
				"s:envs/prod/stacks/stack",
				"s:envs/staging/stacks/stack",
			},
			configs: []backendconfig{
				{
					relpath: "envs/prod",
					config: `terramate {
  backend "remote" {
    environment = "prod"
  }
}`,
				},
				{
					relpath: "envs/staging",
					config: `terramate {
  backend "remote" {
    environment = "staging"
  }
}
`,
				},
			},
			want: want{
				stacks: []stackcode{
					{
						relpath: "envs/prod/stacks/stack",
						code: `terraform {
  backend "remote" {
    environment = "prod"
  }
}
`,
					},
					{
						relpath: "envs/staging/stacks/stack",
						code: `terraform {
  backend "remote" {
    environment = "staging"
  }
}
`,
					},
				},
				res: runResult{IgnoreStdout: true},
			},
		},
		{
			name:   "single stack with config on stack and N attrs using metadata",
			layout: []string{"s:stack-metadata"},
			configs: []backendconfig{
				{
					relpath: "stack-metadata",
					config: `terramate {
  required_version = "~> 0.0.0"
  backend "metadata" {
    name = terramate.name
    path = terramate.path
    somelist = [terramate.name, terramate.path]
  }
}
stack {}`,
				},
			},
			want: want{
				stacks: []stackcode{
					{
						relpath: "stack-metadata",
						code: `terraform {
  backend "metadata" {
    name     = "{{.Stack.Name}}"
    path     = "{{.Stack.Path}}"
    somelist = ["{{.Stack.Name}}", "{{.Stack.Path}}"]
  }
}
`,
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
				dir := filepath.Join(s.BaseDir(), cfg.relpath)
				test.WriteFile(t, dir, terramate.ConfigFilename, cfg.config)
			}

			ts := newCLI(t, s.BaseDir())

			assertRunResult(t, ts.run("generate"), tcase.want.res)

			for _, want := range tcase.want.stacks {
				stack := s.StackEntry(want.relpath)
				got := string(stack.ReadGeneratedTf())

				// Use templating to allow testing of metadata eval
				stackMetadata := metadata{}
				stackMetadata.Stack.Name = filepath.Base(want.relpath)
				stackMetadata.Stack.Path = "/" + want.relpath

				code := applyTemplate(t, want.code, stackMetadata)

				wantcode := terramate.GeneratedCodeHeader + code

				if diff := cmp.Diff(wantcode, got); diff != "" {
					t.Error("generated code doesn't match expectation")
					t.Errorf("want:\n%q", wantcode)
					t.Errorf("got:\n%q", got)
					t.Fatalf("diff:\n%s", diff)
				}
			}

			// TODO(katcipis): proper unexpected generated code check
		})
	}

}

func applyTemplate(t *testing.T, templ string, data interface{}) string {
	tmpl, err := template.New(t.Name()).Parse(templ)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	err = tmpl.Execute(&b, data)
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
