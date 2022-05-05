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

package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mineiros-io/terramate/errors"
	"github.com/mineiros-io/terramate/generate"
	prj "github.com/mineiros-io/terramate/project"
	"github.com/mineiros-io/terramate/run"
	"github.com/mineiros-io/terramate/run/dag"

	"github.com/alecthomas/kong"
	"github.com/emicklei/dot"
	"github.com/mineiros-io/terramate"
	"github.com/mineiros-io/terramate/config"
	"github.com/mineiros-io/terramate/git"
	"github.com/mineiros-io/terramate/hcl"
	"github.com/mineiros-io/terramate/stack"
	"github.com/posener/complete"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/willabides/kongplete"
)

const (
	// ErrOutdatedLocalRev indicates the local revision is outdated.
	ErrOutdatedLocalRev errors.Kind = "outdated local revision"
	// ErrInit indicates a failure to initialize stacks.
	ErrInit errors.Kind = "failed to initialize all stacks"
	// ErrOutdatedGenCodeDetected indicates outdated generated code detected.
	ErrOutdatedGenCodeDetected errors.Kind = "outdated generated code detected"
)

const (
	defaultRemote        = "origin"
	defaultBranch        = "main"
	defaultBranchBaseRef = "HEAD^"
)

const (
	defaultLogLevel = "warn"
	defaultLogFmt   = "console"
)

type cliSpec struct {
	Version       struct{} `cmd:"" help:"Terramate version"`
	VersionFlag   bool     `name:"version" help:"Terramate version"`
	Chdir         string   `short:"C" optional:"true" help:"Sets working directory"`
	GitChangeBase string   `short:"B" optional:"true" help:"Git base ref for computing changes"`
	Changed       bool     `short:"c" optional:"true" help:"Filter by changed infrastructure"`
	LogLevel      string   `optional:"true" default:"warn" enum:"trace,debug,info,warn,error,fatal" help:"Log level to use: 'trace', 'debug', 'info', 'warn', 'error', or 'fatal'"`
	LogFmt        string   `optional:"true" default:"console" enum:"console,text,json" help:"Log format to use: 'console', 'text', or 'json'"`

	DisableCheckGitUntracked   bool `optional:"true" default:"false" help:"Disable git check for untracked files"`
	DisableCheckGitUncommitted bool `optional:"true" default:"false" help:"Disable git check for uncommitted files"`

	List struct {
		Why bool `help:"Shows the reason why the stack has changed"`
	} `cmd:"" help:"List stacks"`

	Run struct {
		DisableCheckGenCode bool     `optional:"true" default:"false" help:"Disable outdated generated code check"`
		ContinueOnError     bool     `default:"false" help:"Continue executing in other stacks in case of error"`
		DryRun              bool     `default:"false" help:"Plan the execution but do not execute it"`
		Reverse             bool     `default:"false" help:"Reverse the order of execution"`
		Command             []string `arg:"" name:"cmd" passthrough:"" help:"Command to execute"`
	} `cmd:"" help:"Run command in the stacks"`

	Generate struct{} `cmd:"" help:"Generate terraform code for stacks"`

	InstallCompletions kongplete.InstallCompletions `cmd:"" help:"Install shell completions"`

	Experimental struct {
		InitStack struct {
			StackDirs []string `arg:"" name:"paths" optional:"true" help:"The stack directory (current directory if not set)"`
		} `cmd:"" help:"Initialize a stack, does nothing if stack already initialized"`

		Metadata struct{} `cmd:"" help:"Shows metadata available on the project"`

		Globals struct {
		} `cmd:"" help:"List globals for all stacks"`

		RunGraph struct {
			Outfile string `short:"o" default:"" help:"Output .dot file"`
			Label   string `short:"l" default:"stack.name" help:"Label used in graph nodes (it could be either \"stack.name\" or \"stack.dir\""`
		} `cmd:"" help:"Generate a graph of the execution order"`

		RunOrder struct {
			Basedir string `arg:"" optional:"true" help:"Base directory to search stacks"`
		} `cmd:"" help:"Show the topological ordering of the stacks"`
	} `cmd:"" help:"Experimental features (may change or be removed in the future)"`
}

// Exec will execute terramate with the provided flags defined on args.
// Only flags should be on the args slice.
//
// Results will be written on stdout, according to the command flags and
// errors/warnings written on stderr. Exec will abort the process with a status
// code different than zero in the case of fatal errors.
//
// Sometimes sub commands may be executed, the provided stdin will be passed to
// then as the sub process stdin.
//
// Each Exec call is completely isolated from each other (no shared state) as
// far as the parameters are not shared between the run calls.
func Exec(
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) {
	configureLogging(defaultLogLevel, defaultLogFmt, stderr)
	c := newCLI(args, stdin, stdout, stderr)
	c.run()
}

type cli struct {
	ctx        *kong.Context
	parsedArgs *cliSpec
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	exit       bool
	prj        project
}

func newCLI(args []string, stdin io.Reader, stdout, stderr io.Writer) *cli {
	if len(args) == 0 {
		// WHY: avoid default kong error, print help
		args = []string{"--help"}
	}

	logger := log.With().
		Str("action", "newCli()").
		Logger()

	kongExit := false
	kongExitStatus := 0

	parsedArgs := cliSpec{}
	parser, err := kong.New(&parsedArgs,
		kong.Name("terramate"),
		kong.Description("A tool for managing terraform stacks"),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
		}),
		kong.Exit(func(status int) {
			// Avoid kong aborting entire process since we designed CLI as lib
			kongExit = true
			kongExitStatus = status
		}),
		kong.Writers(stdout, stderr),
	)

	if err != nil {
		logger.Fatal().
			Err(err).
			Msg("failed to create cli parser")
	}

	kongplete.Complete(parser,
		kongplete.WithPredictor("cli", complete.PredictAnything),
	)

	ctx, err := parser.Parse(args)

	if kongExit && kongExitStatus == 0 {
		return &cli{exit: true}
	}

	// When we run terramate --version the kong parser just fails
	// since no subcommand was provided (which is odd..but happens).
	// So we check if the flag for version is present before checking the error.
	if parsedArgs.VersionFlag {
		logger.Debug().Msg("Get terramate version using --version.")
		fmt.Println(terramate.Version())
		return &cli{exit: true}
	}

	if err != nil {
		logger.Fatal().
			Err(err).
			Msgf("failed to parse cli args: %v", args)
	}

	configureLogging(parsedArgs.LogLevel, parsedArgs.LogFmt, stderr)
	// If we don't re-create the logger after configuring we get some
	// log entries with a mix of default fmt and selected fmt.
	logger = log.With().
		Str("action", "newCli()").
		Logger()

	switch ctx.Command() {
	case "version":
		logger.Debug().Msg("Get terramate version with version subcommand.")
		fmt.Println(terramate.Version())
		return &cli{exit: true}
	case "install-completions":
		logger.Debug().Msg("Handle `install-completions` command.")

		err := parsedArgs.InstallCompletions.Run(ctx)
		if err != nil {
			log.Fatal().
				Err(err).
				Msg("installing shell completions.")
		}
		return &cli{exit: true}
	}

	wd, err := os.Getwd()
	if err != nil {
		log.Fatal().
			Err(err).
			Msg("Getwd() failed")
	}

	logger = logger.With().
		Str("workingDir", wd).
		Logger()

	if parsedArgs.Chdir != "" {
		logger.Debug().
			Str("dir", parsedArgs.Chdir).
			Msg("Changing working directory")
		err = os.Chdir(parsedArgs.Chdir)
		if err != nil {
			logger.Fatal().
				Str("dir", parsedArgs.Chdir).
				Err(err).
				Msg("Changing working directory failed")
		}

		wd, err = os.Getwd()
		if err != nil {
			log.Fatal().
				Err(err).
				Msg("Getwd() failed")
		}
	}

	wd, err = filepath.EvalSymlinks(wd)
	if err != nil {
		log.Fatal().
			Err(err).
			Msg("EvalSymlinks() failed")
	}

	logger.Trace().
		Msg("Running in directory")

	prj, foundRoot, err := lookupProject(wd)
	if err != nil {
		logger.Fatal().
			Err(err).
			Msg("failed to lookup project root")
	}

	if !foundRoot {
		logger.Fatal().
			Msg("project root not found")
	}

	logger.Trace().Msg("Set defaults from parsed command line arguments.")

	err = prj.setDefaults(&parsedArgs)
	if err != nil {
		logger.Fatal().
			Err(err).
			Msg("setting configuration")
	}

	if parsedArgs.Changed && !prj.isRepo {
		logger.Fatal().
			Msg("flag --changed provided but no git repository found")
	}

	return &cli{
		stdin:      stdin,
		stdout:     stdout,
		stderr:     stderr,
		parsedArgs: &parsedArgs,
		ctx:        ctx,
		prj:        prj,
	}
}

func (c *cli) run() {
	if c.exit {
		// WHY: parser called exit but with no error (like help)
		return
	}

	logger := log.With().
		Str("action", "run()").
		Str("cmd", c.ctx.Command()).
		Str("workingDir", c.wd()).
		Logger()

	c.checkVersion()

	if c.parsedArgs.Changed {
		logger.Trace().Msg("`Changed` flag was set.")

		logger.Trace().Msg("Create new git wrapper.")

		git, err := newGit(c.root(), true)
		if err != nil {
			log.Fatal().
				Err(err).
				Msg("creating git wrapper.")
		}

		logger.Trace().Msg("Check git default branch was updated.")

		if err := c.prj.checkLocalDefaultIsUpdated(git); err != nil {
			log.Fatal().
				Err(err).
				Msg("checking git default branch was updated.")
		}
	}

	logger.Debug().Msg("Handle command.")

	switch c.ctx.Command() {
	case "list":
		log.Trace().
			Str("actionContext", "cli()").
			Msg("Print list of stacks.")
		c.printStacks()
	case "run":
		logger.Debug().Msg("Handle `run` command.")

		if len(c.parsedArgs.Run.Command) == 0 {
			log.Fatal().Msg("no command specified")
		}
		fallthrough
	case "run <cmd>":
		logger.Debug().Msg("Handle `run <cmd>` command.")

		c.runOnStacks()
	case "generate":
		report := generate.Do(c.loader(), c.root(), c.wd())
		c.log(report.String())

		if report.HasFailures() {
			os.Exit(1)
		}
	case "experimental globals":
		c.printStacksGlobals()
	case "experimental metadata":
		c.printMetadata()
	case "experimental init-stack <paths>":
		c.initStack(c.parsedArgs.Experimental.InitStack.StackDirs)
	case "experimental init-stack":
		c.initStack([]string{c.wd()})
	case "experimental run-graph":
		c.generateGraph()
	case "experimental run-order":
		c.printRunOrder()
	default:
		log.Fatal().Msg("unexpected command sequence")
	}
}

func (c *cli) initStack(dirs []string) {
	var errmsgs []string

	logger := log.With().
		Str("action", "initStack()").
		Logger()

	logger.Debug().
		Msg("Init stacks.")
	for _, d := range dirs {
		if !filepath.IsAbs(d) {
			log.Trace().
				Str("stack", fmt.Sprintf("%s%s", c.wd(), strings.Trim(d, "."))).
				Msg("Make file path absolute.")
			d = filepath.Join(c.wd(), d)
		}

		log.Debug().
			Str("stack", fmt.Sprintf("%s%s", c.wd(), strings.Trim(d, "."))).
			Msg("Init stack.")

		err := terramate.Init(c.loader(), c.root(), d)
		if err != nil {
			c.logerr("warn: failed to initialize stack: %v", err)
			errmsgs = append(errmsgs, err.Error())
		}
	}

	if len(errmsgs) > 0 {
		log.Fatal().
			Err(errors.E(ErrInit)).
			Send()
	}
}

func (c *cli) gitSafeguards(checks terramate.RepoChecks, shouldAbort bool) {
	logger := log.With().
		Str("action", "gitSafeguards()").
		Logger()

	if c.parsedArgs.Run.DryRun {
		return
	}

	if !c.parsedArgs.DisableCheckGitUntracked && len(checks.UntrackedFiles) > 0 {
		if shouldAbort {
			logger.Fatal().
				Strs("files", checks.UntrackedFiles).
				Msg("repository has untracked files")
		} else {
			logger.Warn().
				Strs("files", checks.UntrackedFiles).
				Msg("repository has untracked files")
		}

	}

	if !c.parsedArgs.DisableCheckGitUncommitted && len(checks.UncommittedFiles) > 0 {
		if shouldAbort {
			logger.Fatal().
				Strs("files", checks.UncommittedFiles).
				Msg("repository has uncommitted files")
		} else {
			logger.Warn().
				Strs("files", checks.UncommittedFiles).
				Msg("repository has uncommitted files")
		}
	}
}

func (c *cli) listStacks(mgr *terramate.Manager, isChanged bool) (*terramate.StacksReport, error) {
	if isChanged {
		log.Trace().
			Str("action", "listStacks()").
			Str("workingDir", c.wd()).
			Msg("`Changed` flag was set. List changed stacks.")
		return mgr.ListChanged()
	}
	return mgr.List()
}

func (c *cli) printStacks() {
	logger := log.With().
		Str("action", "printStacks()").
		Logger()

	if c.parsedArgs.List.Why && !c.parsedArgs.Changed {
		logger.Fatal().
			Msg("the --why flag must be used together with --changed")
	}

	logger.Trace().
		Str("workingDir", c.wd()).
		Msg("Create a new stack manager.")
	mgr := terramate.NewManager(c.loader(), c.root(), c.prj.baseRef)

	logger.Trace().
		Str("workingDir", c.wd()).
		Msg("Get stack list.")
	report, err := c.listStacks(mgr, c.parsedArgs.Changed)
	if err != nil {
		logger.Fatal().
			Err(err).
			Msg("listing stacks")
	}

	c.gitSafeguards(report.Checks, false)

	logger.Trace().
		Str("workingDir", c.wd()).
		Msg("Print stacks.")

	for _, entry := range report.Stacks {
		stack := entry.Stack
		stackRepr, ok := c.friendlyFmtDir(stack.Path())
		if !ok {
			continue
		}

		logger.Debug().
			Stringer("stack", stack).
			Msg("Print stack.")

		if c.parsedArgs.List.Why {
			c.log("%s - %s", stackRepr, entry.Reason)
		} else {
			c.log(stackRepr)
		}
	}
}

func (c *cli) generateGraph() {
	var getLabel func(s stack.S) string

	logger := log.With().
		Str("action", "generateGraph()").
		Str("workingDir", c.wd()).
		Logger()

	logger.Trace().Msg("Handle graph label command line argument.")

	switch c.parsedArgs.Experimental.RunGraph.Label {
	case "stack.name":
		logger.Debug().Msg("Set label to stack name.")

		getLabel = func(s stack.S) string { return s.Name() }
	case "stack.dir":
		logger.Debug().Msg("Set label stack directory.")

		getLabel = func(s stack.S) string { return s.Path() }
	default:
		logger.Fatal().
			Msg("-label expects the values \"stack.name\" or \"stack.dir\"")
	}

	entries, err := terramate.ListStacks(c.loader(), c.root())
	if err != nil {
		logger.Fatal().
			Err(err).
			Msg("listing stacks.")
	}

	logger.Debug().Msg("Create new graph.")

	loader := stack.NewLoader(c.root())
	dotGraph := dot.NewGraph(dot.Directed)
	graph := dag.New()

	visited := map[string]struct{}{}
	for _, e := range c.filterStacksByWorkingDir(entries) {
		if _, ok := visited[e.Stack.Path()]; ok {
			continue
		}

		err := run.BuildDAG(graph, c.root(), e.Stack, loader, visited)
		if err != nil {
			log.Fatal().
				Err(err).
				Msg("failed to build order tree")
		}
	}

	for _, id := range graph.IDs() {
		val, err := graph.Node(id)
		if err != nil {
			log.Fatal().
				Err(err).
				Msg("generating graph")
		}

		generateDot(dotGraph, graph, id, val.(stack.S), getLabel)
	}

	logger.Debug().
		Msg("Set output of graph.")
	outFile := c.parsedArgs.Experimental.RunGraph.Outfile
	var out io.Writer
	if outFile == "" {
		logger.Trace().
			Msg("Set output to stdout.")
		out = c.stdout
	} else {
		logger.Trace().
			Msg("Set output to file.")
		f, err := os.Create(outFile)
		if err != nil {
			log.Fatal().
				Str("path", outFile).
				Err(err).
				Msg("opening file")
		}

		defer func() {
			if err := f.Close(); err != nil {
				log.Fatal().
					Err(err).
					Msg("closing output graph file")
			}
		}()

		out = f
	}

	logger.Debug().
		Msg("Write graph to output.")
	_, err = out.Write([]byte(dotGraph.String()))
	if err != nil {
		log.Fatal().
			Str("path", outFile).
			Err(err).
			Msg("writing output")
	}
}

func generateDot(
	dotGraph *dot.Graph,
	graph *dag.DAG,
	id dag.ID,
	stackval stack.S,
	getLabel func(s stack.S) string,
) {
	logger := log.With().
		Str("action", "generateDot()").
		Logger()

	parent := dotGraph.Node(getLabel(stackval))
	for _, childid := range graph.ChildrenOf(id) {
		val, err := graph.Node(childid)
		if err != nil {
			logger.Fatal().
				Err(err).
				Msg("generating dot file")
		}
		s := val.(stack.S)
		n := dotGraph.Node(getLabel(s))

		edges := dotGraph.FindEdges(parent, n)
		if len(edges) == 0 {
			edge := dotGraph.Edge(parent, n)
			if graph.HasCycle(childid) {
				edge.Attr("color", "red")
				continue
			}
		}

		if graph.HasCycle(childid) {
			continue
		}

		generateDot(dotGraph, graph, childid, s, getLabel)
	}
}

func (c *cli) printRunOrder() {
	logger := log.With().
		Str("action", "printRunOrder()").
		Str("workingDir", c.wd()).
		Logger()

	stacks, err := c.computeSelectedStacks(false)
	if err != nil {
		logger.Fatal().
			Err(err).
			Msgf("computing selected stacks")
	}

	logger.Debug().Msg("Get run order.")
	orderedStacks, reason, err := run.Sort(c.root(), stacks)
	if err != nil {
		if errors.IsKind(err, dag.ErrCycleDetected) {
			log.Fatal().
				Err(err).
				Str("reason", reason).
				Msg("running on order")
		} else {
			log.Fatal().
				Err(err).
				Msg("failed to plan execution")
		}
	}

	for _, s := range orderedStacks {
		c.log(s.Name())
	}
}

func (c *cli) printStacksGlobals() {
	logger := log.With().
		Str("action", "printStacksGlobals()").
		Logger()

	logger.Trace().
		Msg("Create new terramate manager.")

	mgr := terramate.NewManager(c.loader(), c.root(), c.prj.baseRef)
	report, err := c.listStacks(mgr, c.parsedArgs.Changed)
	if err != nil {
		logger.Fatal().
			Err(err).
			Msg("listing stacks")
	}

	for _, stackEntry := range c.filterStacksByWorkingDir(report.Stacks) {
		meta := stack.Metadata(stackEntry.Stack)
		globals, err := stack.LoadGlobals(c.root(), meta)
		if err != nil {
			log.Fatal().
				Err(err).
				Str("stack", meta.Path()).
				Msg("listing stacks globals: loading stack")
		}

		globalsStrRepr := globals.String()
		if globalsStrRepr == "" {
			continue
		}

		c.log("\nstack %q:", meta.Path())
		for _, line := range strings.Split(globalsStrRepr, "\n") {
			c.log("\t%s", line)
		}
	}
}

func (c *cli) printMetadata() {
	logger := log.With().
		Str("action", "printMetadata()").
		Logger()

	logger.Trace().
		Msg("Create new terramate manager.")

	mgr := terramate.NewManager(c.loader(), c.root(), c.prj.baseRef)
	report, err := c.listStacks(mgr, c.parsedArgs.Changed)
	if err != nil {
		logger.Fatal().
			Err(err).
			Msg("listing stacks")
	}

	stackEntries := c.filterStacksByWorkingDir(report.Stacks)

	if len(stackEntries) == 0 {
		return
	}

	c.log("Available metadata:")

	for _, stackEntry := range stackEntries {
		stackMeta := stack.Metadata(stackEntry.Stack)

		logger.Debug().
			Stringer("stack", stackEntry.Stack).
			Msg("Print metadata for individual stack.")

		c.log("\nstack %q:", stackMeta.Path())
		c.log("\tterramate.name=%q", stackMeta.Name())
		c.log("\tterramate.path=%q", stackMeta.Path())
		c.log("\tterramate.description=%q", stackMeta.Desc())
	}
}

func (c *cli) checkOutdatedGeneratedCode(stacks []stack.S) {
	logger := log.With().
		Str("action", "checkOutdatedGeneratedCode()").
		Logger()

	if c.parsedArgs.Run.DisableCheckGenCode {
		logger.Trace().Msg("Outdated generated code check is disabled.")
		return
	}

	logger.Trace().Msg("Checking if any stack has outdated code.")

	hasOutdated := false
	for _, stack := range stacks {
		logger := logger.With().
			Stringer("stack", stack).
			Logger()

		logger.Trace().Msg("checking stack for outdated code")

		outdated, err := generate.CheckStack(c.root(), stack)
		if err != nil {
			logger.Fatal().Err(err).Msg("checking stack for outdated code")
		}

		if len(outdated) > 0 {
			hasOutdated = true
		}

		for _, filename := range outdated {
			logger.Error().
				Str("filename", filename).
				Msg("outdated code found")
		}
	}

	if hasOutdated {
		logger.Fatal().
			Err(errors.E(ErrOutdatedGenCodeDetected)).
			Msg("please run: 'terramate generate' to update generated code")
	}
}

func (c *cli) runOnStacks() {
	logger := log.With().
		Str("action", "runOnStacks()").
		Str("workingDir", c.wd()).
		Logger()

	stacks, err := c.computeSelectedStacks(true)
	if err != nil {
		logger.Fatal().
			Err(err).
			Msgf("computing selected stacks")
	}

	c.checkOutdatedGeneratedCode(stacks)

	logger.Trace().Msg("Get order of stacks to run command on.")

	orderedStacks, reason, err := run.Sort(c.root(), stacks)
	if err != nil {
		if errors.IsKind(err, dag.ErrCycleDetected) {
			logger.Fatal().
				Str("reason", reason).
				Err(err).
				Msg("running in order")
		} else {
			log.Fatal().
				Err(err).
				Msg("failed to plan execution")
		}
	}

	if c.parsedArgs.Run.Reverse {
		logger.Trace().Msg("Reversing stacks order.")
		stack.Reverse(orderedStacks)
	}

	if c.parsedArgs.Run.DryRun {
		logger.Trace().
			Msg("Do a dry run - get order without actually running command.")
		if len(orderedStacks) > 0 {
			c.log("The stacks will be executed using order below:")

			for i, s := range orderedStacks {
				stackdir, _ := c.friendlyFmtDir(s.Path())
				c.log("\t%d. %s (%s)", i, s.Name(), stackdir)
			}
		} else {
			c.log("No stacks will be executed.")
		}

		return
	}

	logger.Info().Msg("Running on selected stacks")

	failed := false

	for _, stack := range orderedStacks {
		cmd := exec.Command(c.parsedArgs.Run.Command[0], c.parsedArgs.Run.Command[1:]...)
		cmd.Dir = stack.HostPath()
		cmd.Env = os.Environ()
		cmd.Stdin = c.stdin
		cmd.Stdout = c.stdout
		cmd.Stderr = c.stderr

		logger := log.With().
			Str("cmd", strings.Join(c.parsedArgs.Run.Command, " ")).
			Stringer("stack", stack).
			Logger()

		logger.Info().Msg("Running")

		err = cmd.Run()
		if err != nil {
			failed = true

			if c.parsedArgs.Run.ContinueOnError {
				logger.Warn().
					Err(err).
					Msg("failed to execute command")
			} else {
				logger.Fatal().
					Err(err).
					Msg("failed to execute command")
			}
		}
	}

	if failed {
		os.Exit(1)
	}
}

func (c *cli) wd() string            { return c.prj.wd }
func (c *cli) root() string          { return c.prj.root }
func (c *cli) loader() *stack.Loader { return c.prj.loader }

func (c *cli) log(format string, args ...interface{}) {
	fmt.Fprintln(c.stdout, fmt.Sprintf(format, args...))
}

func (c *cli) logerr(format string, args ...interface{}) {
	fmt.Fprintln(c.stderr, fmt.Sprintf(format, args...))
}

func (c *cli) friendlyFmtDir(dir string) (string, bool) {
	return prj.FriendlyFmtDir(c.root(), c.wd(), dir)
}

func (c *cli) computeSelectedStacks(ensureCleanRepo bool) ([]stack.S, error) {
	logger := log.With().
		Str("action", "computeSelectedStacks()").
		Str("workingDir", c.wd()).
		Logger()

	logger.Trace().Msg("Create new terramate manager.")

	mgr := terramate.NewManager(c.loader(), c.root(), c.prj.baseRef)

	logger.Trace().Msg("Get list of stacks.")

	report, err := c.listStacks(mgr, c.parsedArgs.Changed)
	if err != nil {
		return nil, err
	}

	c.gitSafeguards(report.Checks, ensureCleanRepo)

	logger.Trace().Msg("Filter stacks by working directory.")

	entries := c.filterStacksByWorkingDir(report.Stacks)
	stacks := make([]stack.S, len(entries))
	for i, e := range entries {
		stacks[i] = e.Stack
	}

	stacks, err = mgr.AddWantedOf(stacks)
	if err != nil {
		return nil, fmt.Errorf("adding wanted stacks: %w", err)
	}

	return stacks, nil
}

func (c *cli) filterStacksByWorkingDir(stacks []terramate.Entry) []terramate.Entry {
	logger := log.With().
		Str("action", "filterStacksByWorkingDir()").
		Str("workingDir", c.wd()).
		Logger()

	logger.Trace().
		Msg("Get relative working directory.")
	relwd := prj.PrjAbsPath(c.root(), c.wd())

	logger.Trace().
		Msg("Get filtered stacks.")
	filtered := []terramate.Entry{}
	for _, e := range stacks {
		if strings.HasPrefix(e.Stack.Path(), relwd) {
			filtered = append(filtered, e)
		}
	}

	return filtered
}

func (c cli) checkVersion() {
	logger := log.With().
		Str("action", "cli.checkVersion()").
		Str("root", c.root()).
		Logger()

	logger.Trace().Msg("checking if terramate version satisfies project constraint")

	rootcfg := c.prj.rootcfg

	if rootcfg.Terramate == nil {
		logger.Debug().Msg("project root has no config, skipping version check")
		return
	}

	if rootcfg.Terramate.RequiredVersion == "" {
		logger.Debug().Msg("project root config has no required_version, skipping version check")
		return
	}

	if err := terramate.CheckVersion(rootcfg.Terramate.RequiredVersion); err != nil {
		logger.Fatal().Err(err).Send()
	}
}

func newGit(basedir string, checkrepo bool) (*git.Git, error) {
	log.Debug().
		Str("action", "newGit()").
		Msg("Create new git wrapper providing config.")
	g, err := git.WithConfig(git.Config{
		WorkingDir: basedir,
		Env:        os.Environ(),
	})

	if err != nil {
		return nil, err
	}

	if checkrepo && !g.IsRepository() {
		return nil, fmt.Errorf("dir %q is not a git repository", basedir)
	}

	return g, nil
}

func lookupProject(wd string) (prj project, found bool, err error) {
	prj = project{
		wd: wd,
	}

	logger := log.With().
		Str("action", "lookupProject()").
		Str("workingDir", wd).
		Logger()

	logger.Trace().Msg("Create new git wrapper.")

	gw, err := newGit(wd, false)
	if err == nil {
		logger.Trace().Msg("Get root of git repo.")

		gitdir, err := gw.Root()
		if err == nil {
			logger.Trace().Msg("Get absolute path of git directory.")

			gitabs, err := filepath.Abs(gitdir)
			if err != nil {
				return project{}, false, fmt.Errorf("getting absolute path of %q: %w", gitdir, err)
			}

			logger.Trace().Msg("Evaluate symbolic links.")

			gitabs, err = filepath.EvalSymlinks(gitabs)
			if err != nil {
				return project{}, false, fmt.Errorf("failed evaluating symlinks of %q: %w",
					gitabs, err)
			}

			root := filepath.Dir(gitabs)

			logger.Trace().Msg("Load root config.")

			cfg, err := hcl.ParseDir(root)
			if err != nil {
				return project{}, false, err
			}

			prj.isRepo = true
			prj.rootcfg = cfg
			prj.root = root

			return prj, true, nil
		}
	}

	dir := wd

	for {
		logger.Trace().Msg("Load root config.")

		cfg, ok, err := config.TryLoadRootConfig(dir)
		if err != nil {
			return project{}, false, err
		}

		if ok {
			prj.root = dir
			prj.rootcfg = cfg

			return prj, true, nil
		}

		if dir == "/" {
			break
		}

		dir = filepath.Dir(dir)
	}

	return project{}, false, nil
}

func configureLogging(logLevel string, logFmt string, output io.Writer) {
	zloglevel, err := zerolog.ParseLevel(logLevel)

	if err != nil {
		zloglevel = zerolog.FatalLevel
	}

	zerolog.SetGlobalLevel(zloglevel)

	if logFmt == "json" {
		zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
		log.Logger = log.Output(output)
	} else if logFmt == "text" { // no color
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: output, NoColor: true, TimeFormat: time.RFC3339})
	} else { // default: console mode using color
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: output, NoColor: false, TimeFormat: time.RFC3339})
	}
}
