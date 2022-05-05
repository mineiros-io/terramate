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

package git

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"
)

type (
	// Config configures the wrapper.
	Config struct {
		Username    string // Username used in commits.
		Email       string // Email used in commits.
		ProgramPath string

		// WorkingDir sets the directory where the commands will be applied.
		WorkingDir string

		// Env is the environment variables to be passed over to git.
		// If it is nil it means no environment variables should be passed.
		// To inherit all env vars from the parent process os.Getenv() needs
		// to be passed explicitly.
		Env []string

		// Isolated tells if the wrapper should run with isolated
		// configurations, which means setting it to true will make the wrapper
		// not rely on the global/system configuration. It's useful for
		// reproducibility of scripts.
		Isolated bool

		// AllowPorcelain tells if the wrapper is allowed to execute porcelain
		// commands. It's useful if the user is not sure if all commands used by
		// their program are plumbing.
		AllowPorcelain bool
	}

	// Git is the wrapper object.
	Git struct {
		config Config
	}

	// Ref is a git reference.
	Ref struct {
		Name     string
		CommitID string
	}

	// Remote is a git remote.
	Remote struct {
		// Name of the remote reference
		Name string
		// Branches are all the branches the remote reference has
		Branches []string
	}

	// LogLine is a log summary.
	LogLine struct {
		CommitID string
		Message  string
	}

	// Error is the sentinel error type.
	Error string

	// CmdError is the error for failed commands.
	CmdError struct {
		cmd    string // command-line executed
		code   int    // exit code
		stdout []byte // stdout of the failed command
		stderr []byte // stderr of the failed command
	}
)

const (
	// ErrGitNotFound is the error that tells if git was not found.
	ErrGitNotFound Error = "git program not found"

	// ErrInvalidConfig is the error that tells if the configuration is invalid.
	ErrInvalidConfig Error = "invalid configuration"

	// ErrDenyPorcelain is the error that tells if a porcelain method was called
	// when AllowPorcelain is false.
	ErrDenyPorcelain Error = "porcelain commands are not allowed by the configuration"
)

type remoteSorter []Remote

// NewConfig creates a new configuration. The username and email are the only
// required config fields.
func NewConfig(username, email string) Config {
	return Config{
		Username: username,
		Email:    email,
	}
}

// NewConfigWithPath calls NewConfig but also sets the git program path.
func NewConfigWithPath(username, email, programPath string) Config {
	log.Debug().
		Str("action", "NewConfigWithPath()").
		Str("path", programPath).
		Msg("Make new config.")
	config := NewConfig(username, email)
	config.ProgramPath = programPath
	return config
}

// NewWrapper creates a new wrapper.
func NewWrapper(user, email string) (*Git, error) {
	return WithConfig(NewConfig(user, email))
}

// WithConfig creates a new git wrapper by providing the config.
func WithConfig(cfg Config) (*Git, error) {
	logger := log.With().
		Str("action", "WithConfig()").
		Str("workingDir", cfg.WorkingDir).
		Logger()

	logger.Trace().
		Msg("Construct new config.")
	git := &Git{
		config: cfg,
	}

	logger.Trace().
		Msg("Apply defaults.")
	err := git.applyDefaults()
	if err != nil {
		return nil, fmt.Errorf("applying default config values: %w", err)
	}

	logger.Trace().
		Msg("Validate git config.")
	err = git.validate()
	if err != nil {
		return nil, err
	}

	logger.Trace().
		Msg("Get git version.")
	_, err = git.Version()
	return git, err
}

func (git *Git) applyDefaults() error {
	logger := log.With().
		Str("action", "applyDefaults()").
		Str("workingDir", git.config.WorkingDir).
		Logger()

	cfg := &git.config

	if cfg.ProgramPath == "" {
		logger.Trace().
			Msg("Config program path was null.")

		logger.Trace().
			Msg("Look for path 'git'.")
		programPath, err := exec.LookPath("git")
		if err != nil {
			return fmt.Errorf("%w: %v", ErrGitNotFound, err)
		}

		cfg.ProgramPath = programPath
	}

	if cfg.WorkingDir == "" {
		logger.Trace().
			Msg("Working directory was null.")

		logger.Trace().
			Msg("Get working directory.")
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}

		cfg.WorkingDir = wd
	}

	return nil
}

func (git *Git) validate() error {
	cfg := git.config

	logger := log.With().
		Str("action", "validate()").
		Str("workingDir", git.config.WorkingDir).
		Logger()

	logger.Trace().
		Str("path", cfg.ProgramPath).
		Msg("Get path program path information.")
	_, err := os.Stat(cfg.ProgramPath)
	if err != nil {
		return fmt.Errorf("failed to stat git program path \"%s\": %w: %v",
			cfg.ProgramPath, ErrInvalidConfig, err)
	}

	// DefaultBranch and DefaultRemote cannot be validated yet because the
	// repository needs to be initialized and git wrapper can be used to
	// initialize a repository with any branch and remote names user wants.

	return nil
}

// Version of the git program.
func (git *Git) Version() (string, error) {
	logger := log.With().
		Str("action", "Version()").
		Str("workingDir", git.config.WorkingDir).
		Logger()

	logger.Debug().
		Msg("Get git version.")
	out, err := git.exec("version")
	if err != nil {
		return "", err
	}

	const expected = "git version "

	// git version 2.33.1
	if strings.HasPrefix(out, expected) {
		return out[len(expected):], nil
	}

	return "", fmt.Errorf("unexpected \"git version\" output: %q", out)
}

// Init initializes a git repository. If bare is true, it initializes a "bare
// repository", in other words, a repository not intended for work but just
// store revisions.
// Beware: Init is a porcelain method.
func (git *Git) Init(dir string, defaultBranch string, bare bool) error {
	logger := log.With().
		Str("action", "Init()").
		Str("workingDir", git.config.WorkingDir).
		Logger()

	if !git.config.AllowPorcelain {
		return fmt.Errorf("Init: %w", ErrDenyPorcelain)
	}

	args := []string{
		"-b", defaultBranch,
	}

	if bare {
		args = append(args, "--bare")
	}

	logger.Trace().Msg("Append arguments to init command and execute.")

	args = append(args, dir)
	_, err := git.exec("init", args...)
	if err != nil {
		return err
	}

	bkwd := git.config.WorkingDir

	defer func() {
		git.config.WorkingDir = bkwd
	}()

	git.config.WorkingDir = dir

	if git.config.Username != "" {
		logger.Trace().
			Msg("Username was set. Configure username.")
		_, err = git.exec("config", "--local", "user.name", git.config.Username)
		if err != nil {
			return err
		}
	}

	if git.config.Email != "" {
		logger.Trace().
			Msg("Email was set. Configure email.")
		_, err = git.exec("config", "--local", "user.email", git.config.Email)
		if err != nil {
			return err
		}
	}

	return nil
}

// RemoteAdd adds a new remote name.
func (git *Git) RemoteAdd(name string, url string) error {
	_, err := git.exec("remote", "add", name, url)
	return err
}

// Remotes returns a list of all configured remotes and their respective branches.
// The result slice is ordered lexicographically by the remote name.
//
// Returns an empty list if no remote is found.
func (git *Git) Remotes() ([]Remote, error) {
	const refprefix = "refs/remotes/"

	logger := log.With().
		Str("action", "Remotes()").
		Str("workingDir", git.config.WorkingDir).
		Logger()

	logger.Trace().
		Msg("Get all remote references.")
	res, err := git.exec("for-each-ref", "--format", "%(refname)", refprefix)

	if err != nil {
		return nil, err
	}

	if res == "" {
		return nil, nil
	}

	references := map[string][]string{}

	logger.Trace().
		Msg("Range over references.")
	for _, rawref := range strings.Split(res, "\n") {
		logger.Trace().
			Str("reference", rawref).
			Msg("Format reference.")
		trimmedref := strings.TrimPrefix(rawref, refprefix)
		parsed := strings.Split(trimmedref, "/")
		if len(parsed) < 2 {
			return nil, fmt.Errorf("unexpected remote reference %q", rawref)
		}
		name := parsed[0]
		branch := strings.Join(parsed[1:], "/")
		branches := references[name]
		logger.Trace().
			Str("reference", branch).
			Msg("Append formatted reference to branches array.")
		references[name] = append(branches, branch)
	}

	var remotes remoteSorter

	logger.Trace().
		Msg("Create remotes array.")
	for name, branches := range references {
		remotes = append(remotes, Remote{Name: name, Branches: branches})
	}

	logger.Trace().
		Msg("Sort remotes.")
	sort.Stable(remotes)
	return remotes, nil
}

// LogSummary returns a list of commit log summary in reverse chronological
// order from the revs set operation. It expects the same revision list as the
// `git rev-list` command.
//
// It returns only the first line of the commit message.
func (git *Git) LogSummary(revs ...string) ([]LogLine, error) {
	logger := log.With().
		Str("action", "LogSummary()").
		Str("workingDir", git.config.WorkingDir).
		Logger()

	if len(revs) == 0 {
		logger.Trace().
			Msg("Length of revisions was 0. Append 'HEAD'.")
		revs = append(revs, "HEAD")
	}

	logger.Trace().
		Msg("Append `-pretty-oneline` argument.")
	args := append([]string{}, "--pretty=oneline")
	args = append(args, revs...)

	logger.Trace().
		Msg("Get list of revs.")
	out, err := git.exec("rev-list", args...)
	if err != nil {
		return nil, err
	}

	logs := []LogLine{}

	lines := strings.Split(out, "\n")

	logger.Trace().
		Msg("Range over revs.")
	for _, line := range lines {
		l := strings.TrimSpace(line)
		if len(l) == 0 {
			break
		}

		index := strings.Index(l, " ")
		if index == -1 {
			return nil, fmt.Errorf("malformed log line")
		}

		logger.Trace().
			Msg("Append rev to logs.")
		logs = append(logs, LogLine{
			CommitID: l[0:index],
			Message:  l[index+1:],
		})
	}

	return logs, nil
}

// Add files to current staged index.
// Beware: Add is a porcelain method.
func (git *Git) Add(files ...string) error {
	if !git.config.AllowPorcelain {
		return fmt.Errorf("Add: %w", ErrDenyPorcelain)
	}

	log.Debug().
		Str("action", "Add()").
		Str("workingDir", git.config.WorkingDir).
		Msg("Add file to current staged index.")
	_, err := git.exec("add", files...)
	return err
}

// Commit the current staged changes.
// The args are extra flags and/or arguments to git commit command line.
// Beware: Commit is a porcelain method.
func (git *Git) Commit(msg string, args ...string) error {
	logger := log.With().
		Str("action", "Commit()").
		Str("workingDir", git.config.WorkingDir).
		Logger()

	if !git.config.AllowPorcelain {
		return fmt.Errorf("Commit: %w", ErrDenyPorcelain)
	}

	logger.Trace().
		Msg("Range args.")
	for _, arg := range args {
		if arg == "-m" {
			return fmt.Errorf("the -m argument is already implicitly set")
		}
	}

	vargs := []string{
		"-m", msg,
	}

	vargs = append(vargs, args...)

	logger.Debug().
		Msg("Commit with args.")
	_, err := git.exec("commit", vargs...)
	return err
}

// RevParse parses the rev name and returns the commit id it points to.
// The rev name follows the [git revisions](https://git-scm.com/docs/gitrevisions)
// documentation.
func (git *Git) RevParse(rev string) (string, error) {
	return git.exec("rev-parse", rev)
}

// FetchRemoteRev will fetch from the remote repo the commit id and ref name
// for the given remote and reference. This will make use of the network
// to fetch data from the remote configured on the git repo.
func (git *Git) FetchRemoteRev(remote, ref string) (Ref, error) {
	logger := log.With().
		Str("action", "FetchRemoteRev()").
		Str("workingDir", git.config.WorkingDir).
		Logger()

	logger.Debug().
		Msg("List references in remote repository.")
	output, err := git.exec("ls-remote", remote, ref)
	if err != nil {
		return Ref{}, fmt.Errorf(
			"Git.FetchRemoteRev: git ls-remote %q %q: %v",
			remote,
			ref,
			err,
		)
	}
	parsed := strings.Split(output, "\t")
	if len(parsed) != 2 {
		return Ref{}, fmt.Errorf(
			"Git.FetchRemoteRev: git ls-remote %q %q can't parse: %v",
			remote,
			ref,
			output,
		)
	}
	return Ref{
		CommitID: parsed[0],
		Name:     parsed[1],
	}, nil
}

// MergeBase finds the common commit ancestor of commit1 and commit2.
func (git *Git) MergeBase(commit1, commit2 string) (string, error) {
	return git.exec("merge-base", commit1, commit2)
}

// Status returns the git status of the current branch.
// Beware: Status is a porcelain method.
func (git *Git) Status() (string, error) {
	if !git.config.AllowPorcelain {
		return "", fmt.Errorf("Status: %w", ErrDenyPorcelain)
	}

	return git.exec("status")
}

// DiffTree compares the from and to commit ids and returns the differences. If
// nameOnly is set then only the file names of changed files are show. If
// recurse is set, then it walks into child trees as well. If
// relative is set, then only show local changes of current dir.
func (git *Git) DiffTree(from, to string, relative, nameOnly, recurse bool) (string, error) {
	logger := log.With().
		Str("action", "DiffTree()").
		Str("workingDir", git.config.WorkingDir).
		Logger()

	args := []string{from, to}

	if relative {
		logger.Trace().
			Msg("Set `--relative` command line arg.")
		args = append(args, "--relative")
	}

	if nameOnly {
		logger.Trace().
			Msg("Set `--name-only` command line arg.")
		args = append(args, "--name-only")
	}

	if recurse {
		logger.Trace().
			Msg("Set `--r` command line arg.")
		args = append(args, "-r") // git help shows no long flag name
	}

	return git.exec("diff-tree", args...)
}

// DiffNames recursively walks the git tree objects computing the from and to
// commit ids differences and return all the file names containing differences
// relative to configuration WorkingDir.
func (git *Git) DiffNames(from, to string) ([]string, error) {
	log.Trace().
		Str("action", "DiffNames()").
		Str("workingDir", git.config.WorkingDir).
		Str("reference", fmt.Sprintf("from `%s` to `%s`", from, to)).
		Msg("Get tree differences.")
	diff, err := git.DiffTree(from, to, true, true, true)
	if err != nil {
		return nil, fmt.Errorf("diff-tree: %w", err)
	}

	return removeEmptyLines(strings.Split(diff, "\n")), nil
}

// NewBranch creates a new branch reference pointing to current HEAD.
func (git *Git) NewBranch(name string) error {
	log.Trace().
		Str("action", "NewBranch()").
		Str("workingDir", git.config.WorkingDir).
		Str("reference", name).
		Msg("Get commit ID.")
	_, err := git.RevParse(name)
	if err == nil {
		return fmt.Errorf("branch \"%s\" already exists", name)
	}

	log.Debug().
		Str("action", "NewBranch()").
		Str("workingDir", git.config.WorkingDir).
		Str("reference", name).
		Msg("Create new branch.")
	_, err = git.exec("update-ref", "refs/heads/"+name, "HEAD")
	return err
}

// DeleteBranch deletes the branch.
func (git *Git) DeleteBranch(name string) error {
	log.Trace().
		Str("action", "DeleteBranch()").
		Str("workingDir", git.config.WorkingDir).
		Str("reference", name).
		Msg("Get commit ID.")
	_, err := git.RevParse(name)
	if err != nil {
		return fmt.Errorf("branch \"%s\" doesn't exist", name)
	}

	log.Debug().
		Str("action", "DeleteBranch()").
		Str("workingDir", git.config.WorkingDir).
		Str("reference", name).
		Msg("Delete branch.")
	_, err = git.exec("update-ref", "-d", "refs/heads/"+name)
	return err
}

// Checkout switches branches or change to specific revisions in the tree.
// When switching branches, the create flag can be set to automatically create
// the new branch before changing into it.
// Beware: Checkout is a porcelain method.
func (git *Git) Checkout(rev string, create bool) error {
	if !git.config.AllowPorcelain {
		return fmt.Errorf("checkout: %w", ErrDenyPorcelain)
	}

	if create {
		log.Trace().
			Str("action", "Checkout()").
			Str("workingDir", git.config.WorkingDir).
			Str("reference", rev).
			Msg("Create new branch.")
		err := git.NewBranch(rev)
		if err != nil {
			return err
		}
	}

	log.Debug().
		Str("action", "Checkout()").
		Str("workingDir", git.config.WorkingDir).
		Str("reference", rev).
		Msg("Checkout.")
	_, err := git.exec("checkout", rev)
	return err
}

// Merge branch into current branch using the non fast-forward strategy.
// For doing a fast-forwarded merge see FFMerge().
// Beware: Merge is a porcelain method.
func (git *Git) Merge(branch string) error {
	if !git.config.AllowPorcelain {
		return fmt.Errorf("Merge: %w", ErrDenyPorcelain)
	}

	log.Debug().
		Str("action", "Merge()").
		Str("workingDir", git.config.WorkingDir).
		Str("reference", branch).
		Msg("Merge.")
	_, err := git.exec("merge", "--no-ff", branch)
	return err
}

// Push changes from branch onto remote.
func (git *Git) Push(remote, branch string) error {
	if !git.config.AllowPorcelain {
		return fmt.Errorf("Push: %w", ErrDenyPorcelain)
	}

	log.Debug().
		Str("action", "Push()").
		Str("workingDir", git.config.WorkingDir).
		Str("reference", fmt.Sprintf("from `%s` to `%s`", branch, remote)).
		Msg("Git push.")
	_, err := git.exec("push", remote, branch)
	return err
}

// Pull changes from remote into branch
func (git *Git) Pull(remote, branch string) error {
	if !git.config.AllowPorcelain {
		return fmt.Errorf("Pull: %w", ErrDenyPorcelain)
	}

	log.Debug().
		Str("action", "Pull()").
		Str("workingDir", git.config.WorkingDir).
		Str("reference", fmt.Sprintf("from `%s` to `%s`", remote, branch)).
		Msg("Git pull.")
	_, err := git.exec("pull", remote, branch)
	return err
}

// FFMerge branch into current branch using the fast-forward strategy.
// For doing a non fast-forwarded merge see Merge().
// Beware: FFMerge is a porcelain method.
func (git *Git) FFMerge(branch string) error {
	if !git.config.AllowPorcelain {
		return fmt.Errorf("FFMerge: %w", ErrDenyPorcelain)
	}

	log.Debug().
		Str("action", "FFMerge()").
		Str("workingDir", git.config.WorkingDir).
		Str("reference", branch).
		Msg("Fast forward merge branch.")
	_, err := git.exec("merge", "--ff", branch)
	return err
}

// ListUntracked lists untracked files in the directories provided in dirs.
func (git *Git) ListUntracked(dirs ...string) ([]string, error) {
	args := []string{
		"--others", "--exclude-standard",
	}

	if len(dirs) > 0 {
		args = append(args, "--")
		args = append(args, dirs...)
	}

	log.Debug().
		Str("action", "ListUntracked()").
		Str("workingDir", git.config.WorkingDir).
		Msg("List untracked files.")
	out, err := git.exec("ls-files", args...)
	if err != nil {
		return nil, fmt.Errorf("ls-files: %w", err)
	}

	return removeEmptyLines(strings.Split(out, "\n")), nil

}

// ListUncommitted lists uncommitted files in the directories provided in dirs.
func (git *Git) ListUncommitted(dirs ...string) ([]string, error) {
	args := []string{
		"--modified", "--exclude-standard",
	}

	if len(dirs) > 0 {
		args = append(args, "--")
		args = append(args, dirs...)
	}

	log.Debug().
		Str("action", "ListUncommitted()").
		Str("workingDir", git.config.WorkingDir).
		Msg("List uncommitted files.")
	out, err := git.exec("ls-files", args...)
	if err != nil {
		return nil, fmt.Errorf("ls-files: %w", err)
	}

	return removeEmptyLines(strings.Split(out, "\n")), nil
}

// Root returns the git root directory.
func (git *Git) Root() (string, error) {
	return git.exec("rev-parse", "--git-dir")
}

// IsRepository tell if the git wrapper setup is operating in a valid git
// repository.
func (git *Git) IsRepository() bool {
	_, err := git.Root()
	return err == nil
}

// Exec executes any provided git command. We don't allow Exec if AllowPorcelain
// is set to false.
func (git *Git) Exec(command string, args ...string) (string, error) {
	if !git.config.AllowPorcelain {
		return "", fmt.Errorf("Exec: %w", ErrDenyPorcelain)
	}
	return git.exec(command, args...)
}

// CurrentBranch returns the short branch name that HEAD points to.
func (git *Git) CurrentBranch() (string, error) {
	return git.exec("symbolic-ref", "--short", "HEAD")
}

// IsIgnored checks if the file must be ignored.
func (git *Git) IsIgnored(path string) (bool, error) {
	_, err := git.exec("check-ignore", path)
	if err != nil {
		var cmdErr *CmdError
		if errors.As(err, &cmdErr) {
			if cmdErr.ExitCode() > 1 {
				return false, err
			}
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (git *Git) exec(command string, args ...string) (string, error) {
	logger := log.With().
		Str("action", "exec()").
		Str("workingDir", git.config.WorkingDir).
		Logger()

	logger.Trace().
		Msg("Create cmd to execute.")
	cmd := exec.Cmd{
		Path: git.config.ProgramPath,
		Args: []string{git.config.ProgramPath, command},
		Dir:  git.config.WorkingDir,
		Env:  []string{},
	}

	logger.Trace().
		Msg("Append arguments.")
	cmd.Args = append(cmd.Args, args...)

	// nil and empty slice behave differently on exec.Cmd.
	// nil defaults to use parent env, empty means actually empty.
	// we want nil and empty to behave the same (no env).
	if git.config.Env != nil {
		cmd.Env = git.config.Env
	}

	if git.config.Isolated {
		logger.Trace().
			Msg("Add git config environment variables.")
		cmd.Env = append(cmd.Env, "GIT_CONFIG_SYSTEM=/dev/null")
		cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL=/dev/null")
		cmd.Env = append(cmd.Env, "GIT_CONFIG_NOGLOBAL=1") // back-compat
		cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1") // back-compat
		cmd.Env = append(cmd.Env, "GIT_ATTR_NOSYSTEM=1")
	}

	logger.Debug().Msg("Run command.")

	stdout, err := cmd.Output()
	if err != nil {
		var execError *exec.ExitError
		if errors.As(err, &execError) {
			return "", NewCmdError(
				cmd.String(), execError.ExitCode(), stdout, execError.Stderr,
			)
		}
		return "", err
	}

	out := strings.TrimSpace(string(stdout))
	return out, nil
}

// Error string representation.
func (e Error) Error() string {
	return string(e)
}

// NewCmdError returns a new command line error.
func NewCmdError(cmd string, code int, stdout []byte, stderr []byte) error {
	return &CmdError{
		cmd:    cmd,
		code:   code,
		stdout: stdout,
		stderr: stderr,
	}
}

// Is tells if err is of the type CmdError.
func (e *CmdError) Is(err error) bool {
	_, ok := err.(*CmdError)
	return ok
}

// Error string representation.
func (e *CmdError) Error() string {
	return fmt.Sprintf("failed to exec: %s : stderr=%q", e.cmd, string(e.stderr))
}

// ShortCommitID returns the short version of the commit ID.
// If the reference doesn't have a valid commit id it returns empty.
func (r Ref) ShortCommitID() string {
	if len(r.CommitID) < 8 {
		return ""
	}
	return r.CommitID[0:8]
}

// Command is the failed command.
func (e *CmdError) Command() string { return e.cmd }

// Stdout of the failed command.
func (e *CmdError) Stdout() []byte { return e.stdout }

// Stderr of the failed command.
func (e *CmdError) Stderr() []byte { return e.stderr }

// ExitCode returns the exit code.
func (e *CmdError) ExitCode() int { return e.code }

func (r remoteSorter) Len() int {
	return len(r)
}

func (r remoteSorter) Less(i, j int) bool {
	return r[i].Name < r[j].Name
}

func (r remoteSorter) Swap(i, j int) {
	r[i], r[j] = r[j], r[i]
}

func removeEmptyLines(lines []string) []string {
	log.Trace().
		Str("action", "removeEmptyLines()").
		Msg("Remove empty lines.")
	outlines := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			outlines = append(outlines, line)
		}
	}
	return outlines
}
