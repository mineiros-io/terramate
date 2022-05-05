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

package stack_test

import (
	"path/filepath"
	"testing"

	"github.com/madlambda/spells/assert"
	"github.com/mineiros-io/terramate"
	"github.com/mineiros-io/terramate/config"
	"github.com/mineiros-io/terramate/errors"
	"github.com/mineiros-io/terramate/hcl"
	"github.com/mineiros-io/terramate/stack"
	"github.com/mineiros-io/terramate/test"
	errtest "github.com/mineiros-io/terramate/test/errors"
	"github.com/mineiros-io/terramate/test/hclwrite"
	"github.com/mineiros-io/terramate/test/sandbox"
	"github.com/zclconf/go-cty-debug/ctydebug"
)

// TODO(katcipis): add tests related to tf functions that depend on filesystem
// (BaseDir parameter passed on Scope when creating eval context).

func TestLoadGlobals(t *testing.T) {
	type (
		hclconfig struct {
			path     string
			filename string
			add      *hclwrite.Block
		}
		testcase struct {
			name    string
			layout  []string
			configs []hclconfig
			want    map[string]*hclwrite.Block
			wantErr error
		}
	)

	labels := func(labels ...string) hclwrite.BlockBuilder {
		return hclwrite.Labels(labels...)
	}
	block := func(name string, builders ...hclwrite.BlockBuilder) *hclwrite.Block {
		return hclwrite.BuildBlock(name, builders...)
	}
	globals := func(builders ...hclwrite.BlockBuilder) *hclwrite.Block {
		return block("globals", builders...)
	}
	expr := hclwrite.Expression
	attr := func(name, expr string) hclwrite.BlockBuilder {
		return hclwrite.AttributeValue(t, name, expr)
	}
	str := hclwrite.String
	number := hclwrite.NumberInt
	boolean := hclwrite.Boolean

	tcases := []testcase{
		{
			name:   "no stacks no globals",
			layout: []string{},
		},
		{
			name:   "single stacks no globals",
			layout: []string{"s:stack"},
		},
		{
			name: "two stacks no globals",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2",
			},
		},
		{
			name: "non-global block is ignored",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2",
			},
			configs: []hclconfig{
				{
					path: "/",
					add:  block("terramate"),
				},
			},
		},
		{
			name:   "single stack with its own globals",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						str("some_string", "string"),
						number("some_number", 777),
						boolean("some_bool", true),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(
					str("some_string", "string"),
					number("some_number", 777),
					boolean("some_bool", true),
				),
			},
		},
		{
			name:   "single stack with three globals blocks",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{path: "/stack", add: globals(str("str", "hi"))},
				{path: "/stack", add: globals(number("num", 666))},
				{path: "/stack", add: globals(boolean("bool", false))},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(
					str("str", "hi"),
					number("num", 666),
					boolean("bool", false),
				),
			},
		},
		{
			name: "multiple stacks with config on parent dir",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2",
			},
			configs: []hclconfig{
				{path: "/stacks", add: globals(str("parent", "hi"))},
			},
			want: map[string]*hclwrite.Block{
				"/stacks/stack-1": globals(str("parent", "hi")),
				"/stacks/stack-2": globals(str("parent", "hi")),
			},
		},
		{
			name: "multiple stacks with config on root dir",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2",
			},
			configs: []hclconfig{
				{path: "/", add: globals(str("root", "hi"))},
			},
			want: map[string]*hclwrite.Block{
				"/stacks/stack-1": globals(str("root", "hi")),
				"/stacks/stack-2": globals(str("root", "hi")),
			},
		},
		{
			name: "multiple stacks merging no overriding",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2",
			},
			configs: []hclconfig{
				{path: "/", add: globals(str("root", "root"))},
				{path: "/stacks", add: globals(boolean("parent", true))},
				{path: "/stacks/stack-1", add: globals(number("stack", 666))},
				{path: "/stacks/stack-2", add: globals(number("stack", 777))},
			},
			want: map[string]*hclwrite.Block{
				"/stacks/stack-1": globals(
					str("root", "root"),
					boolean("parent", true),
					number("stack", 666),
				),
				"/stacks/stack-2": globals(
					str("root", "root"),
					boolean("parent", true),
					number("stack", 777),
				),
			},
		},
		{
			name: "multiple stacks merging with overriding",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2",
				"s:stacks/stack-3",
			},
			configs: []hclconfig{
				{
					path: "/",
					add: globals(
						str("field_a", "field_a_root"),
						str("field_b", "field_b_root"),
					),
				},
				{
					path: "/stacks",
					add: globals(
						str("field_b", "field_b_stacks"),
						str("field_c", "field_c_stacks"),
						str("field_d", "field_d_stacks"),
					),
				},
				{
					path: "/stacks/stack-1",
					add: globals(
						str("field_a", "field_a_stack_1"),
						str("field_b", "field_b_stack_1"),
						str("field_c", "field_c_stack_1"),
					),
				},
				{
					path: "/stacks/stack-2",
					add: globals(
						str("field_d", "field_d_stack_2"),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stacks/stack-1": globals(
					str("field_a", "field_a_stack_1"),
					str("field_b", "field_b_stack_1"),
					str("field_c", "field_c_stack_1"),
					str("field_d", "field_d_stacks"),
				),
				"/stacks/stack-2": globals(
					str("field_a", "field_a_root"),
					str("field_b", "field_b_stacks"),
					str("field_c", "field_c_stacks"),
					str("field_d", "field_d_stack_2"),
				),
				"/stacks/stack-3": globals(
					str("field_a", "field_a_root"),
					str("field_b", "field_b_stacks"),
					str("field_c", "field_c_stacks"),
					str("field_d", "field_d_stacks"),
				),
			},
		},
		{
			name: "stacks referencing metadata",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2:description=someDescriptionStack2",
			},
			configs: []hclconfig{
				{
					path: "/stacks/stack-1",
					add: globals(
						expr("stack_path", "terramate.path"),
						expr("interpolated", `"prefix-${terramate.name}-suffix"`),
						expr("stack_description", "terramate.description"),
					),
				},
				{
					path: "/stacks/stack-2",
					add: globals(
						expr("stack_path", "terramate.path"),
						expr("stack_description", "terramate.description"),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stacks/stack-1": globals(
					str("stack_path", "/stacks/stack-1"),
					str("interpolated", "prefix-stack-1-suffix"),
					str("stack_description", ""),
				),
				"/stacks/stack-2": globals(
					str("stack_path", "/stacks/stack-2"),
					str("stack_description", "someDescriptionStack2"),
				),
			},
		},
		{
			name: "stacks using functions and metadata",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2",
			},
			configs: []hclconfig{
				{
					path: "/stacks/stack-1",
					add: globals(
						expr("interpolated", `"prefix-${tm_replace(terramate.path, "/", "@")}-suffix"`),
					),
				},
				{
					path: "/stacks/stack-2",
					add: globals(
						expr("stack_path", `tm_replace(terramate.path, "/", "-")`),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stacks/stack-1": globals(
					str("interpolated", "prefix-@stacks@stack-1-suffix"),
				),
				"/stacks/stack-2": globals(str("stack_path", "-stacks-stack-2")),
			},
		},
		{
			name:   "stack with globals referencing globals",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						str("field", "some-string"),
						expr("stack_path", "terramate.path"),
						expr("ref_field", "global.field"),
						expr("ref_stack_path", "global.stack_path"),
						expr("interpolation", `"${global.ref_stack_path}-${global.ref_field}"`),
						expr("ref_interpolation", "global.interpolation"),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(
					str("field", "some-string"),
					str("stack_path", "/stack"),
					str("ref_field", "some-string"),
					str("ref_stack_path", "/stack"),
					str("interpolation", "/stack-some-string"),
					str("ref_interpolation", "/stack-some-string"),
				),
			},
		},
		{
			name:   "stack with globals referencing globals on multiple files",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path:     "/stack",
					filename: "globals_1.tm.hcl",
					add: globals(
						str("field", "some-string"),
						expr("stack_path", "terramate.path"),
					),
				},
				{
					path:     "/stack",
					filename: "globals_2.tm.hcl",
					add: globals(
						expr("ref_field", "global.field"),
						expr("ref_stack_path", "global.stack_path"),
					),
				},
				{
					path:     "/stack",
					filename: "globals_3.tm.hcl",
					add: globals(
						expr("interpolation", `"${global.ref_stack_path}-${global.ref_field}"`),
						expr("ref_interpolation", "global.interpolation"),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(
					str("field", "some-string"),
					str("stack_path", "/stack"),
					str("ref_field", "some-string"),
					str("ref_stack_path", "/stack"),
					str("interpolation", "/stack-some-string"),
					str("ref_interpolation", "/stack-some-string"),
				),
			},
		},
		{
			name: "root with globals referencing globals on multiple files",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2",
			},
			configs: []hclconfig{
				{
					path:     "/",
					filename: "globals_1.tm.hcl",
					add: globals(
						str("field", "some-string"),
						expr("stack_path", "terramate.path"),
					),
				},
				{
					path:     "/",
					filename: "globals_2.tm.hcl",
					add: globals(
						expr("ref_field", "global.field"),
						expr("ref_stack_path", "global.stack_path"),
					),
				},
				{
					path:     "/",
					filename: "globals_3.tm.hcl",
					add: globals(
						expr("interpolation", `"${global.ref_stack_path}-${global.ref_field}"`),
						expr("ref_interpolation", "global.interpolation"),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stacks/stack-1": globals(
					str("field", "some-string"),
					str("stack_path", "/stacks/stack-1"),
					str("ref_field", "some-string"),
					str("ref_stack_path", "/stacks/stack-1"),
					str("interpolation", "/stacks/stack-1-some-string"),
					str("ref_interpolation", "/stacks/stack-1-some-string"),
				),
				"/stacks/stack-2": globals(
					str("field", "some-string"),
					str("stack_path", "/stacks/stack-2"),
					str("ref_field", "some-string"),
					str("ref_stack_path", "/stacks/stack-2"),
					str("interpolation", "/stacks/stack-2-some-string"),
					str("ref_interpolation", "/stacks/stack-2-some-string"),
				),
			},
		},
		{
			name:   "stack with globals referencing globals hierarchically no overriding",
			layout: []string{"s:envs/prod/stacks/stack"},
			configs: []hclconfig{
				{
					path: "/",
					add: globals(
						str("root_field", "root-data"),
						number("root_number", 666),
						boolean("root_bool", true),
						expr("root_stack_ref", "global.stack_inter"),
					),
				},
				{
					path: "/envs",
					add: globals(
						expr("env_metadata", "terramate.path"),
						expr("env_root_ref", "global.root_field"),
					),
				},
				{
					path: "/envs/prod",
					add:  globals(str("env", "prod")),
				},
				{
					path: "/envs/prod/stacks",
					add: globals(
						expr("stacks_field", `"${terramate.name}-${global.env}"`),
					),
				},
				{
					path: "/envs/prod/stacks/stack",
					add: globals(
						expr("stack_inter", `"${global.root_field}-${global.env}-${global.stacks_field}"`),
						expr("stack_bool", "global.root_bool"),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/envs/prod/stacks/stack": globals(
					str("root_field", "root-data"),
					number("root_number", 666),
					boolean("root_bool", true),
					str("root_stack_ref", "root-data-prod-stack-prod"),
					str("env_metadata", "/envs/prod/stacks/stack"),
					str("env_root_ref", "root-data"),
					str("env", "prod"),
					str("stacks_field", "stack-prod"),
					str("stack_inter", "root-data-prod-stack-prod"),
					boolean("stack_bool", true),
				),
			},
		},
		{
			name: "stack with globals referencing globals hierarchically and overriding",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2",
			},
			configs: []hclconfig{
				{
					path: "/",
					add: globals(
						expr("stack_ref", "global.stack"),
					),
				},
				{
					path: "/stacks",
					add: globals(
						expr("stack_ref", "global.stack_other"),
					),
				},
				{
					path: "/stacks/stack-1",
					add: globals(
						str("stack", "stack-1"),
						str("stack_other", "other stack-1"),
					),
				},
				{
					path: "/stacks/stack-2",
					add: globals(
						str("stack", "stack-2"),
						str("stack_other", "other stack-2"),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stacks/stack-1": globals(
					str("stack", "stack-1"),
					str("stack_other", "other stack-1"),
					str("stack_ref", "other stack-1"),
				),
				"/stacks/stack-2": globals(
					str("stack", "stack-2"),
					str("stack_other", "other stack-2"),
					str("stack_ref", "other stack-2"),
				),
			},
		},
		{
			name: "globals hierarchically defined with different filenames",
			layout: []string{
				"s:stacks/stack-1",
				"s:stacks/stack-2",
			},
			configs: []hclconfig{
				{
					path:     "/",
					filename: "root_globals.tm",
					add: globals(
						expr("stack_ref", "global.stack"),
					),
				},
				{
					path:     "/stacks",
					filename: "stacks_globals.tm.hcl",
					add: globals(
						expr("stack_ref", "global.stack_other"),
					),
				},
				{
					path:     "/stacks/stack-1",
					filename: "stack_1_globals.tm",
					add: globals(
						str("stack", "stack-1"),
						str("stack_other", "other stack-1"),
					),
				},
				{
					path:     "/stacks/stack-2",
					filename: "stack_2_globals.tm.hcl",
					add: globals(
						str("stack", "stack-2"),
						str("stack_other", "other stack-2"),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stacks/stack-1": globals(
					str("stack", "stack-1"),
					str("stack_other", "other stack-1"),
					str("stack_ref", "other stack-1"),
				),
				"/stacks/stack-2": globals(
					str("stack", "stack-2"),
					str("stack_other", "other stack-2"),
					str("stack_ref", "other stack-2"),
				),
			},
		},
		{
			name:   "unknown global reference is ignored if it is overridden",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/",
					add:  globals(expr("field", "global.wont_exist")),
				},
				{
					path: "/stack",
					add:  globals(str("field", "data")),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(str("field", "data")),
			},
		},
		{
			name:   "global reference with functions",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/",
					add:  globals(str("field", "@lala@hello")),
				},
				{
					path: "/stack",
					add: globals(
						expr("newfield", `tm_replace(global.field, "@", "/")`),
						expr("splitfun", `tm_split("@", global.field)[1]`),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(
					str("field", "@lala@hello"),
					str("newfield", "/lala/hello"),
					str("splitfun", "lala"),
				),
			},
		},
		{
			name:   "global reference with successful try on stack",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						attr("team", `{ members = ["aaa"] }`),
						expr("members", "global.team.members"),
						expr("members_try", `tm_try(global.team.members, [])`),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(
					attr("team", `{ members = ["aaa"] }`),
					attr("members", `["aaa"]`),
					attr("members_try", `["aaa"]`),
				),
			},
		},
		{
			name:   "global reference with failed try on stack",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						attr("team", `{ members = ["aaa"] }`),
						expr("members_try", `tm_try(global.team.mistake, [])`),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(
					attr("team", `{ members = ["aaa"] }`),
					attr("members_try", "[]"),
				),
			},
		},
		{
			name:   "global interpolating strings",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						str("str1", "hello"),
						str("str2", "world"),
						str("str3", "${global.str1}-${global.str2}"),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(
					str("str1", "hello"),
					str("str2", "world"),
					str("str3", "hello-world"),
				),
			},
		},
		{
			// This tests double check that interpolation on a single list
			// produces an actual list object on hcl eval, not a string
			// Which is bizarre...but why not ?
			name:   "global interpolating single list",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						attr("a", `["aaa"]`),
						str("a_interpolated", "${global.a}"),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(
					attr("a", `["aaa"]`),
					attr("a_interpolated", `["aaa"]`),
				),
			},
		},
		{
			name:   "global interpolating of single number",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						number("a", 1),
						str("a_interpolated", "${global.a}"),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(
					number("a", 1),
					number("a_interpolated", 1),
				),
			},
		},
		{
			name:   "global interpolating of single boolean",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						boolean("a", true),
						str("a_interpolated", "${global.a}"),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(
					boolean("a", true),
					boolean("a_interpolated", true),
				),
			},
		},
		{
			name:   "global interpolating multiple lists fails",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						attr("a", `["aaa"]`),
						str("a_interpolated", "${global.a}-${global.a}"),
					),
				},
			},
			wantErr: errors.E(stack.ErrGlobalEval),
		},
		{
			name:   "global interpolating list with space fails",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						attr("a", `["aaa"]`),
						str("a_interpolated", " ${global.a}"),
					),
				},
			},
			wantErr: errors.E(stack.ErrGlobalEval),
		},
		{
			// This tests double check that interpolation on a single object/map
			// produces an actual object on hcl eval, not a string.
			// Which is bizarre...but why not ?
			name:   "global interpolating single object",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						attr("a", `{ members = ["aaa"] }`),
						str("a_interpolated", "${global.a}"),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(
					attr("a", `{ members = ["aaa"] }`),
					attr("a_interpolated", `{ members = ["aaa"] }`),
				),
			},
		},
		{
			name:   "global interpolating multiple objects fails",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						attr("a", `{ members = ["aaa"] }`),
						str("a_interpolated", "${global.a}-${global.a}"),
					),
				},
			},
			wantErr: errors.E(stack.ErrGlobalEval),
		},
		{
			name:   "global interpolating object with space fails",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						attr("a", `{ members = ["aaa"] }`),
						str("a_interpolated", "${global.a} "),
					),
				},
			},
			wantErr: errors.E(stack.ErrGlobalEval),
		},
		{
			// This tests double check that interpolation on a single number
			// produces an actual number on hcl eval, not a string.
			// Which is bizarre...but why not ?
			name:   "global interpolating numbers",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						number("a", 666),
						str("a_interpolated", "${global.a}"),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(
					number("a", 666),
					number("a_interpolated", 666),
				),
			},
		},
		{
			// Composing numbers on a interpolation works and then produces
			// string. Testing this because this does not work with all types
			// and it is useful for us as maintainers to map/test these different behaviors.
			name:   "global interpolating multiple numbers",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						number("a", 666),
						str("a_interpolated", "${global.a}-${global.a}"),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(
					number("a", 666),
					str("a_interpolated", "666-666"),
				),
			},
		},
		{
			// This tests double check that interpolation on a single boolean
			// produces an actual boolean on hcl eval, not a string.
			// Which is bizarre...but why not ?
			name:   "global interpolating numbers",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						boolean("a", true),
						str("a_interpolated", "${global.a}"),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(
					boolean("a", true),
					boolean("a_interpolated", true),
				),
			},
		},
		{
			// Composing booleans on a interpolation works and then produces
			// string. Testing this because this does not work with all types
			// and it is useful for us as maintainers to map/test these different behaviors.
			name:   "global interpolating multiple numbers",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						boolean("a", false),
						str("a_interpolated", "${global.a}-${global.a}"),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(
					boolean("a", false),
					str("a_interpolated", "false-false"),
				),
			},
		},
		{
			name:   "global reference with try on root config and value defined on stack",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/",
					add: globals(
						expr("team_def", "global.team.def"),
						expr("team_def_try", `tm_try(global.team.def, {})`),
					),
				},
				{
					path: "/stack",
					add: globals(
						attr("team", `{ def = { name = "awesome" } }`),
					),
				},
			},
			want: map[string]*hclwrite.Block{
				"/stack": globals(
					attr("team", `{ def = { name = "awesome" } }`),
					attr("team_def", `{ name = "awesome" }`),
					attr("team_def_try", `{ name = "awesome" }`),
				),
			},
		},
		{
			name:   "globals cant have blocks inside",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/",
					add: globals(
						str("test", "hallo"),
						block("notallowed"),
					),
				},
			},
			wantErr: errors.E(hcl.ErrTerramateSchema),
		},
		{
			name:   "globals cant have labels",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/",
					add: globals(
						labels("no"),
						str("test", "hallo"),
					),
				},
			},
			wantErr: errors.E(hcl.ErrTerramateSchema),
		},
		{
			name:   "global undefined reference on root",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/",
					add:  globals(expr("field", "global.unknown")),
				},
				{
					path: "/stack",
					add:  globals(str("stack", "whatever")),
				},
			},
			wantErr: errors.E(stack.ErrGlobalEval),
		},
		{
			name:   "global undefined reference on stack",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add:  globals(expr("field", "global.unknown")),
				},
			},
			wantErr: errors.E(stack.ErrGlobalEval),
		},
		{
			name:   "global undefined references mixed on stack",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						expr("field_a", "global.unknown"),
						expr("field_b", "global.unknown_again"),
						str("valid", "valid"),
						expr("field_c", "global.oopsie"),
					),
				},
			},
			wantErr: errors.E(stack.ErrGlobalEval),
		},
		{
			name:   "global cyclic reference on stack",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path: "/stack",
					add: globals(
						expr("a", "global.b"),
						expr("b", "global.c"),
						expr("c", "global.a"),
					),
				},
			},
			wantErr: errors.E(stack.ErrGlobalEval),
		},
		{
			name:   "global cyclic references across hierarchy",
			layout: []string{"s:stacks/stack"},
			configs: []hclconfig{
				{
					path: "/",
					add:  globals(expr("a", "global.b")),
				},
				{
					path: "/stacks",
					add:  globals(expr("b", "global.c")),
				},
				{
					path: "/stacks/stack",
					add:  globals(expr("c", "global.a")),
				},
			},
			wantErr: errors.E(stack.ErrGlobalEval),
		},
		{
			name:   "global redefined on different file on stack",
			layout: []string{"s:stack"},
			configs: []hclconfig{
				{
					path:     "/stack",
					filename: "globals.tm.hcl",
					add:      globals(str("a", "a")),
				},
				{
					path:     "/stack",
					filename: "globals2.tm.hcl",
					add:      globals(str("a", "b")),
				},
			},
			wantErr: errors.E(stack.ErrGlobalRedefined),
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			s := sandbox.New(t)
			s.BuildTree(tcase.layout)

			for _, globalBlock := range tcase.configs {
				path := filepath.Join(s.RootDir(), globalBlock.path)
				filename := config.DefaultFilename
				if globalBlock.filename != "" {
					filename = globalBlock.filename
				}
				test.AppendFile(t, path, filename, globalBlock.add.String())
			}

			wantGlobals := tcase.want

			stackEntries, err := terramate.ListStacks(s.StackLoader(), s.RootDir())
			if err != nil {
				errtest.AssertKind(t, err, tcase.wantErr)
			}

			var stacks []stack.S
			for _, entry := range stackEntries {
				st := entry.Stack
				stacks = append(stacks, st)

				got, err := stack.LoadGlobals(s.RootDir(), st)

				errtest.Assert(t, err, tcase.wantErr)
				if tcase.wantErr != nil {
					continue
				}

				want, ok := wantGlobals[st.Path()]
				if !ok {
					want = globals()
				}
				delete(wantGlobals, st.Path())

				// Could have one type for globals configs and another type
				// for wanted evaluated globals, but that would make
				// globals building more annoying (two sets of functions).
				if want.HasExpressions() {
					t.Fatal("wanted globals definition contains expressions, they should be defined only by evaluated values")
					t.Errorf("wanted globals definition:\n%s\n", want)
				}

				gotAttrs := got.Attributes()
				wantAttrs := want.AttributesValues()

				if len(gotAttrs) != len(wantAttrs) {
					t.Errorf("got %d global attributes; wanted %d", len(gotAttrs), len(wantAttrs))
				}

				for name, wantVal := range wantAttrs {
					gotVal, ok := gotAttrs[name]
					if !ok {
						t.Errorf("wanted global.%s is missing", name)
						continue
					}
					if diff := ctydebug.DiffValues(wantVal, gotVal); diff != "" {
						t.Errorf("global.%s doesn't match expectation", name)
						t.Errorf("want: %s", ctydebug.ValueString(wantVal))
						t.Errorf("got: %s", ctydebug.ValueString(gotVal))
						t.Errorf("diff:\n%s", diff)
					}
				}
			}

			if len(wantGlobals) > 0 {
				t.Fatalf("wanted stack globals: %v that was not found on stacks: %v", wantGlobals, stacks)
			}
		})
	}
}

func TestLoadGlobalsErrors(t *testing.T) {
	type (
		cfg struct {
			path string
			body string
		}
		testcase struct {
			name    string
			layout  []string
			configs []cfg
			want    error
		}
	)

	// These test scenarios where quite hard to describe with the
	// core test fixture (core model doesn't allow duplicated fields
	// by nature, and it never creates malformed global blocks),
	// hence this separate error tests exists :-).

	tcases := []testcase{
		{
			name:   "stack config has invalid global definition",
			layout: []string{"s:stack"},
			configs: []cfg{
				{
					path: "/stack",
					body: `
					  globals {
					    a = "hi"
					`,
				},
			},
			want: errors.E(hcl.ErrHCLSyntax),
		},
		{
			name:   "root config has invalid global definition",
			layout: []string{"s:stack"},
			configs: []cfg{
				{
					path: "/",
					body: `
					  globals {
					    a = "hi"
					`,
				},
			},
			want: errors.E(hcl.ErrHCLSyntax),
		},
		{
			name:   "stack config has global redefinition on single block",
			layout: []string{"s:stack"},
			configs: []cfg{
				{
					path: "/stack",
					body: `
					  globals {
					    a = "hi"
					    a = 5
					  }
					`,
				},
			},
			// FIXME(katcipis): would be better to have ErrGlobalRedefined
			// for now we get an error directly from hcl for this.
			want: errors.E(hcl.ErrHCLSyntax),
		},
		{
			name:   "root config has global redefinition on single block",
			layout: []string{"s:stack"},
			configs: []cfg{
				{
					path: "/",
					body: `
					  globals {
					    a = "hi"
					    a = 5
					  }
					`,
				},
			},
			// FIXME(katcipis): would be better to have ErrGlobalRedefined
			// for now we get an error directly from hcl for this.
			want: errors.E(hcl.ErrHCLSyntax),
		},
		{
			name:   "stack config has global redefinition on multiple blocks",
			layout: []string{"s:stack"},
			configs: []cfg{
				{
					path: "/stack",
					body: `
					  globals {
					    a = "hi"
					  }
					  globals {
					    a = 5
					  }
					  globals {
					    a = true
					  }
					`,
				},
			},
			want: errors.E(stack.ErrGlobalRedefined),
		},
		{
			name:   "root config has global redefinition on multiple blocks",
			layout: []string{"s:stack"},
			configs: []cfg{
				{
					path: "/",
					body: `
					  globals {
					    a = "hi"
					  }
					  globals {
					    a = 5
					  }
					`,
				},
			},
			want: errors.E(stack.ErrGlobalRedefined),
		},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			s := sandbox.New(t)
			s.BuildTree(tcase.layout)

			for _, c := range tcase.configs {
				path := filepath.Join(s.RootDir(), c.path)
				test.AppendFile(t, path, config.DefaultFilename, c.body)
			}

			stackEntries, err := terramate.ListStacks(s.StackLoader(), s.RootDir())
			// TODO(i4k): this better not be tested here.
			if errors.IsKind(tcase.want, hcl.ErrHCLSyntax) {
				errtest.AssertKind(t, err, tcase.want)
			}

			for _, entry := range stackEntries {
				_, err := stack.LoadGlobals(s.RootDir(), entry.Stack)
				errtest.Assert(t, err, tcase.want)
			}
		})
	}
}

func TestLoadGlobalsErrorOnRelativeDir(t *testing.T) {
	s := sandbox.New(t)
	s.BuildTree([]string{"s:stack"})

	rel, err := filepath.Rel(test.Getwd(t), s.RootDir())
	assert.NoError(t, err)

	stacks := s.LoadStacks()
	assert.EqualInts(t, 1, len(stacks))
	globals, err := stack.LoadGlobals(rel, stacks[0])
	assert.Error(t, err, "got %v instead of error", globals)
}
