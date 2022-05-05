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

package cli

import (
	"fmt"

	"github.com/mineiros-io/terramate/errors"
	"github.com/mineiros-io/terramate/git"
	"github.com/mineiros-io/terramate/hcl"
	"github.com/mineiros-io/terramate/stack"
	"github.com/rs/zerolog/log"
)

type project struct {
	root    string
	wd      string
	isRepo  bool
	rootcfg hcl.Config
	baseRef string
	loader  *stack.Loader

	git struct {
		git                         *git.Git
		headCommitID                string
		localDefaultBranchCommitID  string
		remoteDefaultBranchCommitID string
	}
}

func (p project) gitcfg() *hcl.GitConfig {
	return p.rootcfg.Terramate.RootConfig.Git
}

func (p *project) parseLocalDefaultBranch(g *git.Git) error {
	gitcfg := p.gitcfg()
	refName := gitcfg.DefaultRemote + "/" + gitcfg.DefaultBranch
	val, err := g.RevParse(refName)
	if err != nil {
		return err
	}

	p.git.localDefaultBranchCommitID = val
	return nil
}

func (p *project) parseHead(g *git.Git) error {
	val, err := g.RevParse("HEAD")
	if err != nil {
		return err
	}

	p.git.headCommitID = val
	return nil
}

func (p *project) parseRemoteDefaultBranch(g *git.Git) error {
	gitcfg := p.gitcfg()
	remoteRef, err := g.FetchRemoteRev(gitcfg.DefaultRemote, gitcfg.DefaultBranch)
	if err != nil {
		return fmt.Errorf("fetching remote commit of %s/%s: %v",
			gitcfg.DefaultRemote, gitcfg.DefaultBranch,
			err,
		)
	}

	p.git.remoteDefaultBranchCommitID = remoteRef.CommitID
	return nil
}

func (p *project) isDefaultBranch(g *git.Git) bool {
	if p.git.localDefaultBranchCommitID != p.git.headCommitID {
		return false
	}

	git := p.gitcfg()
	branch, err := g.CurrentBranch()

	return err != nil || branch == git.DefaultBranch
}

func (p *project) defaultBaseRef(g *git.Git) string {
	git := p.gitcfg()
	if p.isDefaultBranch(g) {
		return git.DefaultBranchBaseRef
	}

	return p.defaultBranchRef()
}

func (p project) defaultBranchRef() string {
	git := p.gitcfg()
	return git.DefaultRemote + "/" + git.DefaultBranch
}

func (p *project) setDefaults(parsedArgs *cliSpec) error {
	logger := log.With().
		Str("action", "setDefaults()").
		Str("workingDir", p.wd).
		Logger()

	logger.Debug().Msg("Set defaults.")

	if p.rootcfg.Terramate == nil {
		// if config has no terramate block we create one with default
		// configurations.
		p.rootcfg.Terramate = &hcl.Terramate{}
	}

	cfg := &p.rootcfg
	if cfg.Terramate.RootConfig == nil {
		p.rootcfg.Terramate.RootConfig = &hcl.RootConfig{}
	}
	if cfg.Terramate.RootConfig.Git == nil {
		cfg.Terramate.RootConfig.Git = &hcl.GitConfig{}
	}

	gitOpt := cfg.Terramate.RootConfig.Git

	if gitOpt.DefaultBranchBaseRef == "" {
		gitOpt.DefaultBranchBaseRef = defaultBranchBaseRef
	}

	if gitOpt.DefaultBranch == "" {
		gitOpt.DefaultBranch = defaultBranch
	}

	if gitOpt.DefaultRemote == "" {
		gitOpt.DefaultRemote = defaultRemote
	}

	if p.isRepo {
		logger.Trace().Msg("Create new git wrapper.")

		gw, err := newGit(p.wd, false)
		if err != nil {
			return err
		}

		p.git.git = gw
		p.loader = stack.NewLoaderWithGit(p.root, gw)

		logger.Trace().Msg("Check git default remote.")

		if err := p.checkDefaultRemote(gw); err != nil {
			log.Fatal().
				Err(err).
				Msg("Checking git default remote.")
		}

		err = p.parseLocalDefaultBranch(gw)
		if err != nil {
			return err
		}

		err = p.parseRemoteDefaultBranch(gw)
		if err != nil {
			return err
		}

		err = p.parseHead(gw)
		if err != nil {
			return err
		}

		if parsedArgs.GitChangeBase != "" {
			p.baseRef = parsedArgs.GitChangeBase
		} else {
			p.baseRef = p.defaultBaseRef(gw)
		}
	} else {
		p.loader = stack.NewLoader(p.root)
	}

	return nil
}

func (p project) checkDefaultRemote(g *git.Git) error {
	logger := log.With().
		Str("action", "checkDefaultRemote()").
		Logger()

	logger.Trace().Msg("Get list of configured git remotes.")

	remotes, err := g.Remotes()
	if err != nil {
		return fmt.Errorf("checking if remote %q exists: %v", defaultRemote, err)
	}

	var defRemote *git.Remote

	gitcfg := p.gitcfg()

	logger.Trace().
		Msg("Find default git remote.")
	for _, remote := range remotes {
		if remote.Name == gitcfg.DefaultRemote {
			defRemote = &remote
			break
		}
	}

	if defRemote == nil {
		return fmt.Errorf("repository must have a configured %q remote",
			gitcfg.DefaultRemote,
		)
	}

	logger.Trace().Msg("Find default git branch.")
	for _, branch := range defRemote.Branches {
		if branch == gitcfg.DefaultBranch {
			return nil
		}
	}

	return fmt.Errorf("remote %q has no default branch %q,branches:%v",
		gitcfg.DefaultRemote,
		gitcfg.DefaultBranch,
		defRemote.Branches,
	)
}

func (p *project) checkLocalDefaultIsUpdated(g *git.Git) error {
	logger := log.With().
		Str("action", "checkLocalDefaultIsUpdated()").
		Str("workingDir", p.wd).
		Logger()

	logger.Trace().Msg("Create new git wrapper.")

	gw, err := newGit(p.wd, false)
	if err != nil {
		return err
	}

	if !p.isDefaultBranch(gw) {
		return nil
	}

	gitcfg := p.gitcfg()

	logger.Trace().Msg("Fetch remote reference.")

	mergeBaseCommitID, err := g.MergeBase(p.git.headCommitID, p.git.remoteDefaultBranchCommitID)
	if err != nil {
		return fmt.Errorf(
			"the reference %s/%s is not reachable from HEAD: %w",
			gitcfg.DefaultRemote,
			gitcfg.DefaultBranch,
			err,
		)
	}

	if mergeBaseCommitID != p.git.headCommitID {
		return errors.E(
			ErrOutdatedLocalRev,
			"remote %s/%s != HEAD",
			gitcfg.DefaultRemote,
			gitcfg.DefaultBranch,
		)
	}

	return nil
}
