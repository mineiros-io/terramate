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

package terramate

import (
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"

	"github.com/mineiros-io/terramate/config"
	"github.com/mineiros-io/terramate/errors"
	"github.com/mineiros-io/terramate/hcl"
	"github.com/mineiros-io/terramate/stack"
)

const errInit errors.Kind = "stack initialization failed"

// Init initialize a stack. If the stack is already initialized it returns
// with no error and no changes will be performed on the stack.
// The loader is needed to check if the stack is a leaf.
func Init(loader *stack.Loader, root, dir string) error {
	logger := log.With().
		Str("action", "Init()").
		Str("stack", dir).
		Logger()

	logger.Trace().Msg("Check if directory is abs.")

	if !filepath.IsAbs(dir) {
		// TODO(i4k): this needs to go away soon.
		return errors.E(errInit, "init requires an absolute path")
	}

	logger.Trace().Msg("Get directory info.")

	_, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return errors.E(errInit, "init requires an existing directory")
		}

		return errors.E(errInit, err, "stat failed on %q", dir)
	}

	logger.Trace().Msg("Check if stack is leaf.")

	ok, err := loader.IsLeafStack(dir)
	if err != nil {
		return errors.E(errInit, err)
	}

	if !ok {
		return errors.E(errInit, "directory %q is not a leaf stack", dir)
	}

	logger.Trace().Msg("Lookup parent stack.")
	parentStack, found, err := loader.LookupParentStack(dir)
	if err != nil {
		return errors.E(errInit, err)
	}

	if found {
		return errors.E(errInit, parentStack,
			"directory %q is inside stack but nested stacks are disallowed",
			dir,
		)
	}

	logger.Trace().Msg("Get stack info.")

	parsedCfg, err := hcl.ParseDir(dir)
	if err != nil {
		return errors.E(errInit, err, "checking config in %q", dir)
	}

	if parsedCfg.Stack != nil {
		logger.Debug().Msg("Stack already initialized, nothing to do")
		return nil
	}

	if !parsedCfg.IsEmpty() {
		return errors.E("dir has terramate config with no stack defined, expected empty or a stack")
	}

	logger.Debug().Msg("Create new configuration.")

	cfg, err := hcl.NewConfig(dir)
	if err != nil {
		return errors.E(errInit, err, "failed to create new stack config")
	}

	cfg.Stack = &hcl.Stack{
		Name: filepath.Base(dir),
	}

	logger.Debug().Msg("Save configuration.")

	err = cfg.Save(config.DefaultFilename)
	if err != nil {
		return errors.E(errInit, err, "failed to write %q on stack %q",
			config.DefaultFilename,
			dir,
		)
	}

	return nil
}
