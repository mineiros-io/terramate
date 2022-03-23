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

package genhcl_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/madlambda/spells/assert"
	"github.com/mineiros-io/terramate/config"
	"github.com/mineiros-io/terramate/generate/genhcl"
	"github.com/mineiros-io/terramate/hcl/eval"
	"github.com/mineiros-io/terramate/test"
	"github.com/mineiros-io/terramate/test/hclwrite"
	"github.com/mineiros-io/terramate/test/sandbox"
	"github.com/rs/zerolog"
)

func TestLoadGeneratedHCL(t *testing.T) {
	type (
		hclconfig struct {
			path     string
			filename string
			add      fmt.Stringer
		}
		genHCL struct {
			body   fmt.Stringer
			origin string
		}
		result struct {
			name string
			hcl  genHCL
		}
		testcase struct {
			name    string
			stack   string
			configs []hclconfig
			want    []result
			wantErr error
		}
	)

	attr := func(name, expr string) hclwrite.BlockBuilder {
		t.Helper()
		return hclwrite.AttributeValue(t, name, expr)
	}
	defaultCfg := func(dir string) string {
		return filepath.Join(dir, config.DefaultFilename)
	}

	tcases := []testcase{
		{
			name:  "no generation",
			stack: "/stack",
		},
		{
			name:  "empty content block generates empty code",
			stack: "/stack",
			configs: []hclconfig{
				{
					path: "/stack",
					add: generateHCL(
						labels("empty"),
						content(),
					),
				},
			},
			want: []result{
				{
					name: "empty",
					hcl: genHCL{
						origin: defaultCfg("/stack"),
						body:   hcldoc(),
					},
				},
			},
		},
		{
			name:  "generate hcl on stack with single empty block",
			stack: "/stack",
			configs: []hclconfig{
				{
					path: "/stack",
					add: generateHCL(
						labels("emptytest"),
						content(
							block("empty"),
						),
					),
				},
			},
			want: []result{
				{
					name: "emptytest",
					hcl: genHCL{
						origin: defaultCfg("/stack"),
						body:   block("empty"),
					},
				},
			},
		},
		{
			name:  "generate hcl with only attributes on root body",
			stack: "/stack",
			configs: []hclconfig{
				{
					path: "/stack",
					add: generateHCL(
						labels("attrs"),
						content(
							number("num", 666),
							str("str", "hi"),
						),
					),
				},
			},
			want: []result{
				{
					name: "attrs",
					hcl: genHCL{
						origin: defaultCfg("/stack"),
						body: hcldoc(
							number("num", 666),
							str("str", "hi"),
						),
					},
				},
			},
		},
		{
			name:  "generate hcl with attrs referencing attrs on root",
			stack: "/stack",
			configs: []hclconfig{
				{
					path: "/stack",
					add: generateHCL(
						labels("attrs"),
						content(
							number("a", 666),
							expr("b", "a"),
						),
					),
				},
			},
			want: []result{
				{
					name: "attrs",
					hcl: genHCL{
						origin: defaultCfg("/stack"),
						body: hcldoc(
							number("a", 666),
							expr("b", "a"),
						),
					},
				},
			},
		},
		{
			name:  "generate hcl with attributes and blocks on root body",
			stack: "/stack",
			configs: []hclconfig{
				{
					path: "/stack",
					add: generateHCL(
						labels("attrs"),
						content(
							number("num", 666),
							block("test"),
							str("str", "hi"),
						),
					),
				},
			},
			want: []result{
				{
					name: "attrs",
					hcl: genHCL{
						origin: defaultCfg("/stack"),
						body: hcldoc(
							number("num", 666),
							str("str", "hi"),
							block("test"),
						),
					},
				},
			},
		},
		{
			name:  "scope traversals of unknown namespaces are copied as is",
			stack: "/stack",
			configs: []hclconfig{
				{
					path: "/stack",
					add: generateHCL(
						labels("scope_traversal"),
						content(
							block("traversals",
								expr("local", "local.something"),
								expr("mul", "omg.wat.something"),
								expr("res", "resource.something"),
								expr("val", "omg.something"),
							),
						),
					),
				},
			},
			want: []result{
				{
					name: "scope_traversal",
					hcl: genHCL{
						origin: defaultCfg("/stack"),
						body: block("traversals",
							expr("local", "local.something"),
							expr("mul", "omg.wat.something"),
							expr("res", "resource.something"),
							expr("val", "omg.something"),
						),
					},
				},
			},
		},
		{
			name:  "generate HCL on stack with single block",
			stack: "/stack",
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						str("some_string", "string"),
						number("some_number", 777),
						boolean("some_bool", true),
					),
				},
				{
					path: "/stack",
					add: generateHCL(
						labels("test"),
						content(
							block("testblock",
								expr("bool", "global.some_bool"),
								expr("number", "global.some_number"),
								expr("string", "global.some_string"),
								expr("obj", `{
									string = global.some_string
									number = global.some_number
									bool = global.some_bool
								}`),
							),
						),
					),
				},
			},
			want: []result{
				{
					name: "test",
					hcl: genHCL{
						origin: defaultCfg("/stack"),
						body: block("testblock",
							boolean("bool", true),
							number("number", 777),
							attr("obj", `{
								string = "string"
								number = 777
								bool   = true
							}`),
							str("string", "string"),
						),
					},
				},
			},
		},
		{
			name:  "generate HCL on root with multiple files",
			stack: "/stack",
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						str("some_string", "string"),
						number("some_number", 777),
						boolean("some_bool", true),
					),
				},
				{
					path:     "/",
					filename: "root.tm.hcl",
					add: generateHCL(
						labels("test"),
						content(
							block("testblock",
								expr("bool", "global.some_bool"),
								expr("number", "global.some_number"),
								expr("string", "global.some_string"),
							),
						),
					),
				},
				{
					path:     "/",
					filename: "root2.tm.hcl",
					add: generateHCL(
						labels("test2"),
						content(
							block("testblock2",
								expr("obj", `{
									string = global.some_string
									number = global.some_number
									bool = global.some_bool
								}`),
							),
						),
					),
				},
			},
			want: []result{
				{
					name: "test",
					hcl: genHCL{
						origin: "/root.tm.hcl",
						body: block("testblock",
							boolean("bool", true),
							number("number", 777),
							str("string", "string"),
						),
					},
				},
				{
					name: "test2",
					hcl: genHCL{
						origin: "/root2.tm.hcl",
						body: block("testblock2",
							attr("obj", `{
								string = "string"
								number = 777
								bool   = true
							}`),
						),
					},
				},
			},
		},
		{
			name:  "generate HCL on stack with multiple files",
			stack: "/stack",
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						str("some_string", "string"),
						number("some_number", 777),
						boolean("some_bool", true),
					),
				},
				{
					path:     "/stack",
					filename: "test.tm.hcl",
					add: generateHCL(
						labels("test"),
						content(
							block("testblock",
								expr("bool", "global.some_bool"),
								expr("number", "global.some_number"),
								expr("string", "global.some_string"),
							),
						),
					),
				},
				{
					path:     "/stack",
					filename: "test2.tm.hcl",
					add: generateHCL(
						labels("test2"),
						content(
							block("testblock2",
								expr("obj", `{
									string = global.some_string
									number = global.some_number
									bool = global.some_bool
								}`),
							),
						),
					),
				},
			},
			want: []result{
				{
					name: "test",
					hcl: genHCL{
						origin: "/stack/test.tm.hcl",
						body: block("testblock",
							boolean("bool", true),
							number("number", 777),
							str("string", "string"),
						),
					},
				},
				{
					name: "test2",
					hcl: genHCL{
						origin: "/stack/test2.tm.hcl",
						body: block("testblock2",
							attr("obj", `{
								string = "string"
								number = 777
								bool   = true
							}`),
						),
					},
				},
			},
		},
		{
			name:  "generate HCL on stack using try and labeled block",
			stack: "/stack",
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						attr("obj", `{
							field_a = "a"
							field_b = "b"
							field_c = "c"
						}`),
					),
				},
				{
					path: "/stack",
					add: generateHCL(
						labels("test"),
						content(
							block("labeled",
								labels("label1", "label2"),
								expr("field_a", "try(global.obj.field_a, null)"),
								expr("field_b", "try(global.obj.field_b, null)"),
								expr("field_c", "try(global.obj.field_c, null)"),
								expr("field_d", "tm_try(global.obj.field_d, null)"),
							),
						),
					),
				},
			},
			want: []result{
				{
					name: "test",
					hcl: genHCL{
						origin: defaultCfg("/stack"),
						body: block("labeled",
							labels("label1", "label2"),
							expr("field_a", `try("a", null)`),
							expr("field_b", `try("b", null)`),
							expr("field_c", `try("c", null)`),
							attr("field_d", "null"),
						),
					},
				},
			},
		},
		{
			name:  "generate HCL on stack with single nested block",
			stack: "/stack",
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						str("some_string", "string"),
						number("some_number", 777),
						boolean("some_bool", true),
					),
				},
				{
					path:     "/stack",
					filename: "genhcl.tm.hcl",
					add: generateHCL(
						labels("nesting"),
						content(
							block("block1",
								expr("bool", "global.some_bool"),
								block("block2",
									expr("number", "global.some_number"),
									block("block3",
										expr("string", "global.some_string"),
										expr("obj", `{
											string = global.some_string
											number = global.some_number
											bool = global.some_bool
										}`),
									),
								),
							),
						),
					),
				},
			},
			want: []result{
				{
					name: "nesting",
					hcl: genHCL{
						origin: "/stack/genhcl.tm.hcl",
						body: block("block1",
							boolean("bool", true),
							block("block2",
								number("number", 777),
								block("block3",
									attr("obj", `{
										string = "string"
										number = 777
										bool   = true
									}`),
									str("string", "string"),
								),
							),
						),
					},
				},
			},
		},
		{
			name:  "multiple generate HCL blocks on single file",
			stack: "/stack",
			configs: []hclconfig{
				{
					path: "/stack",
					add: hcldoc(
						globals(
							str("some_string", "string"),
							number("some_number", 666),
							boolean("some_bool", true),
						),
						generateHCL(
							labels("exported_one"),
							content(
								block("block1",
									expr("bool", "global.some_bool"),
									block("block2",
										expr("number", "global.some_number"),
									),
								),
							),
						),
						generateHCL(
							labels("exported_two"),
							content(
								block("yay",
									expr("data", "global.some_string"),
								),
							),
						),
						generateHCL(
							labels("exported_three"),
							content(
								block("something",
									expr("number", "global.some_number"),
								),
							),
						),
					),
				},
			},
			want: []result{
				{
					name: "exported_one",
					hcl: genHCL{
						origin: defaultCfg("/stack"),
						body: block("block1",
							boolean("bool", true),
							block("block2",
								number("number", 666),
							),
						),
					},
				},
				{
					name: "exported_two",
					hcl: genHCL{
						origin: defaultCfg("/stack"),
						body: block("yay",
							str("data", "string"),
						),
					},
				},
				{
					name: "exported_three",
					hcl: genHCL{
						origin: defaultCfg("/stack"),
						body: block("something",
							number("number", 666),
						),
					},
				},
			},
		},
		{
			name:  "generate HCL on stack parent dir",
			stack: "/stacks/stack",
			configs: []hclconfig{
				{
					path: "/",
					add: globals(
						str("some_string", "string"),
						number("some_number", 777),
						boolean("some_bool", true),
					),
				},
				{
					path: "/stacks",
					add: generateHCL(
						labels("on_parent"),
						content(
							block("on_parent_block",
								expr("obj", `{
									string = global.some_string
									number = global.some_number
									bool = global.some_bool
								}`),
							),
						),
					),
				},
			},
			want: []result{
				{
					name: "on_parent",
					hcl: genHCL{
						origin: defaultCfg("/stacks"),
						body: block("on_parent_block",
							attr("obj", `{
								string = "string"
								number = 777
								bool   = true
							}`),
						),
					},
				},
			},
		},
		{
			name:  "generate HCL on project root dir",
			stack: "/stacks/stack",
			configs: []hclconfig{
				{
					path: "/",
					add: generateHCL(
						labels("root"),
						content(
							block("root",
								expr("test", "terramate.path"),
							),
						),
					),
				},
			},
			want: []result{
				{
					name: "root",
					hcl: genHCL{
						origin: defaultCfg("/"),
						body: block("root",
							str("test", "/stacks/stack"),
						),
					},
				},
			},
		},
		{
			name:  "generate HCL on all dirs of the project with different labels",
			stack: "/stacks/stack",
			configs: []hclconfig{
				{
					path: "/",
					add: hcldoc(
						globals(
							str("some_string", "string"),
							number("some_number", 777),
							boolean("some_bool", true),
						),
						generateHCL(
							labels("on_root"),
							content(
								block("on_root_block",
									expr("obj", `{
										string = global.some_string
									}`),
								),
							),
						),
					),
				},
				{
					path: "/stacks",
					add: generateHCL(
						labels("on_parent"),
						content(
							block("on_parent_block",
								expr("obj", `{
									number = global.some_number
								}`),
							),
						),
					),
				},
				{
					path: "/stacks/stack",
					add: generateHCL(
						labels("on_stack"),
						content(
							block("on_stack_block",
								expr("obj", `{
									bool = global.some_bool
								}`),
							),
						),
					),
				},
			},
			want: []result{
				{
					name: "on_root",
					hcl: genHCL{
						origin: defaultCfg("/"),
						body: block("on_root_block",
							attr("obj", `{
								string = "string"
							}`),
						),
					},
				},
				{
					name: "on_parent",
					hcl: genHCL{
						origin: defaultCfg("/stacks"),
						body: block("on_parent_block",
							attr("obj", `{
								number = 777
							}`),
						),
					},
				},
				{
					name: "on_stack",
					hcl: genHCL{
						origin: defaultCfg("/stacks/stack"),
						body: block("on_stack_block",
							attr("obj", `{
								bool   = true
							}`),
						),
					},
				},
			},
		},
		{
			name:  "stack with block with same label as parent is an error",
			stack: "/stacks/stack",
			configs: []hclconfig{
				{
					path: "/stacks",
					add: generateHCL(
						labels("repeated"),
						content(
							block("block",
								str("data", "parent data"),
							),
						),
					),
				},
				{
					path: "/stacks/stack",
					add: generateHCL(
						labels("repeated"),
						content(
							block("block",
								str("data", "stack data"),
							),
						),
					),
				},
			},
			wantErr: genhcl.ErrMultiLevelConflict,
		},
		{
			name:  "stack parents with block with same label is an error",
			stack: "/stacks/stack",
			configs: []hclconfig{
				{
					path: "/",
					add: generateHCL(
						labels("repeated"),
						content(
							block("block",
								str("data", "root data"),
							),
						),
					),
				},
				{
					path: "/stacks",
					add: generateHCL(
						labels("repeated"),
						content(
							block("block",
								str("data", "parent data"),
							),
						),
					),
				},
			},
			wantErr: genhcl.ErrMultiLevelConflict,
		},
		{
			name:  "block with no label fails",
			stack: "/stacks/stack",
			configs: []hclconfig{
				{
					path: "/stacks/stack",
					add: generateHCL(
						content(
							block("block",
								str("data", "some literal data"),
							),
						),
					),
				},
			},
			wantErr: genhcl.ErrParsing,
		},
		{
			name:  "generate_hcl with non-content block inside fails",
			stack: "/stacks/stack",
			configs: []hclconfig{
				{
					path: "/stacks/stack",
					add: generateHCL(
						labels("test"),
						block("block",
							str("data", "some literal data"),
						),
					),
				},
			},
			wantErr: genhcl.ErrParsing,
		},
		{
			name:  "generate_hcl with other blocks than content fails",
			stack: "/stacks/stack",
			configs: []hclconfig{
				{
					path: "/stacks/stack",
					add: generateHCL(
						labels("test"),
						content(
							str("data", "some literal data"),
						),
						block("block",
							str("data", "some literal data"),
						),
					),
				},
			},
			wantErr: genhcl.ErrParsing,
		},
		{
			name:  "generate_hcl.content block is required",
			stack: "/stack",
			configs: []hclconfig{
				{
					path: "/stack",
					add: generateHCL(
						labels("empty"),
					),
				},
			},
			wantErr: genhcl.ErrParsing,
		},
		{
			name:  "generate_hcl.content block with label fails",
			stack: "/stack",
			configs: []hclconfig{
				{
					path: "/stack",
					add: generateHCL(
						labels("empty"),
						content(
							labels("not allowed"),
						),
					),
				},
			},
			wantErr: genhcl.ErrParsing,
		},
		{
			name:  "block with two labels on stack fails",
			stack: "/stacks/stack",
			configs: []hclconfig{
				{
					path: "/stacks/stack",
					add: block("generate_hcl",
						labels("one", "two"),
						content(
							block("block",
								str("data", "some literal data"),
							),
						),
					),
				},
			},
			wantErr: genhcl.ErrParsing,
		},
		{
			name:  "block with empty label on stack fails",
			stack: "/stacks/stack",
			configs: []hclconfig{
				{
					path: "/stacks/stack",
					add: block("generate_hcl",
						labels(""),
						content(
							block("block",
								str("data", "some literal data"),
							),
						),
					),
				},
			},
			wantErr: genhcl.ErrParsing,
		},
		{
			name:  "blocks with same label on same config fails",
			stack: "/stacks/stack",
			configs: []hclconfig{
				{
					path: "/stacks/stack",
					add: hcldoc(
						generateHCL(
							labels("duplicated"),
							content(
								terraform(
									str("data", "some literal data"),
								),
							),
						),
						generateHCL(
							labels("duplicated"),
							content(
								terraform(
									str("data2", "some literal data2"),
								),
							),
						),
					),
				},
			},
			wantErr: genhcl.ErrParsing,
		},
		{
			name:  "blocks with same label on multiple config files fails",
			stack: "/stacks/stack",
			configs: []hclconfig{
				{
					path:     "/stacks/stack",
					filename: "test.tm.hcl",
					add: hcldoc(
						generateHCL(
							labels("duplicated"),
							content(
								terraform(
									str("data", "some literal data"),
								),
							),
						),
					),
				},
				{
					path:     "/stacks/stack",
					filename: "test2.tm.hcl",
					add: hcldoc(
						generateHCL(
							labels("duplicated"),
							content(
								terraform(
									str("data", "some literal data"),
								),
							),
						),
					),
				},
			},
			wantErr: genhcl.ErrParsing,
		},
		{
			name:  "global evaluation failure",
			stack: "/stacks/stack",
			configs: []hclconfig{
				{
					path: "/stacks/stack",
					add: hcldoc(
						generateHCL(
							labels("test"),
							content(
								terraform(
									expr("required_version", "global.undefined"),
								),
							),
						),
					),
				},
			},
			wantErr: genhcl.ErrEval,
		},
		{
			name:  "metadata evaluation failure",
			stack: "/stacks/stack",
			configs: []hclconfig{
				{
					path: "/stacks/stack",
					add: hcldoc(
						generateHCL(
							labels("test"),
							content(
								terraform(
									expr("much_wrong", "terramate.undefined"),
								),
							),
						),
					),
				},
			},
			wantErr: genhcl.ErrEval,
		},
		{
			name:  "valid config on stack but invalid on parent fails",
			stack: "/stacks/stack",
			configs: []hclconfig{
				{
					path: "/stacks",
					add: generateHCL(
						block("block",
							str("data", "some literal data"),
						),
					),
				},
				{
					path: "/stacks/stack",
					add: hcldoc(
						generateHCL(
							labels("valid"),
							content(
								terraform(
									str("data", "some literal data"),
								),
							),
						),
					),
				},
			},
			wantErr: genhcl.ErrParsing,
		},
		{
			name:  "attributes on generate_hcl block fails",
			stack: "/stacks/stack",
			configs: []hclconfig{
				{
					path: "/stacks/stack",
					add: hcldoc(
						generateHCL(
							labels("test"),
							str("some_attribute", "whatever"),
							content(
								terraform(
									str("required_version", "1.11"),
								),
							),
						),
					),
				},
			},
			wantErr: genhcl.ErrParsing,
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			s := sandbox.New(t)
			stackEntry := s.CreateStack(tcase.stack)
			stack := stackEntry.Load()

			for _, cfg := range tcase.configs {
				filename := cfg.filename
				if filename == "" {
					filename = config.DefaultFilename
				}
				path := filepath.Join(s.RootDir(), cfg.path)
				test.AppendFile(t, path, filename, cfg.add.String())
			}

			meta := stack.Meta()
			globals := s.LoadStackGlobals(meta)
			res, err := genhcl.Load(s.RootDir(), meta, globals)
			assert.IsError(t, err, tcase.wantErr)

			got := res.GeneratedHCLs()

			for _, res := range tcase.want {
				gothcl, ok := got[res.name]
				if !ok {
					t.Fatalf("want hcl code to be generated for %q but no code was generated for it", res.name)
				}
				gotcode := gothcl.String()
				wantcode := res.hcl.body.String()

				assertHCLEquals(t, gotcode, wantcode)
				assert.EqualStrings(t,
					res.hcl.origin,
					gothcl.Origin(),
					"wrong origin config path for generated code",
				)

				delete(got, res.name)
			}

			assert.EqualInts(t, 0, len(got), "got unexpected exported code: %v", got)
		})
	}
}

func TestPartialEval(t *testing.T) {
	// These tests simplify the overall setup/description and focus only
	// on how code will be partially evaluated.
	// No support for multiple config files or generating multiple
	// configurations.
	type testcase struct {
		name    string
		config  hclwrite.BlockBuilder
		globals hclwrite.BlockBuilder
		want    fmt.Stringer
		wantErr error
		skip    bool
	}

	attr := func(name, expr string) hclwrite.BlockBuilder {
		t.Helper()
		return hclwrite.AttributeValue(t, name, expr)
	}

	tcases := []testcase{
		{
			name: "unknown references on attributes",
			config: hcldoc(
				expr("count", "count.index"),
				expr("data", "data.ref"),
				expr("local", "local.ref"),
				expr("module", "module.ref"),
				expr("path", "path.ref"),
				expr("resource", "resource.name.etc"),
				expr("terraform", "terraform.ref"),
			),
			want: hcldoc(
				expr("count", "count.index"),
				expr("data", "data.ref"),
				expr("local", "local.ref"),
				expr("module", "module.ref"),
				expr("path", "path.ref"),
				expr("resource", "resource.name.etc"),
				expr("terraform", "terraform.ref"),
			),
		},
		{
			name: "unknown references on object",
			config: hcldoc(
				expr("obj", `{
					count     = count.index,
					data      = data.ref,
					local     = local.ref,
					module    = module.ref,
					path      = path.ref,
					resource  = resource.name.etc,
					terraform = terraform.ref,
				 }`),
			),
			want: hcldoc(
				expr("obj", `{
					count     = count.index,
					data      = data.ref,
					local     = local.ref,
					module    = module.ref,
					path      = path.ref,
					resource  = resource.name.etc,
					terraform = terraform.ref,
				 }`),
			),
		},
		{
			name: "mixed references on same object",
			globals: hcldoc(
				globals(
					number("ref", 666),
				),
			),
			config: hcldoc(
				expr("obj", `{
					local     = local.ref,
					global    = global.ref,
				 }`),
			),
			want: hcldoc(
				expr("obj", `{
					local   = local.ref,
					global  = 666,
				 }`),
			),
		},
		{
			name: "mixed references on list",
			globals: hcldoc(
				globals(
					number("ref", 666),
				),
			),
			config: hcldoc(
				expr("list", `[ local.ref, global.ref ]`),
			),
			want: hcldoc(
				expr("list", `[ local.ref, 666 ]`),
			),
		},
		{
			name: "try with unknown reference on attribute is not evaluated",
			config: hcldoc(
				expr("attr", "try(something.val, null)"),
			),
			want: hcldoc(
				expr("attr", "try(something.val, null)"),
			),
		},
		{
			name: "try with unknown reference on list is not evaluated",
			config: hcldoc(
				expr("list", "[try(something.val, null), 1]"),
			),
			want: hcldoc(
				expr("list", "[try(something.val, null), 1]"),
			),
		},
		{
			name: "try with unknown reference on object is not evaluated",
			config: hcldoc(
				expr("obj", `{
					a = try(something.val, null),	
					b = "val",
				}`),
			),
			want: hcldoc(
				expr("obj", `{
					a = try(something.val, null),	
					b = "val",
				}`),
			),
		},
		{
			name: "function call on attr with mixed references is partially evaluated",
			globals: hcldoc(
				globals(
					attr("list", "[1, 2, 3]"),
				),
			),
			config: hcldoc(
				expr("a", "merge(something.val, global.list)"),
				expr("b", "merge(global.list, local.list)"),
			),
			want: hcldoc(
				expr("a", "merge(something.val, [1, 2, 3])"),
				expr("b", "merge([1, 2, 3], local.list)"),
			),
		},
		{
			name: "function call on obj with mixed references is partially evaluated",
			globals: hcldoc(
				globals(
					attr("list", "[1, 2, 3]"),
				),
			),
			config: hcldoc(
				expr("obj", `{
					a = merge(something.val, global.list)
				}`),
			),
			want: hcldoc(
				expr("obj", `{
					a = merge(something.val, [1, 2, 3])
				}`),
			),
		},
		{
			name: "variable interpolation of number with prefix str",
			globals: hcldoc(
				globals(
					number("num", 1337),
				),
			),
			config: hcldoc(
				str("num", `test-${global.num}`),
			),
			want: hcldoc(
				str("num", "test-1337"),
			),
		},
		{
			name: "variable interpolation of bool with prefixed str",
			globals: hcldoc(
				globals(
					boolean("flag", true),
				),
			),
			config: hcldoc(
				str("flag", `test-${global.flag}`),
			),
			want: hcldoc(
				str("flag", "test-true"),
			),
		},
		{
			name: "variable interpolation without prefixed string",
			globals: hcldoc(
				globals(
					str("string", "hello"),
				),
			),
			config: hcldoc(
				str("string", `${global.string}`),
			),
			want: hcldoc(
				str("string", "hello"),
			),
		},
		{
			name: "variable interpolation with prefixed string",
			globals: hcldoc(
				globals(
					str("string", "hello"),
				),
			),
			config: hcldoc(
				str("string", `test-${global.string}`),
			),
			want: hcldoc(
				str("string", "test-hello"),
			),
		},
		{
			name: "variable interpolation with suffixed string",
			globals: hcldoc(
				globals(
					str("string", "hello"),
				),
			),
			config: hcldoc(
				str("string", `${global.string}-test`),
			),
			want: hcldoc(
				str("string", "hello-test"),
			),
		},
		{
			name: "multiple variable interpolation with prefixed string",
			globals: globals(
				str("string1", "hello1"),
				str("string2", "hello2"),
			),
			config: hcldoc(
				str("string", `something ${global.string1} and ${global.string2}`),
			),
			want: hcldoc(
				str("string", "something hello1 and hello2"),
			),
		},
		{
			name: "multiple variable interpolation without prefixed string",
			globals: hcldoc(
				globals(
					str("string1", "hello1"),
					str("string2", "hello2"),
				),
			),
			config: hcldoc(
				str("string", `${global.string1}${global.string2}`),
			),
			want: hcldoc(
				str("string", "hello1hello2"),
			),
		},
		{
			// Here we check that an interpolated object results on the object itself, not a string.
			name: "object interpolation/serialization",
			globals: globals(
				expr("obj", `{
					string = "hello"
					number = 1337
					bool = false
				}`),
			),
			config: hcldoc(
				expr("obj", "global.obj"),
				str("obj_interpolated", "${global.obj}"),
			),
			want: hcldoc(
				expr("obj", `{
					bool = false
					number = 1337
					string = "hello"
				}`),
				expr("obj_interpolated", `{
					bool = false
					number = 1337
					string = "hello"
				}`),
			),
		},
		{
			name: "interpolating multiple objects fails",
			globals: globals(
				expr("obj", `{ string = "hello" }`),
			),
			config: hcldoc(
				str("a", "${global.obj}-${global.obj}"),
			),
			wantErr: eval.ErrInterpolationEval,
		},
		{
			name: "interpolating object with prefix space fails",
			globals: globals(
				expr("obj", `{ string = "hello" }`),
			),
			config: hcldoc(
				str("a", " ${global.obj}"),
			),
			wantErr: eval.ErrInterpolationEval,
		},
		{
			name: "interpolating object with suffix space fails",
			globals: globals(
				expr("obj", `{ string = "hello" }`),
			),
			config: hcldoc(
				str("a", "${global.obj} "),
			),
			wantErr: eval.ErrInterpolationEval,
		},
		{
			name: "interpolating multiple lists fails",
			globals: globals(
				expr("list", `["hello"]`),
			),
			config: hcldoc(
				str("a", "${global.list}-${global.list}"),
			),
			wantErr: eval.ErrInterpolationEval,
		},
		{
			name: "interpolating list with prefix space fails",
			globals: globals(
				expr("list", `["hello"]`),
			),
			config: hcldoc(
				str("a", " ${global.list}"),
			),
			wantErr: eval.ErrInterpolationEval,
		},
		{
			name: "interpolating list with suffix space fails",
			globals: globals(
				expr("list", `["hello"]`),
			),
			config: hcldoc(
				str("a", "${global.list} "),
			),
			wantErr: eval.ErrInterpolationEval,
		},
		{
			// Here we check that a interpolated lists results on the list itself, not a string.
			name: "list interpolation/serialization",
			globals: globals(
				expr("list", `["hi"]`),
			),
			config: hcldoc(
				expr("list", "global.list"),
				str("list_interpolated", "${global.list}"),
			),
			want: hcldoc(
				expr("list", `["hi"]`),
				expr("list_interpolated", `["hi"]`),
			),
		},
		{
			// Here we check that a interpolated number results on the number itself, not a string.
			name: "number interpolation/serialization",
			globals: globals(
				number("number", 666),
			),
			config: hcldoc(
				expr("number", "global.number"),
				str("number_interpolated", "${global.number}"),
			),
			want: hcldoc(
				number("number", 666),
				number("number_interpolated", 666),
			),
		},
		{
			// Here we check that multiple interpolated numbers results on a string.
			name: "multiple numbers interpolation/serialization",
			globals: globals(
				number("number", 666),
			),
			config: hcldoc(
				expr("number", "global.number"),
				str("number_interpolated", "${global.number}-${global.number}"),
			),
			want: hcldoc(
				number("number", 666),
				str("number_interpolated", "666-666"),
			),
		},
		{
			// Here we check that a interpolated booleans results on the boolean itself, not a string.
			name: "boolean interpolation/serialization",
			globals: globals(
				boolean("bool", true),
			),
			config: hcldoc(
				expr("bool", "global.bool"),
				str("bool_interpolated", "${global.bool}"),
			),
			want: hcldoc(
				boolean("bool", true),
				boolean("bool_interpolated", true),
			),
		},
		{
			// Here we check that multiple interpolated booleans results on a string.
			name: "multiple booleans interpolation/serialization",
			globals: globals(
				boolean("bool", true),
			),
			config: hcldoc(
				expr("bool", "global.bool"),
				str("bool_interpolated", "${global.bool}-${global.bool}"),
			),
			want: hcldoc(
				boolean("bool", true),
				str("bool_interpolated", "true-true"),
			),
		},
		{
			name: "test list - just to see how hcl lib serializes a list // remove me",
			globals: hcldoc(
				globals(
					expr("list", `[1, 2, 3]`),
					str("interp", "${global.list}"),
				),
			),
			config: hcldoc(
				str("var", "${global.interp}"),
			),
			want: hcldoc(
				expr("var", "[1, 2, 3]"),
			),
		},
		{
			name: "variable list interpolation/serialization in a string",
			globals: hcldoc(
				globals(
					expr("list", `[1, 2, 3]`),
				),
			),
			config: hcldoc(
				str("var", "${global.list}"),
			),
			want: hcldoc(
				expr("var", "[1, 2, 3]"),
			),
		},
		{
			name: "basic plus expression",
			config: hcldoc(
				expr("var", `1 + 1`),
			),
			want: hcldoc(
				expr("var", `1 + 1`),
			),
		},
		{
			name: "plus expression funcall",
			config: hcldoc(
				expr("var", `len(a.b) + len2(c.d)`),
			),
			want: hcldoc(
				expr("var", `len(a.b) + len2(c.d)`),
			),
		},
		{
			name: "plus expression evaluated",
			globals: hcldoc(
				globals(
					str("a", "hello"),
					str("b", "world"),
				),
			),
			config: hcldoc(
				expr("var", `tm_upper(global.a) + tm_upper(global.b)`),
			),
			want: hcldoc(
				expr("var", `"HELLO" + "WORLD"`),
			),
		},
		{
			name: "plus expression evaluated advanced",
			globals: hcldoc(
				globals(
					str("a", "hello"),
					str("b", "world"),
				),
			),
			config: hcldoc(
				expr("var", `tm_lower(tm_upper(global.a)) + tm_lower(tm_upper(global.b))`),
			),
			want: hcldoc(
				expr("var", `"hello" + "world"`),
			),
		},
		{
			name: "basic minus expression",
			config: hcldoc(
				expr("var", `1 + 1`),
			),
			want: hcldoc(
				expr("var", `1 + 1`),
			),
		},
		{
			name: "conditional expression",
			config: hcldoc(
				expr("var", `1 == 1 ? 0 : 1`),
			),
			want: hcldoc(
				expr("var", `1 == 1 ? 0 : 1`),
			),
		},
		{
			name: "conditional expression 2",
			globals: hcldoc(
				globals(
					number("num", 10),
				),
			),
			config: hcldoc(
				expr("var", `1 >= global.num ? local.x : [for x in local.a : x]`),
			),
			want: hcldoc(
				expr("var", `1 >= 10 ? local.x : [for x in local.a : x]`),
			),
		},
		{
			name: "operation + conditional expression",
			globals: hcldoc(
				globals(
					number("num", 10),
				),
			),
			config: hcldoc(
				expr("var", `local.x + 1 >= global.num ? local.x : [for x in local.a : x]`),
			),
			want: hcldoc(
				expr("var", `local.x + 1 >= 10 ? local.x : [for x in local.a : x]`),
			),
		},
		{
			name: "deep object interpolation",
			globals: hcldoc(
				globals(
					expr("obj", `{
						obj2 = {
							obj3 = {
								string = "hello"
								number = 1337
								bool = false
							}
						}
						string = "hello"
						number = 1337
						bool = false
					}`),
				),
			),
			config: hcldoc(
				str("var", "${global.obj.string} ${global.obj.obj2.obj3.number}"),
			),
			want: hcldoc(
				str("var", "hello 1337"),
			),
		},
		{
			name: "deep object interpolation of object field and str field fails",
			globals: hcldoc(
				globals(
					expr("obj", `{
						obj2 = {
							obj3 = {
								string = "hello"
								number = 1337
								bool = false
							}
						}
						string = "hello"
						number = 1337
						bool = false
					}`),
				),
			),
			config: hcldoc(
				str("var", "${global.obj.string} ${global.obj.obj2.obj3}"),
			),
			wantErr: eval.ErrInterpolationEval,
		},
		{
			name: "basic list indexing",
			globals: hcldoc(
				globals(
					expr("list", `["a", "b", "c"]`),
				),
			),
			config: hcldoc(
				expr("string", `global.list[0]`),
			),
			want: hcldoc(
				str("string", "a"),
			),
		},
		{
			name: "advanced list indexing",
			skip: true,
			globals: hcldoc(
				globals(
					expr("list", `[ [1, 2, 3], [4, 5, 6], [7, 8, 9]]`),
				),
			),
			config: hcldoc(
				expr("num", `global.list[1][1]`),
			),
			want: hcldoc(
				number("num", 5),
			),
		},
		{
			name: "advanced list indexing 2",
			skip: true,
			globals: hcldoc(
				globals(
					expr("list", `[ [1, 2, 3], [4, 5, 6], [7, 8, 9]]`),
				),
			),
			config: hcldoc(
				expr("num", `global.list[1+1][1-1]`),
			),
			want: hcldoc(
				number("num", 7),
			),
		},
		{
			name: "advanced object indexing",
			skip: true,
			globals: hcldoc(
				globals(
					expr("obj", `{A = {B = "test"}}`),
				),
			),
			config: hcldoc(
				expr("string", `global.list[tm_upper("a")][tm_upper("b)]`),
			),
			want: hcldoc(
				str("string", "test"),
			),
		},
		{
			name: "basic object indexing",
			globals: hcldoc(
				globals(
					expr("obj", `{"a" = "b"}`),
				),
			),
			config: hcldoc(
				expr("string", `global.obj["a"]`),
			),
			want: hcldoc(
				str("string", "b"),
			),
		},
		{
			name: "obj for loop without eval references",
			config: hcldoc(
				expr("obj", `{for k in local.list : k => k}`),
			),
			want: hcldoc(
				expr("obj", `{for k in local.list : k => k}`),
			),
		},
		{
			name: "list for loop without eval references",
			config: hcldoc(
				expr("obj", `[for k in local.list : k]`),
			),
			want: hcldoc(
				expr("obj", `[for k in local.list : k]`),
			),
		},
		{
			name: "{for loop from map and funcall",
			config: hcldoc(
				expr("obj", `{for s in var.list : s => upper(s)}`),
			),
			want: hcldoc(
				expr("obj", `{for s in var.list : s => upper(s)}`),
			),
		},
		{
			name: "{for in from {for map",
			config: hcldoc(
				expr("obj", `{for k, v in {for k,v in a.b : k=>v} : k => v}`),
			),
			want: hcldoc(
				expr("obj", `{for k, v in {for k,v in a.b : k=>v} : k => v}`),
			),
		},
		{
			name: "[for with funcall",
			config: hcldoc(
				expr("obj", `[for s in var.list : upper(s)]`),
			),
			want: hcldoc(
				expr("obj", `[for s in var.list : upper(s)]`),
			),
		},
		{
			name: "[for in from map and Operation body",
			config: hcldoc(
				expr("obj", `[for k, v in var.map : length(k) + length(v)]`),
			),
			want: hcldoc(
				expr("obj", `[for k, v in var.map : length(k) + length(v)]`),
			),
		},
		{
			name: "[for in from map and interpolation body",
			config: hcldoc(
				expr("obj", `[for i, v in var.list : "${i} is ${v}"]`),
			),
			want: hcldoc(
				expr("obj", `[for i, v in var.list : "${i} is ${v}"]`),
			),
		},
		{
			name: "[for in from map with conditional body",
			config: hcldoc(
				expr("obj", `[for s in var.list : upper(s) if s != ""]`),
			),
			want: hcldoc(
				expr("obj", `[for s in var.list : upper(s) if s != ""]`),
			),
		},
		{
			name: "[for in from [for list",
			config: hcldoc(
				expr("obj", `[for s in [for s in a.b : s] : s]`),
			),
			want: hcldoc(
				expr("obj", `[for s in [for s in a.b : s] : s]`),
			),
		},
		{
			name: "list for loop with global reference fails",
			globals: globals(
				expr("list", `["a", "b", "c"]`),
			),
			config: hcldoc(
				expr("list", `[for k in global.list : k]`),
			),
			wantErr: eval.ErrForExprDisallowEval,
		},
		{
			name: "obj for loop with global reference fails",
			globals: globals(
				expr("obj", `{ a = 1}`),
			),
			config: hcldoc(
				expr("obj", `[for k in global.obj : k]`),
			),
			wantErr: eval.ErrForExprDisallowEval,
		},
		{
			name: "[for in from [for list with global references",
			globals: globals(
				expr("list", `["a", "b", "c"]`),
			),
			config: hcldoc(
				expr("obj", `[for s in [for s in global.list : s] : s]`),
			),
			wantErr: eval.ErrForExprDisallowEval,
		},
		{
			name: "mixing {for and [for",
			config: hcldoc(
				expr("obj", `{for k, v in [for k in a.b : k] : k => v}`),
			),
			want: hcldoc(
				expr("obj", `{for k, v in [for k in a.b : k] : k => v}`),
			),
		},
		{
			name: "unary operation !",
			config: hcldoc(
				expr("num", "!0"),
			),
			want: hcldoc(
				expr("num", "!0"),
			),
		},
		{
			name: "unary operation -",
			config: hcldoc(
				expr("num", "-0"),
			),
			want: hcldoc(
				expr("num", "-0"),
			),
		},
		{
			name: "number indexing",
			config: hcldoc(
				expr("a", "b.1000"),
			),
			want: hcldoc(
				expr("a", "b.1000"),
			),
		},
		{
			name: "advanced number literal",
			config: hcldoc(
				expr("a", "10.1200"),
			),
			want: hcldoc(
				expr("a", "10.1200"),
			),
		},
		{
			name: "advanced number literal",
			config: hcldoc(
				expr("a", "0.0.A.0"),
			),
			want: hcldoc(
				expr("a", "0.0.A.0"),
			),
		},
		{
			name: "parenthesis and splat with newlines",
			config: hcldoc(
				expr("a", "(A(). \n*)"),
			),
			want: hcldoc(
				expr("a", "(A(). \n*)"),
			),
		},
		{
			name: "funcall and newlines/comments",
			config: hcldoc(
				expr("a", "funcall(\n/**/a\n/**/,/**/b/**/\n/**/)"),
			),
			want: hcldoc(
				expr("a", "funcall(\n/**/a\n/**/,/**/b/**/\n/**/)"),
			),
		},
		{
			name: "objects and newlines/comments",
			config: hcldoc(
				expr("a", "{/**/\n/**/a/**/=/**/\"a\"/**/\n}"),
			),
			want: hcldoc(
				expr("a", "{/**/\n/**/a/**/=/**/\"a\"/**/\n}"),
			),
		},
		{
			name: "lists and newlines/comments",
			config: hcldoc(
				expr("a", "[/**/\n/**/a/**/\n,\"a\"/**/\n]"),
			),
			want: hcldoc(
				expr("a", "[/**/\n/**/a/**/\n,\"a\"/**/\n]"),
			),
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			const (
				stackname = "stack"
				genname   = "test"
			)

			if tcase.skip {
				t.Skip()
			}

			s := sandbox.New(t)
			stackEntry := s.CreateStack(stackname)
			stack := stackEntry.Load()
			path := filepath.Join(s.RootDir(), stackname)
			if tcase.globals == nil {
				tcase.globals = globals()
			}
			cfg := hcldoc(
				tcase.globals,
				generateHCL(
					labels(genname),
					content(
						tcase.config,
					),
				),
			)

			t.Logf("input: %s", cfg.String())
			test.AppendFile(t, path, config.DefaultFilename, cfg.String())

			meta := stack.Meta()
			globals := s.LoadStackGlobals(meta)
			res, err := genhcl.Load(s.RootDir(), meta, globals)
			assert.IsError(t, err, tcase.wantErr)

			if err != nil {
				return
			}

			got := res.GeneratedHCLs()

			assert.EqualInts(t, len(got), 1, "want single generated HCL")

			gothcl := got[genname]
			gotcode := gothcl.String()
			wantcode := tcase.want.String()
			assertHCLEquals(t, gotcode, wantcode)
		})
	}
}

func assertHCLEquals(t *testing.T, got string, want string) {
	t.Helper()

	const trimmedChars = "\n "

	got = strings.Trim(got, trimmedChars)
	want = strings.Trim(want, trimmedChars)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("generated code doesn't match expectation")
		t.Errorf("want:\n%q", want)
		t.Errorf("got:\n%q", got)
		t.Fatalf("diff:\n%s", diff)
	}
}

func init() {
	zerolog.SetGlobalLevel(zerolog.Disabled)
}
