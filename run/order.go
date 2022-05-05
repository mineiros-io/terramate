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

package run

import (
	"fmt"

	"github.com/mineiros-io/terramate/run/dag"
	"github.com/mineiros-io/terramate/stack"
	"github.com/rs/zerolog/log"
)

type visited map[string]struct{}

// Sort computes the final execution order for the given list of stacks.
// In the case of multiple possible orders, it returns the lexicographic sorted
// path.
func Sort(root string, stacks []stack.S) ([]stack.S, string, error) {
	d := dag.New()
	loader := stack.NewLoader(root)

	for _, stack := range stacks {
		loader.Set(stack.Path(), stack)
	}

	visited := visited{}

	logger := log.With().
		Str("action", "run.Sort()").
		Str("root", root).
		Logger()

	logger.Trace().Msg("Sorting stacks.")

	for _, stack := range stacks {
		if _, ok := visited[stack.Path()]; ok {
			continue
		}

		logger.Debug().
			Str("stack", stack.Path()).
			Msg("Build DAG.")
		err := BuildDAG(d, root, stack, loader, visited)
		if err != nil {
			return nil, "", err
		}
	}

	logger.Trace().Msg("Validate DAG.")

	reason, err := d.Validate()
	if err != nil {
		return nil, reason, err
	}

	logger.Trace().Msg("Get topologically order DAG.")

	order := d.Order()

	orderedStacks := make([]stack.S, 0, len(order))

	logger.Trace().Msg("Get ordered stacks.")

	isSelectedStack := func(s stack.S) bool {
		// Stacks may be added on the DAG from after/before references
		// but they should not be on the final order if they are not part
		// of the previously selected stacks passed as a parameter.
		// This is important for change detection to work on ordering and
		// also for filtering by working dir.
		for _, stack := range stacks {
			if s.Path() == stack.Path() {
				return true
			}
		}
		return false
	}

	for _, id := range order {
		val, err := d.Node(id)
		if err != nil {
			return nil, "", fmt.Errorf("calculating run-order: %w", err)
		}
		s := val.(stack.S)
		if !isSelectedStack(s) {
			logger.Trace().
				Str("stack", s.Path()).
				Msg("ignoring since not part of selected stacks")
			continue
		}
		orderedStacks = append(orderedStacks, s)
	}

	return orderedStacks, "", nil
}

// BuildDAG builds a run order DAG for the given stack.
func BuildDAG(
	d *dag.DAG,
	root string,
	s stack.S,
	loader *stack.Loader,
	visited visited,
) error {
	logger := log.With().
		Str("action", "BuildDAG()").
		Str("path", root).
		Str("stack", s.Path()).
		Logger()

	visited[s.Path()] = struct{}{}

	logger.Trace().
		Msg("Load all stacks in dir after current stack.")
	afterStacks, err := loader.LoadAll(root, s.HostPath(), s.After()...)
	if err != nil {
		return fmt.Errorf("stack %q: failed to load the \"after\" stacks: %w", s, err)
	}

	logger.Trace().
		Msg("Load all stacks in dir before current stack.")
	beforeStacks, err := loader.LoadAll(root, s.HostPath(), s.Before()...)
	if err != nil {
		return fmt.Errorf("stack %q: failed to load the \"before\" stacks: %w", s, err)
	}

	logger.Debug().
		Msg("Add new node to DAG.")
	err = d.AddNode(dag.ID(s.Path()), s, toids(beforeStacks), toids(afterStacks))
	if err != nil {
		return fmt.Errorf("stack %q: failed to build DAG: %w", s, err)
	}

	stacks := []stack.S{}
	stacks = append(stacks, afterStacks...)
	stacks = append(stacks, beforeStacks...)

	logger.Trace().
		Msg("Range over stacks.")
	for _, s := range stacks {
		logger = log.With().
			Str("action", "BuildDAG()").
			Str("path", root).
			Str("stack", s.Path()).
			Logger()

		if _, ok := visited[s.Path()]; ok {
			continue
		}

		logger.Trace().
			Msg("Build DAG.")
		err = BuildDAG(d, root, s, loader, visited)
		if err != nil {
			return fmt.Errorf("stack %q: failed to build DAG: %w", s, err)
		}
	}
	return nil
}

func toids(values []stack.S) []dag.ID {
	ids := make([]dag.ID, 0, len(values))
	for _, v := range values {
		ids = append(ids, dag.ID(v.Path()))
	}
	return ids
}
