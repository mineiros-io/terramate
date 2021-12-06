package hhcl_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/madlambda/spells/assert"
	"github.com/mineiros-io/terrastack/hcl"
	"github.com/mineiros-io/terrastack/hcl/hhcl"
	"github.com/mineiros-io/terrastack/test"
)

func TestHHCLParserModules(t *testing.T) {
	type want struct {
		modules   []hcl.Module
		err       error
		errPrefix error
	}
	type testcase struct {
		name  string
		input string
		want
	}

	for _, tc := range []testcase{
		{
			name:  "ignore module type with no label",
			input: `module {}`,
		},
		{
			name:  "ignore module type with no source attribute",
			input: `module "test" {}`,
		},
		{
			name:  "empty source is a valid module",
			input: `module "test" {source = ""}`,
			want: want{
				modules: []hcl.Module{
					{
						Source: "",
					},
				},
			},
		},
		{
			name:  "valid module",
			input: `module "test" {source = "test"}`,
			want: want{
				modules: []hcl.Module{
					{
						Source: "test",
					},
				},
			},
		},
		{
			name: "mixing modules and attributes, ignore attrs",
			input: `
a = 1
module "test" {
	source = "test"
}
b = 1
`,
			want: want{
				modules: []hcl.Module{
					{
						Source: "test",
					},
				},
			},
		},
		{
			name: "multiple modules",
			input: `
a = 1
module "test" {
	source = "test"
}
b = 1
module "bleh" {
	source = "bleh"
}
`,
			want: want{
				modules: []hcl.Module{
					{
						Source: "test",
					},
					{
						Source: "bleh",
					},
				},
			},
		},
		{
			name: "ignore if source is not a string",
			input: `
module "test" {
	source = -1
}
`,
		},
		{
			input: "module \"test\" {\nsource = \"${var.test}\"\n}\n",
			want: want{
				errPrefix: fmt.Errorf("looking for \"test\".source attribute: " +
					"failed to evaluate"),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := test.WriteFile(t, "", "main.tf", tc.input)

			parser := hhcl.NewParser()
			modules, err := parser.ParseModules(path)
			if tc.want.errPrefix != nil {
				if err == nil {
					t.Fatalf("expects error prefix: %v", tc.want.errPrefix)
				}
				if !strings.HasPrefix(err.Error(), tc.want.errPrefix.Error()) {
					t.Fatalf("got[%v] but wants prefix [%v]", err, tc.want.errPrefix)
				}
			} else if tc.want.err != nil {
				assert.EqualErrs(t, tc.want.err, err, "failed to parse module %q", path)
			}

			assert.EqualInts(t, len(tc.modules), len(modules), "modules len mismatch")

			for i := 0; i < len(tc.want.modules); i++ {
				assert.EqualStrings(t, tc.want.modules[i].Source, modules[i].Source,
					"module source mismatch")
			}
		})
	}
}

func TestHHCLParserTerrastackBlock(t *testing.T) {
	type want struct {
		err   error
		block hcl.Terrastack
	}
	type testcase struct {
		name  string
		input string
		want  want
	}

	for _, tc := range []testcase{
		{
			name: "empty config",
			want: want{
				err: hcl.ErrNoTerrastackBlock,
			},
		},
		{
			name: "required_version > 0.0.0",
			input: `
terrastack {
	required_version = "> 0.0.0"
}
`,
			want: want{
				block: hcl.Terrastack{
					RequiredVersion: "> 0.0.0",
				},
			},
		},
		{
			name: "after: empty set works",
			input: `
terrastack {
	required_version = ""
	after = []
}`,
		},
		{
			name: "'after' single entry",
			input: `
terrastack {
	required_version = ""
	after = ["test"]
}`,
			want: want{
				block: hcl.Terrastack{
					After: []string{"test"},
				},
			},
		},
		{
			name: "'after' invalid element entry",
			input: `
terrastack {
	required_version = ""
	after = [1]
}`,
			want: want{
				err: hcl.ErrInvalidRunOrder,
			},
		},
		{
			name: "'after' duplicated entry",
			input: `
terrastack {
	required_version = ""
	after = ["test", "test"]
}`,
			want: want{
				err: hcl.ErrInvalidRunOrder,
			},
		},
		{
			name: "multiple 'after' fields",
			input: `
terrastack {
	required_version = ""
	after = ["test"]
	after = []
}`,
			want: want{
				err: hcl.ErrHCLSyntax,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := hhcl.NewParser()
			got, err := p.Parse(tc.name, []byte(tc.input))
			assert.IsError(t, err, tc.want.err)

			if tc.want.err == nil {
				test.AssertTerrastackBlock(t, *got, tc.want.block)
			}
		})
	}
}
