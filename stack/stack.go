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

package stack

import (
	"path/filepath"
	"sort"

	"github.com/mineiros-io/terramate/hcl"
	"github.com/mineiros-io/terramate/project"
	"github.com/zclconf/go-cty/cty"
)

// S represents a stack
type S struct {
	// hostpath is the file system absolute path of the stack.
	hostpath string

	// path is the absolute path of the stack relative to project's root.
	path string

	// name of the stack.
	name string

	// desc is the description of the stack.
	desc string

	// after is a list of stack paths that must run before this stack.
	after []string

	// before is a list of stack paths that must run after this stack.
	before []string

	// wants is the list of stacks that must be selected whenever this stack
	// is selected.
	wants []string

	// changed tells if this is a changed stack.
	changed bool
}

// Metadata has all metadata loaded per stack
type Metadata interface {
	Name() string
	Path() string
	Desc() string
}

// New creates a new stack from configuration cfg.
func New(root string, cfg hcl.Config) S {
	name := cfg.Stack.Name
	if name == "" {
		name = filepath.Base(cfg.AbsDir())
	}

	return S{
		name:     name,
		desc:     cfg.Stack.Description,
		after:    cfg.Stack.After,
		before:   cfg.Stack.Before,
		wants:    cfg.Stack.Wants,
		hostpath: cfg.AbsDir(),
		path:     project.PrjAbsPath(root, cfg.AbsDir()),
	}
}

// Name of the stack.
func (s S) Name() string {
	if s.name != "" {
		return s.name
	}
	return s.Path()
}

// Desc is the description of the stack.
func (s S) Desc() string { return s.desc }

// After specifies the list of stacks that must run before this stack.
func (s S) After() []string { return s.after }

// Before specifies the list of stacks that must run after this stack.
func (s S) Before() []string { return s.before }

// Wants specifies the list of wanted stacks.
func (s S) Wants() []string { return s.wants }

// IsChanged tells if the stack is marked as changed.
func (s S) IsChanged() bool { return s.changed }

// SetChanged sets the changed flag of the stack.
func (s *S) SetChanged(b bool) { s.changed = b }

// String representation of the stack.
func (s S) String() string { return s.Path() }

// Path returns the project's absolute path of stack.
func (s S) Path() string { return s.path }

// HostPath returns the file system absolute path of stack.
func (s S) HostPath() string { return s.hostpath }

// MetaToCtyMap returns metadata as a cty values map.
func MetaToCtyMap(m Metadata) map[string]cty.Value {
	return map[string]cty.Value{
		"name":        cty.StringVal(m.Name()),
		"path":        cty.StringVal(m.Path()),
		"description": cty.StringVal(m.Desc()),
	}
}

// Sort sorts the given stacks.
func Sort(stacks []S) {
	sort.Sort(stackSlice(stacks))
}

// Reverse reverses the given stacks slice.
func Reverse(stacks []S) {
	i, j := 0, len(stacks)-1
	for i < j {
		stacks[i], stacks[j] = stacks[j], stacks[i]
		i++
		j--
	}
}

// stackSlice implements the Sort interface.
type stackSlice []S

func (l stackSlice) Len() int           { return len(l) }
func (l stackSlice) Less(i, j int) bool { return l[i].Path() < l[j].Path() }
func (l stackSlice) Swap(i, j int)      { l[i], l[j] = l[j], l[i] }
