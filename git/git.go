package git

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type (
	// Config configures the wrapper.
	Config struct {
		Username      string // Username used in commits.
		Email         string // Email used in commits.
		DefaultBranch string // DefaultBranch is the default branch (commonly main).
		DefaultRemote string // DefaultRemote is the default remote (commonly origin).
		ProgramPath   string

		// WorkingDir sets the directory where the commands will be applied.
		WorkingDir string

		// InheritEnv tells if the parent environment variables must be
		// inherited by the git client.
		InheritEnv bool

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

	// LogLine is a log summary.
	LogLine struct {
		CommitID string
		Message  string
	}

	// Error is the sentinel error type.
	Error string

	// CmdError is the error for failed commands.
	CmdError struct {
		cmd    string // Command-line executed
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
	git := &Git{
		config: cfg,
	}

	err := git.applyDefaults()
	if err != nil {
		return nil, fmt.Errorf("applying default config values: %w", err)
	}

	err = git.validate()
	if err != nil {
		return nil, err
	}

	_, err = git.Version()
	return git, err
}

func (git *Git) applyDefaults() error {
	cfg := &git.config

	if cfg.ProgramPath == "" {
		programPath, err := exec.LookPath("git")
		if err != nil {
			return fmt.Errorf("%w: %v", ErrGitNotFound, err)
		}

		cfg.ProgramPath = programPath
	}

	if cfg.WorkingDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}

		cfg.WorkingDir = wd
	}

	if cfg.DefaultBranch == "" {
		cfg.DefaultBranch = "main"
	}

	if cfg.DefaultRemote == "" {
		cfg.DefaultRemote = "origin"
	}

	return nil
}

func (git *Git) validate() error {
	cfg := git.config

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
func (git *Git) Init(dir string, bare bool) error {
	if !git.config.AllowPorcelain {
		return fmt.Errorf("Init: %w", ErrDenyPorcelain)
	}

	args := []string{
		"-b", git.config.DefaultBranch,
	}

	if bare {
		args = append(args, "--bare")
	}

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
		_, err = git.exec("config", "--local", "user.name", git.config.Username)
		if err != nil {
			return err
		}
	}

	if git.config.Email != "" {
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

// Remotes returns a list of all configure remotes, empty list if no remote is available.
func (git *Git) Remotes() ([]string, error) {
	res, err := git.exec("remote")
	if err != nil {
		return nil, err
	}
	if res == "" {
		return nil, nil
	}
	return strings.Split(res, "\n"), nil
}

// SymbolicRef reads a symbolic reference.
// Given the reference, reads which branch head the given symbolic ref refers
// to and outputs its path, relative to the .git/ directory.
//
// Typically you would give HEAD as the argument to see which branch
// your working tree is on.
//
// Example: "refs/remotes/origin/HEAD"
func (git *Git) SymbolicRef(ref string) (string, error) {
	return git.exec("symbolic-ref", ref)
}

// LogSummary returns a list of commit log summary in reverse chronological
// order from the revs set operation. It expects the same revision list as the
// `git rev-list` command.
//
// It returns only the first line of the commit message.
func (git *Git) LogSummary(revs ...string) ([]LogLine, error) {
	if len(revs) == 0 {
		revs = append(revs, "HEAD")
	}

	args := append([]string{}, "--pretty=oneline")
	args = append(args, revs...)

	out, err := git.exec("rev-list", args...)
	if err != nil {
		return nil, err
	}

	logs := []LogLine{}

	lines := strings.Split(out, "\n")
	for _, line := range lines {
		l := strings.TrimSpace(line)
		if len(l) == 0 {
			break
		}

		index := strings.Index(l, " ")
		if index == -1 {
			return nil, fmt.Errorf("malformed log line")
		}

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
	_, err := git.exec("add", files...)
	return err
}

// Commit the current staged changes.
// The args are extra flags and/or arguments to git commit command line.
// Beware: Commit is a porcelain method.
func (git *Git) Commit(msg string, args ...string) error {
	if !git.config.AllowPorcelain {
		return fmt.Errorf("Commit: %w", ErrDenyPorcelain)
	}

	for _, arg := range args {
		if arg == "-m" {
			return fmt.Errorf("the -m argument is already implicitly set")
		}
	}

	vargs := []string{
		"-m", msg,
	}

	vargs = append(vargs, args...)

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
	args := []string{from, to}

	if relative {
		args = append(args, "--relative")
	}

	if nameOnly {
		args = append(args, "--name-only")
	}

	if recurse {
		args = append(args, "-r") // git help shows no long flag name
	}

	return git.exec("diff-tree", args...)
}

// DiffNames recursively walks the git tree objects computing the from and to
// commit ids differences and return all the file names containing differences
// relative to configuration WorkingDir.
func (git *Git) DiffNames(from, to string) ([]string, error) {
	diff, err := git.DiffTree(from, to, true, true, true)
	if err != nil {
		return nil, fmt.Errorf("diff-tree: %w", err)
	}

	return removeEmptyLines(strings.Split(diff, "\n")), nil
}

// NewBranch creates a new branch reference pointing to current HEAD.
func (git *Git) NewBranch(name string) error {
	_, err := git.RevParse(name)
	if err == nil {
		return fmt.Errorf("branch \"%s\" already exists", name)
	}
	_, err = git.exec("update-ref", "refs/heads/"+name, "HEAD")
	return err
}

// DeleteBranch deletes the branch.
func (git *Git) DeleteBranch(name string) error {
	_, err := git.RevParse(name)
	if err != nil {
		return fmt.Errorf("branch \"%s\" doesn't exist", name)
	}
	_, err = git.exec("update-ref", "-d", "refs/heads/"+name)
	return err
}

// Checkout switches branches or change to specific revisions in the tree.
// When switching branches, the create flag can be set to automatically create
// the new branch before changing into it.
// Beware: Checkout is a porcelain method.
func (git *Git) Checkout(rev string, create bool) error {
	if !git.config.AllowPorcelain {
		return fmt.Errorf("Checkout: %w", ErrDenyPorcelain)
	}

	if create {
		err := git.NewBranch(rev)
		if err != nil {
			return err
		}
	}

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

	_, err := git.exec("merge", "--no-ff", branch)
	return err
}

// Push changes from branch onto remote.
func (git *Git) Push(remote, branch string) error {
	if !git.config.AllowPorcelain {
		return fmt.Errorf("Push: %w", ErrDenyPorcelain)
	}

	_, err := git.exec("push", remote, branch)
	return err
}

// Pull changes from remote into branch
func (git *Git) Pull(remote, branch string) error {
	if !git.config.AllowPorcelain {
		return fmt.Errorf("Pull: %w", ErrDenyPorcelain)
	}

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

	out, err := git.exec("ls-files", args...)
	if err != nil {
		return nil, fmt.Errorf("ls-files: %w", err)
	}

	return removeEmptyLines(strings.Split(out, "\n")), nil
}

// IsRepository tell if the git wrapper setup is operating in a valid git
// repository.
func (git *Git) IsRepository() bool {
	_, err := git.exec("rev-parse", "--git-dir")
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

func (git *Git) exec(command string, args ...string) (string, error) {
	cmd := exec.Cmd{
		Path: git.config.ProgramPath,
		Args: []string{git.config.ProgramPath, command},
		Dir:  git.config.WorkingDir,
		Env:  []string{},
	}

	cmd.Args = append(cmd.Args, args...)

	if git.config.InheritEnv {
		cmd.Env = os.Environ()
	}

	if git.config.Isolated {
		cmd.Env = append(cmd.Env, "GIT_CONFIG_SYSTEM=/dev/null")
		cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL=/dev/null")
		cmd.Env = append(cmd.Env, "GIT_CONFIG_NOGLOBAL=1") // back-compat
		cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1") // back-compat
		cmd.Env = append(cmd.Env, "GIT_ATTR_NOSYSTEM=1")
	}

	stdout, err := cmd.Output()
	if err != nil {
		stderr := []byte{}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			stderr = exitError.Stderr
		}

		return "", NewCmdError(cmd.String(), stdout, stderr)
	}

	out := strings.TrimSpace(string(stdout))
	return out, nil
}

// Error string representation.
func (e Error) Error() string {
	return string(e)
}

// NewCmdError returns a new command line error.
func NewCmdError(cmd string, stdout, stderr []byte) error {
	return &CmdError{
		cmd:    cmd,
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
	return fmt.Sprintf(
		"failed to exec: %s : stdout=%q stderr=%q",
		e.cmd,
		string(e.stdout),
		string(e.stderr),
	)
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

func removeEmptyLines(lines []string) []string {
	outlines := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			outlines = append(outlines, line)
		}
	}
	return outlines
}
