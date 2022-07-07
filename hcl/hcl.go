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

package hcl

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/mineiros-io/terramate/errors"
	"github.com/mineiros-io/terramate/hcl/ast"
	"github.com/mineiros-io/terramate/hcl/eval"
	"github.com/rs/zerolog/log"
	"github.com/zclconf/go-cty/cty"
)

// Errors returned during the HCL parsing.
const (
	ErrHCLSyntax       errors.Kind = "HCL syntax error"
	ErrTerramateSchema errors.Kind = "terramate schema error"
	ErrImport          errors.Kind = "import error"
)

// Config represents a Terramate configuration.
type Config struct {
	Terramate *Terramate
	Stack     *Stack

	// absdir is the absolute path to the configuration directory.
	absdir string
}

// RunConfig represents Terramate run configuration.
type RunConfig struct {
	Env *RunEnv
}

// RunEnv represents Terramate run environment.
type RunEnv struct {
	// Attributes is the collection of attribute definitions within the env block.
	Attributes ast.Attributes
}

// GitConfig represents Terramate Git configuration.
type GitConfig struct {
	// DefaultBranchBaseRef is the baseRef when in default branch.
	DefaultBranchBaseRef string

	// DefaultBranch is the default branch.
	DefaultBranch string

	// DefaultRemote is the default remote.
	DefaultRemote string

	// DisableCheckUntracked disables untracked files checking.
	DisableCheckUntracked bool

	// DisableCheckUncommitted disables uncommitted files checking.
	DisableCheckUncommitted bool

	// DisableCheckRemote disables checking if local default branch is updated with remote.
	DisableCheckRemote bool
}

// RootConfig represents the root config block of a Terramate configuration.
type RootConfig struct {
	Git *GitConfig
	Run *RunConfig
}

// Terramate is the parsed "terramate" HCL block.
type Terramate struct {
	// RequiredVersion contains the terramate version required by the stack.
	RequiredVersion string

	// Config is the parsed config blocks.
	Config *RootConfig
}

// StackID represents the stack ID. Its zero value represents an undefined ID.
type StackID struct {
	id *string
}

// Stack is the parsed "stack" HCL block.
type Stack struct {
	// ID of the stack. If the ID is nil it indicates this stack has no ID.
	ID StackID

	// Name of the stack
	Name string

	// Description of the stack
	Description string

	// After is a list of non-duplicated stack entries that must run before the
	// current stack runs.
	After []string

	// Before is a list of non-duplicated stack entries that must run after the
	// current stack runs.
	Before []string

	// Wants is a list of non-duplicated stack entries that must be selected
	// whenever the current stack is selected.
	Wants []string

	// Watch is a list of files to be watched for changes.
	Watch []string
}

// GenHCLBlock represents a parsed generate_hcl block.
type GenHCLBlock struct {
	// Origin is the filename where this block is defined.
	Origin string
	// Label of the block.
	Label string
	// Content block.
	Content *hclsyntax.Block
	// Condition attribute of the block, if any.
	Condition *hclsyntax.Attribute
}

// GenFileBlock represents a parsed generate_file block
type GenFileBlock struct {
	// Origin is the filename where this block is defined.
	Origin string
	// Label of the block
	Label string
	// Content attribute of the block
	Content *hclsyntax.Attribute
	// Condition attribute of the block, if any.
	Condition *hclsyntax.Attribute
}

// PartialEvaluator represents an HCL partial evaluator
type PartialEvaluator func(hclsyntax.Expression) (hclwrite.Tokens, error)

// TerramateParser is an HCL parser tailored for Terramate configuration schema.
// As the Terramate configuration can span multiple files in the same directory,
// this API allows you to define the exact set of files (and contents) that are
// going to be included in the final configuration.
type TerramateParser struct {
	rootdir   string
	dir       string
	files     map[string][]byte // path=content
	hclparser *hclparse.Parser

	evalctx *eval.Context

	// MergedAttributes are the top-level attributes of all files.
	MergedAttributes ast.Attributes

	// MergedBlocks are the merged blocks from all files.
	MergedBlocks ast.MergedBlocks

	// Blocks are the unmerged blocks from all files.
	Blocks ast.Blocks

	// parsedFiles stores a map of all parsed files
	parsedFiles map[string]parsedFile

	// if true, calling Parse() or MinimalParse() will fail.
	parsed bool
}

var stackIDRegex = regexp.MustCompile("^[a-zA-Z0-9_-]{1,64}$")

// NewStackID creates a new StackID with the given string as its id.
// It guarantees that the id passed is a valid StackID value,
// an error is returned otherwise.
func NewStackID(id string) (StackID, error) {
	if !stackIDRegex.MatchString(id) {
		return StackID{}, errors.E("Stack ID %q doesn't match %q", id, stackIDRegex)
	}
	return StackID{id: &id}, nil
}

// Value returns the ID string value and true if this StackID is defined,
// it returns "" and false otherwise.
func (s StackID) Value() (string, bool) {
	if s.id == nil {
		return "", false
	}
	return *s.id, true
}

// parsedFile tells the origin and kind of the parsedFile.
// The kind can be either internal or external, meaning the file was parsed
// by this parser or by another parser instance, respectively.
type parsedFile struct {
	kind   parsedKind
	origin string
}

type parsedKind int

const (
	_ parsedKind = iota
	internal
	external
)

type mergeHandler func(block *ast.Block) error

// NewTerramateParser creates a Terramate parser for the directory dir inside
// the root directory.
// The parser creates sub-parsers for parsing imports but keeps a list of all
// parsed files of all sub-parsers for detecting cycles and import duplications.
// Calling Parse() or MinimalParse() multiple times is an error.
func NewTerramateParser(rootdir string, dir string) (*TerramateParser, error) {
	if !strings.HasPrefix(dir, rootdir) {
		return nil, errors.E("directory %q is not inside root %q", dir, rootdir)
	}

	return &TerramateParser{
		rootdir:          rootdir,
		dir:              dir,
		files:            map[string][]byte{},
		hclparser:        hclparse.NewParser(),
		MergedAttributes: make(ast.Attributes),
		MergedBlocks:     make(ast.MergedBlocks),
		parsedFiles:      make(map[string]parsedFile),
		evalctx:          eval.NewContext(dir),
	}, nil
}

func (p *TerramateParser) addParsedFile(origin string, kind parsedKind, files ...string) {
	for _, file := range files {
		p.parsedFiles[file] = parsedFile{
			kind:   kind,
			origin: origin,
		}
	}
}

// AddDir walks over all the files in the directory dir and add all .tm and
// .tm.hcl files to the parser.
func (p *TerramateParser) AddDir(dir string) error {
	logger := log.With().
		Str("action", "parser.AddDir()").
		Str("dir", dir).
		Logger()

	tmFiles, err := listTerramateFiles(dir)
	if err != nil {
		return errors.E(err, "adding directory to terramate parser")
	}

	for _, filename := range tmFiles {
		path := filepath.Join(dir, filename)
		logger.Trace().
			Str("file", path).
			Msg("Reading config file.")

		data, err := os.ReadFile(path)
		if err != nil {
			return errors.E(err, "reading config file %q", path)
		}

		if err := p.AddFileContent(path, data); err != nil {
			return err
		}

		logger.Trace().Msg("file added")
	}

	return nil
}

// AddFile adds a file path to be parsed.
func (p *TerramateParser) AddFile(path string) error {
	if !strings.HasPrefix(path, p.dir) {
		return errors.E("parser only allow files from directory %q", p.dir)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.E("adding file %q to parser", path, err)
	}
	return p.AddFileContent(path, data)
}

// AddFileContent adds a file to the set of files to be parsed.
func (p *TerramateParser) AddFileContent(name string, data []byte) error {
	if !strings.HasPrefix(name, p.dir) {
		return errors.E("parser only allow files from directory %q", p.dir)
	}
	if _, ok := p.files[name]; ok {
		return errors.E(os.ErrExist, "adding file %q to the parser", name)
	}

	p.files[name] = data
	return nil
}

// Parse the previously added files and return either a Config or an error.
func (p *TerramateParser) Parse() (Config, error) {
	err := p.MinimalParse()
	if err != nil {
		return Config{}, err
	}

	// TODO(i4k): don't validate schema here.
	// Changing this requires changes to the editor extensions / linters / etc.
	return p.parseTerramateSchema()
}

// MinimalParse does the syntax parsing and merging of configurations but do not
// validate if it's valid terramate configuration.
func (p *TerramateParser) MinimalParse() error {
	if p.parsed {
		return errors.E("files already parsed")
	}
	defer func() { p.parsed = true }()

	err := p.parseSyntax()
	if err != nil {
		return err
	}

	errs := errors.L()
	errs.Append(p.mergeConfig())
	errs.Append(p.applyImports())
	return errs.AsError()
}

// Imports returns all import blocks parsed.
func (p *TerramateParser) Imports() (ast.Blocks, error) {
	errs := errors.L()
	imports := ast.Blocks{}

	for _, importBlock := range filterBlocksByType("import", p.Blocks) {
		err := validateImportBlock(importBlock)
		errs.Append(err)
		if err == nil {
			imports = append(imports, importBlock)
		}
	}
	if err := errs.AsError(); err != nil {
		return nil, err
	}
	return imports, nil
}

func (p *TerramateParser) mergeHandlers() map[string]mergeHandler {
	return map[string]mergeHandler{
		"terramate":     p.mergeBlock,
		"globals":       p.mergeBlock,
		"stack":         p.addBlock,
		"generate_file": p.addBlock,
		"generate_hcl":  p.addBlock,
		"import":        p.addBlock,
	}
}

func (p *TerramateParser) mergeBlocks(blocks ast.Blocks) error {
	handlers := p.mergeHandlers()

	errs := errors.L()
	for _, block := range blocks {
		handler, ok := handlers[block.Type]
		if !ok {
			errs.Append(
				errors.E(ErrTerramateSchema, block.DefRange(),
					"unrecognized block %q", block.Type),
			)

			continue
		}

		errs.Append(handler(block))
	}
	return errs.AsError()
}

func (p *TerramateParser) addBlock(block *ast.Block) error {
	p.Blocks = append(p.Blocks, block)
	return nil
}

func (p *TerramateParser) mergeBlock(block *ast.Block) error {
	if other, ok := p.MergedBlocks[block.Type]; ok {
		err := other.MergeBlock(block)
		if err != nil {
			return errors.E(ErrTerramateSchema, err)
		}
		return nil
	}

	merged := ast.NewMergedBlock(block.Type)
	p.MergedBlocks[block.Type] = merged
	err := merged.MergeBlock(block)
	if err != nil {
		return errors.E(ErrTerramateSchema, err)
	}
	return nil
}

func (p *TerramateParser) mergeConfig() error {
	errs := errors.L()

	bodies := p.ParsedBodies()
	for _, origin := range p.sortedParsedFilenames() {
		body := bodies[origin]

		errs.Append(p.mergeAttrs(ast.NewAttributes(origin, body.Attributes)))
		errs.Append(p.mergeBlocks(ast.NewBlocks(origin, body.Blocks)))
	}
	return errs.AsError()
}

func (p *TerramateParser) mergeAttrs(other ast.Attributes) error {
	errs := errors.L()
	for _, attr := range other.SortedList() {
		if _, ok := p.MergedAttributes[attr.Name]; ok {
			errs.Append(errors.E(ErrTerramateSchema,
				attr.NameRange,
				"attribute %q redeclared", attr.Name))
			continue
		}

		p.MergedAttributes[attr.Name] = attr
	}
	return errs.AsError()
}

func (p *TerramateParser) parseSyntax() error {
	errs := errors.L()
	for _, name := range p.sortedFilenames() {
		data := p.files[name]
		_, diags := p.hclparser.ParseHCL(data, name)
		if diags.HasErrors() {
			errs.Append(errors.E(ErrHCLSyntax, diags))
			continue
		}
		p.addParsedFile(p.dir, internal, name)
	}
	return errs.AsError()
}

func (p *TerramateParser) applyImports() error {
	importBlocks, err := p.Imports()
	if err != nil {
		return err
	}

	errs := errors.L()
	for _, importBlock := range importBlocks {
		errs.Append(p.handleImport(importBlock))
	}
	return errs.AsError()
}

func (p *TerramateParser) handleImport(importBlock *ast.Block) error {
	srcAttr := importBlock.Attributes["source"]
	srcVal, diags := srcAttr.Expr.Value(nil)
	if diags.HasErrors() {
		return errors.E(ErrTerramateSchema, srcAttr.Expr.Range(),
			"failed to evaluate import.source")
	}

	if srcVal.Type() != cty.String {
		return errors.E(ErrTerramateSchema, srcAttr.Expr.Range(),
			"import.source must be a string")
	}

	src := srcVal.AsString()
	srcBase := filepath.Base(src)
	srcDir := filepath.Dir(src)
	if filepath.IsAbs(srcDir) { // project-path
		srcDir = filepath.Join(p.rootdir, srcDir)
	} else {
		srcDir = filepath.Join(p.dir, srcDir)
	}

	if srcDir == p.dir {
		return errors.E(ErrImport, srcAttr.Expr.Range(),
			"importing files in the same directory is not permitted")
	}

	if strings.HasPrefix(p.dir, srcDir) {
		return errors.E(ErrImport, srcAttr.Expr.Range(),
			"importing files in the same tree is not permitted")
	}

	src = filepath.Join(srcDir, srcBase)

	if _, ok := p.parsedFiles[src]; ok {
		return errors.E(ErrImport, srcAttr.Expr.Range(),
			"file %q already parsed", src)
	}

	importParser, err := NewTerramateParser(p.rootdir, srcDir)
	if err != nil {
		return errors.E(ErrImport, srcAttr.Expr.Range(),
			err, "failed to create sub parser")
	}
	err = importParser.AddFile(src)
	if err != nil {
		return errors.E(ErrImport, srcAttr.Expr.Range(),
			err)
	}
	importParser.addParsedFile(p.dir, external, p.internalParsedFiles()...)
	err = importParser.MinimalParse()
	if err != nil {
		return err
	}
	errs := errors.L()
	for _, block := range importParser.Blocks {
		if block.Type == "stack" {
			errs.Append(
				errors.E(ErrImport, srcAttr.Expr.Range(),
					"import of stack block is not permitted"))
		}
	}
	errs.Append(p.mergeAttrs(importParser.MergedAttributes))
	errs.Append(p.mergeBlocks(importParser.MergedBlocks.AsBlocks()))
	errs.Append(p.mergeBlocks(importParser.Blocks))
	if err := errs.AsError(); err != nil {
		return errors.E(ErrImport, err, "failed to merge imported configuration")
	}

	p.addParsedFile(p.dir, external, src)
	return nil
}

// ParsedBodies returns a map of filename to the parsed hclsyntax.Body.
func (p *TerramateParser) ParsedBodies() map[string]*hclsyntax.Body {
	parsed := make(map[string]*hclsyntax.Body)
	bodyMap := p.hclparser.Files()
	for _, filename := range p.internalParsedFiles() {
		hclfile := bodyMap[filename]
		// A cast error here would be a severe programming error on Terramate
		// side, so we are by design allowing the cast to panic
		parsed[filename] = hclfile.Body.(*hclsyntax.Body)
	}
	return parsed
}

func (p *TerramateParser) sortedFilenames() []string {
	filenames := []string{}
	for fname := range p.files {
		filenames = append(filenames, fname)
	}
	sort.Strings(filenames)
	return filenames
}

func (p *TerramateParser) sortedParsedFilenames() []string {
	filenames := append([]string{}, p.internalParsedFiles()...)
	sort.Strings(filenames)
	return filenames
}

func (p *TerramateParser) internalParsedFiles() []string {
	filenames := []string{}
	for fname, parsed := range p.parsedFiles {
		if parsed.kind == internal {
			filenames = append(filenames, fname)
		}
	}
	sort.Strings(filenames)
	return filenames
}

// NewConfig creates a new HCL config with dir as config directory path.
func NewConfig(dir string) (Config, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return Config{}, errors.E(err, "initializing config")
	}

	if !st.IsDir() {
		return Config{}, errors.E("config constructor requires a directory path")
	}

	return Config{
		absdir: dir,
	}, nil
}

// HasRunEnv returns true if the config has a terramate.config.run.env block defined
func (c Config) HasRunEnv() bool {
	return c.Terramate != nil &&
		c.Terramate.Config != nil &&
		c.Terramate.Config.Run != nil &&
		c.Terramate.Config.Run.Env != nil
}

// AbsDir returns the absolute path of the configuration directory.
func (c Config) AbsDir() string { return c.absdir }

// IsEmpty returns true if the config is empty, false otherwise.
func (c Config) IsEmpty() bool {
	return c.Stack == nil && c.Terramate == nil
}

// Save the configuration file using filename inside config directory.
func (c Config) Save(filename string) (err error) {
	cfgpath := filepath.Join(c.absdir, filename)
	f, err := os.Create(cfgpath)
	if err != nil {
		return errors.E(err, "saving configuration file %q", cfgpath)
	}

	defer func() {
		err2 := f.Close()

		if err != nil {
			return
		}

		err = err2
	}()

	return PrintConfig(f, c)
}

// NewTerramate creates a new TerramateBlock with reqversion.
func NewTerramate(reqversion string) *Terramate {
	return &Terramate{
		RequiredVersion: reqversion,
	}
}

// ParseDir will parse Terramate configuration from a given directory,
// using root as project workspace, parsing all files with the suffixes .tm and
// .tm.hcl.
// Note: it does not recurse into child directories.
func ParseDir(root string, dir string) (Config, error) {
	logger := log.With().
		Str("action", "ParseDir()").
		Str("dir", dir).
		Logger()

	logger.Trace().Msg("Parsing configuration files")

	p, err := NewTerramateParser(root, dir)
	if err != nil {
		return Config{}, err
	}
	err = p.AddDir(dir)
	if err != nil {
		return Config{}, errors.E("adding files to parser", err)
	}
	return p.Parse()
}

// ParseGenerateHCLBlocks parses all Terramate files on the given dir, returning
// only generate_hcl blocks (other blocks are discarded).
// generate_hcl blocks are validated, so the caller can expect valid blocks only or an error.
func ParseGenerateHCLBlocks(root, dir string) ([]GenHCLBlock, error) {
	logger := log.With().
		Str("action", "hcl.ParseGenerateHCLBlocks").
		Str("configdir", dir).
		Logger()

	logger.Trace().Msg("loading config")

	blocks, err := parseUnmergedBlocks(root, dir, "generate_hcl", func(block *ast.Block) error {
		return validateGenerateHCLBlock(block)
	})
	if err != nil {
		return nil, err
	}

	var genhclBlocks []GenHCLBlock
	for _, block := range blocks {
		genhclBlocks = append(genhclBlocks, GenHCLBlock{
			Origin:    block.Origin,
			Label:     block.Labels[0],
			Content:   block.Body.Blocks[0],
			Condition: block.Body.Attributes["condition"],
		})
	}

	return genhclBlocks, nil
}

// ParseGenerateFileBlocks parses all Terramate files on the given dir, returning
// parsed generate_file blocks.
func ParseGenerateFileBlocks(root, dir string) ([]GenFileBlock, error) {
	blocks, err := parseUnmergedBlocks(root, dir, "generate_file", func(block *ast.Block) error {
		return validateGenerateFileBlock(block)
	})
	if err != nil {
		return nil, err
	}

	var genfileBlocks []GenFileBlock
	for _, block := range blocks {
		genfileBlocks = append(genfileBlocks, GenFileBlock{
			Origin:    block.Origin,
			Label:     block.Labels[0],
			Content:   block.Body.Attributes["content"],
			Condition: block.Body.Attributes["condition"],
		})
	}

	return genfileBlocks, nil
}

func validateImportBlock(block *ast.Block) error {
	errs := errors.L()
	if len(block.Labels) != 0 {
		errs.Append(errors.E(ErrTerramateSchema, block.LabelRanges[0],
			"import must have no labels but got %v",
			block.Labels,
		))
	}
	schema := &hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{
				Name:     "source",
				Required: true,
			},
		},
	}

	_, diags := block.Body.Content(schema)
	if diags.HasErrors() {
		errs.Append(errors.E(ErrTerramateSchema, diags))
	}
	return errs.AsError()
}

func validateGenerateHCLBlock(block *ast.Block) error {
	errs := errors.L()

	// Don't seem like we can use hcl.BodySchema to check for any non-empty
	// label, only specific label values.
	if len(block.Labels) != 1 {
		errs.Append(errors.E(ErrTerramateSchema, block.OpenBraceRange,
			"generate_hcl must have single label instead got %v",
			block.Labels,
		))
	} else if block.Labels[0] == "" {
		errs.Append(errors.E(ErrTerramateSchema, block.OpenBraceRange,
			"generate_hcl label can't be empty"))
	}
	// Schema check passes if no block is present, so check for amount of blocks
	if len(block.Body.Blocks) == 0 {
		errs.Append(errors.E(ErrTerramateSchema, block.Body.Range(),
			"generate_hcl must have one 'content' block"))
	} else if len(block.Body.Blocks) != 1 {
		errs.Append(errors.E(ErrTerramateSchema, block.Body.Range(),
			"generate_hcl must have one block of type 'content', found %d blocks",
			len(block.Body.Blocks)))
	}

	schema := &hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{
				Name:     "condition",
				Required: false,
			},
		},
		Blocks: []hcl.BlockHeaderSchema{
			{
				Type:       "content",
				LabelNames: []string{},
			},
		},
	}

	_, diags := block.Body.Content(schema)
	if diags.HasErrors() {
		errs.Append(errors.E(ErrTerramateSchema, diags))
	}
	return errs.AsError()
}

func validateGenerateFileBlock(block *ast.Block) error {
	errs := errors.L()
	if len(block.Labels) != 1 {
		errs.Append(errors.E(ErrTerramateSchema, block.OpenBraceRange,
			"generate_file must have single label instead got %v",
			block.Labels,
		))
	} else if block.Labels[0] == "" {
		errs.Append(errors.E(ErrTerramateSchema, block.OpenBraceRange,
			"generate_file label can't be empty"))
	}
	schema := &hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{
				Name:     "content",
				Required: true,
			},
			{
				Name:     "condition",
				Required: false,
			},
		},
	}

	_, diags := block.Body.Content(schema)
	if diags.HasErrors() {
		errs.Append(errors.E(ErrTerramateSchema, diags))
	}
	return errs.AsError()
}

// CopyBody will copy the src body to the given target, evaluating attributes using the
// given evaluation context.
//
// Scoped traversals, like name.traverse, for unknown namespaces will be copied
// as is (original expression form, no evaluation).
//
// Returns an error if the evaluation fails.
func CopyBody(target *hclwrite.Body, src *hclsyntax.Body, eval PartialEvaluator) error {
	logger := log.With().
		Str("action", "CopyBody()").
		Logger()

	logger.Trace().Msg("Sorting attributes.")

	attrs := ast.SortRawAttributes(src.Attributes)
	for _, attr := range attrs {
		logger := logger.With().
			Str("attrName", attr.Name).
			Logger()

		logger.Trace().Msg("evaluating.")
		tokens, err := eval(attr.Expr)
		if err != nil {
			return errors.E(err, attr.Expr.Range())
		}

		logger.Trace().Str("attribute", attr.Name).Msg("Setting evaluated attribute.")
		target.SetAttributeRaw(attr.Name, tokens)
	}

	logger.Trace().Msg("Append blocks.")

	for _, block := range src.Blocks {
		targetBlock := target.AppendNewBlock(block.Type, block.Labels)
		if block.Body == nil {
			continue
		}
		if err := CopyBody(targetBlock.Body(), block.Body, eval); err != nil {
			return err
		}
	}

	return nil
}

func assignSet(name string, target *[]string, val cty.Value) error {
	logger := log.With().
		Str("action", "assignSet()").
		Logger()

	if val.IsNull() {
		return nil
	}

	// as the parser is schemaless it only creates tuples (lists of arbitrary types).
	// we have to check the elements themselves.
	if !val.Type().IsTupleType() && !val.Type().IsListType() {
		return errors.E(ErrTerramateSchema, "field %q must be a set(string) but "+
			"found a %q", name, val.Type().FriendlyName())
	}

	logger.Trace().Msg("Iterate over values.")

	errs := errors.L()
	values := map[string]struct{}{}
	iterator := val.ElementIterator()
	index := -1
	for iterator.Next() {
		index++
		_, elem := iterator.Element()

		logger.Trace().Msg("Check element is of correct type.")

		if elem.Type() != cty.String {
			errs.Append(errors.E("field %q must be a set(string) but element %d "+
				"has type %q", name, index, elem.Type().FriendlyName()))

			continue
		}

		logger.Trace().Msg("Get element as string.")

		str := elem.AsString()
		if _, ok := values[str]; ok {
			errs.Append(errors.E("duplicated entry %q in the index %d of field %q"+
				" of type set(string)", str, name))

			continue
		}
		values[str] = struct{}{}
	}

	if err := errs.AsError(); err != nil {
		return err
	}

	var elems []string
	for v := range values {
		elems = append(elems, v)
	}

	logger.Trace().Msg("Sort elements.")

	sort.Strings(elems)
	*target = elems
	return nil
}

func parseStack(evalctx *eval.Context, stack *Stack, stackblock *ast.Block) error {
	logger := log.With().
		Str("action", "parseStack()").
		Str("stack", stack.Name).
		Logger()

	errs := errors.L()

	for _, block := range stackblock.Body.Blocks {
		errs.Append(
			errors.E(block.TypeRange, "unrecognized block %q", block.Type),
		)
	}

	logger.Debug().Msg("Get stack attributes.")

	for _, attr := range ast.SortRawAttributes(stackblock.Body.Attributes) {
		logger.Trace().Msg("Get attribute value.")

		attrVal, err := evalctx.Eval(attr.Expr)
		if err != nil {
			errs.Append(
				errors.E(err, "failed to evaluate %q attribute", attr.Name),
			)
			continue
		}

		logger.Trace().
			Str("attribute", attr.Name).
			Msg("Setting attribute on configuration.")

		switch attr.Name {
		case "id":
			if attrVal.Type() != cty.String {
				errs.Append(errors.E(attr.NameRange,
					"field stack.\"id\" must be a \"string\" but is %q",
					attrVal.Type().FriendlyName()),
				)
				continue
			}
			id, err := NewStackID(attrVal.AsString())
			if err != nil {
				errs.Append(errors.E(attr.NameRange, err))
				continue
			}
			logger.Trace().
				Str("id", attrVal.AsString()).
				Msg("found valid stack ID definition")
			stack.ID = id
		case "name":
			if attrVal.Type() != cty.String {
				errs.Append(errors.E(attr.NameRange,
					"field stack.\"name\" must be a \"string\" but given %q",
					attrVal.Type().FriendlyName()),
				)
				continue
			}
			stack.Name = attrVal.AsString()

		case "after":
			errs.Append(assignSet(attr.Name, &stack.After, attrVal))

		case "before":
			errs.Append(assignSet(attr.Name, &stack.Before, attrVal))

		case "wants":
			errs.Append(assignSet(attr.Name, &stack.Wants, attrVal))

		case "watch":
			errs.Append(assignSet(attr.Name, &stack.Watch, attrVal))

		case "description":
			logger.Trace().Msg("parsing stack description.")
			if attrVal.Type() != cty.String {
				errs.Append(errors.E(attr.Expr.Range(),
					"field stack.\"description\" must be a \"string\" but given %q",
					attrVal.Type().FriendlyName(),
				))

				continue
			}
			stack.Description = attrVal.AsString()

		default:
			errs.Append(errors.E(
				attr.NameRange, "unrecognized attribute stack.%q", attr.Name,
			))
		}
	}

	return errs.AsError()
}

func parseRootConfig(cfg *RootConfig, block *ast.MergedBlock) error {
	logger := log.With().
		Str("action", "parseRootConfig()").
		Logger()

	errs := errors.L()

	logger.Trace().Msg("Range over block attributes.")

	for _, attr := range block.Attributes.SortedList() {
		errs.Append(errors.E(attr.NameRange,
			"unrecognized attribute terramate.config.%s", attr.Name,
		))
	}

	errs.AppendWrap(ErrTerramateSchema, block.ValidateSubBlocks("git", "run"))

	gitBlock, ok := block.Blocks["git"]
	if ok {
		logger.Trace().Msg("Type is 'git'")

		cfg.Git = &GitConfig{}

		logger.Trace().Msg("Parse git config.")

		errs.Append(parseGitConfig(cfg.Git, gitBlock))
	}

	runBlock, ok := block.Blocks["run"]
	if ok {
		logger.Trace().Msg("Type is 'run'")

		cfg.Run = &RunConfig{}

		logger.Trace().Msg("Parse run config.")

		errs.Append(parseRunConfig(cfg.Run, runBlock))
	}

	return errs.AsError()
}

func parseRunConfig(runCfg *RunConfig, runBlock *ast.MergedBlock) error {
	logger := log.With().
		Str("action", "parseRunConfig()").
		Logger()

	logger.Trace().Msg("Checking run.env block")

	errs := errors.L()
	for _, attr := range runBlock.Attributes.SortedList() {
		errs.Append(errors.E("unrecognized attribute terramate.config.run.env.%s",
			attr.Name))
	}

	errs.AppendWrap(ErrTerramateSchema, runBlock.ValidateSubBlocks("env"))

	block, ok := runBlock.Blocks["env"]
	if ok {
		runCfg.Env = &RunEnv{}
		errs.Append(parseRunEnv(runCfg.Env, block))
	}

	return errs.AsError()
}

func parseRunEnv(runEnv *RunEnv, envBlock *ast.MergedBlock) error {
	if len(envBlock.Attributes) > 0 {
		runEnv.Attributes = envBlock.Attributes
	}

	errs := errors.L()
	errs.AppendWrap(ErrTerramateSchema, envBlock.ValidateSubBlocks())
	return errs.AsError()
}

func parseGitConfig(git *GitConfig, gitBlock *ast.MergedBlock) error {
	logger := log.With().
		Str("action", "parseGitConfig()").
		Logger()

	logger.Trace().Msg("Range over block attributes.")

	errs := errors.L()

	errs.AppendWrap(ErrTerramateSchema, gitBlock.ValidateSubBlocks())

	for _, attr := range gitBlock.Attributes.SortedList() {
		logger := logger.With().
			Str("attribute", attr.Name).
			Logger()

		value, diags := attr.Expr.Value(nil)
		if diags.HasErrors() {
			errs.Append(errors.E(diags,
				"failed to evaluate terramate.config.%s attribute", attr.Name,
			))
			continue
		}

		logger.Trace().Msg("setting attribute on config")

		switch attr.Name {
		case "default_branch":
			if value.Type() != cty.String {
				errs.Append(errors.E(attr.Expr.Range(),
					"terramate.config.git.branch is not a string but %q",
					value.Type().FriendlyName(),
				))

				continue
			}

			git.DefaultBranch = value.AsString()
		case "default_remote":
			if value.Type() != cty.String {
				errs.Append(errors.E(attr.NameRange,
					"terramate.config.git.remote is not a string but %q",
					value.Type().FriendlyName(),
				))

				continue
			}

			git.DefaultRemote = value.AsString()

		case "default_branch_base_ref":
			if value.Type() != cty.String {
				errs.Append(errors.E(attr.NameRange,
					"terramate.config.git.defaultBranchBaseRef is not a string but %q",
					value.Type().FriendlyName(),
				))

				continue
			}
			git.DefaultBranchBaseRef = value.AsString()

		case "disable_check_untracked":
			if value.Type() != cty.Bool {
				errs.Append(errors.E(attr.NameRange,
					"terramate.config.git.disable_check_untracked is not a boolean but %q",
					value.Type().FriendlyName(),
				))
				continue
			}
			git.DisableCheckUntracked = value.True()
		case "disable_check_uncommitted":
			if value.Type() != cty.Bool {
				errs.Append(errors.E(attr.NameRange,
					"terramate.config.git.disable_check_uncommitted is not a boolean but %q",
					value.Type().FriendlyName(),
				))
				continue
			}
			git.DisableCheckUncommitted = value.True()
		case "disable_check_remote":
			if value.Type() != cty.Bool {
				errs.Append(errors.E(attr.NameRange,
					"terramate.config.git.disable_check_remote is not a boolean but %q",
					value.Type().FriendlyName(),
				))
				continue
			}
			git.DisableCheckRemote = value.True()

		default:
			errs.Append(errors.E(
				attr.NameRange,
				"unrecognized attribute terramate.config.git.%s",
				attr.Name,
			))
		}
	}
	return errs.AsError()
}

func filterBlocksByType(blocktype string, blocks ast.Blocks) ast.Blocks {
	logger := log.With().
		Str("action", "filterBlocksByType()").
		Logger()

	logger.Trace().Msg("Range over blocks.")

	var filtered ast.Blocks
	for _, block := range blocks {
		if block.Type != blocktype {
			continue
		}
		filtered = append(filtered, block)
	}

	return filtered
}

func (p *TerramateParser) parseTerramateSchema() (Config, error) {
	logger := log.With().
		Str("action", "parseTerramateSchema()").
		Str("dir", p.dir).
		Logger()

	config := Config{
		absdir: p.dir,
	}

	errKind := ErrTerramateSchema
	errs := errors.L()

	logger.Trace().Msg("checking for top-level attributes.")

	for _, attr := range p.MergedAttributes.SortedList() {
		errs.Append(errors.E(errKind, attr.NameRange,
			"unrecognized attribute %q", attr.Name))
	}

	logger.Trace().Msg("Range over unmerged blocks.")

	var foundstack bool
	var stackblock *ast.Block
	for _, block := range p.Blocks {
		// unmerged blocks

		logger := logger.With().
			Str("block", block.Type).
			Logger()

		if block.Type == "stack" {
			logger.Trace().Msg("Found stack block type.")

			if foundstack {
				errs.Append(errors.E(errKind, block.DefRange(),
					"duplicated stack block"))
				continue
			}

			foundstack = true
			stackblock = block
		}

		if block.Type == "generate_hcl" {
			logger.Trace().Msg("Found \"generate_hcl\" block")

			errs.Append(validateGenerateHCLBlock(block))
		}

		if block.Type == "generate_file" {
			logger.Trace().Msg("Found \"generate_file\" block")

			errs.Append(validateGenerateFileBlock(block))
		}
	}

	tmBlock, ok := p.MergedBlocks["terramate"]
	if ok {
		var tmconfig Terramate
		tmconfig, err := parseTerramateBlock(tmBlock)
		errs.Append(err)
		if err == nil {
			config.Terramate = &tmconfig
		}
	}

	globalsBlock, ok := p.MergedBlocks["globals"]
	if ok {
		errs.AppendWrap(ErrTerramateSchema, globalsBlock.ValidateSubBlocks())

		// value ignored in the main parser.
	}

	if foundstack {
		logger.Debug().Msg("Parsing stack cfg.")

		if config.Stack != nil {
			errs.Append(errors.E(errKind, stackblock.DefRange(),
				"duplicated stack blocks across configs"))
		}

		config.Stack = &Stack{}
		errs.AppendWrap(errKind, parseStack(p.evalctx, config.Stack, stackblock))
	}

	if err := errs.AsError(); err != nil {
		return Config{}, err
	}

	return config, nil
}

func parseTerramateBlock(block *ast.MergedBlock) (Terramate, error) {
	logger := log.With().
		Str("action", "parseTerramateBlock").
		Logger()

	logger.Trace().Msg("Range over terramate block attributes.")

	tm := Terramate{}

	errKind := ErrTerramateSchema
	errs := errors.L()
	for _, attr := range block.Attributes.SortedList() {
		value, diags := attr.Expr.Value(nil)
		if diags.HasErrors() {
			errs.Append(errors.E(errKind, diags))
		}
		switch attr.Name {
		case "required_version":
			logger.Trace().Msg("Parsing  attribute 'required_version'.")

			if value.Type() != cty.String {
				errs.Append(errors.E(errKind, attr.Expr.Range(),
					"attribute is not a string"))

				continue
			}
			if tm.RequiredVersion != "" {
				errs.Append(errors.E(errKind, attr.NameRange,
					"duplicated attribute"))
			}
			tm.RequiredVersion = value.AsString()

		default:
			errs.Append(errors.E(errKind, attr.NameRange,
				"unsupported attribute"))
		}
	}

	errs.AppendWrap(ErrTerramateSchema, block.ValidateSubBlocks("config"))

	logger.Trace().Msg("Parse terramate sub blocks")

	configBlock, ok := block.Blocks["config"]
	if ok {
		logger.Trace().Msg("Found config block.")

		tm.Config = &RootConfig{}

		logger.Trace().Msg("Parse root config.")

		err := parseRootConfig(tm.Config, configBlock)
		if err != nil {
			errs.Append(errors.E(errKind, err))
		}
	}
	if err := errs.AsError(); err != nil {
		return Terramate{}, err
	}
	return tm, nil
}

type blockValidator func(*ast.Block) error

func parseUnmergedBlocks(root, dir, blocktype string, validate blockValidator) (ast.Blocks, error) {
	logger := log.With().
		Str("action", "hcl.parseBlocks").
		Str("configdir", dir).
		Str("blocktype", blocktype).
		Logger()

	logger.Trace().Msg("loading config")

	parser, err := NewTerramateParser(root, dir)
	if err != nil {
		return nil, err
	}
	err = parser.AddDir(dir)
	if err != nil {
		return nil, errors.E("adding files to parser", err)
	}

	err = parser.MinimalParse()
	if err != nil {
		return nil, err
	}

	logger.Trace().Msg("Validating and filtering blocks")

	blocks := filterBlocksByType(blocktype, parser.Blocks)
	for _, block := range blocks {
		if err := validate(block); err != nil {
			return nil, errors.E(err, "validation failed")
		}
	}

	logger.Trace().Msg("validated blocks")

	return blocks, nil
}

func listTerramateFiles(dir string) ([]string, error) {
	logger := log.With().
		Str("action", "listTerramateFiles()").
		Str("dir", dir).
		Logger()

	logger.Trace().Msg("listing files")

	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, errors.E(err, "reading dir to list Terramate files")
	}

	logger.Trace().Msg("looking for Terramate files")

	files := []string{}

	for _, dirEntry := range dirEntries {
		logger := logger.With().
			Str("entryName", dirEntry.Name()).
			Logger()

		if strings.HasPrefix(dirEntry.Name(), ".") {
			logger.Trace().Msg("ignoring dotfile")
			continue
		}

		if dirEntry.IsDir() {
			logger.Trace().Msg("ignoring dir")
			continue
		}

		filename := dirEntry.Name()
		if isTerramateFile(filename) {
			logger.Trace().Msg("Found Terramate file")
			files = append(files, filename)
		}
	}

	return files, nil
}

// listTerramateDirs lists Terramate dirs, which are any dirs
// except ones starting with ".".
func listTerramateDirs(dir string) ([]string, error) {
	logger := log.With().
		Str("action", "listTerramateDirs()").
		Str("dir", dir).
		Logger()

	logger.Trace().Msg("listing dirs")

	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, errors.E(err, "reading dir to list Terramate dirs")
	}

	logger.Trace().Msg("looking for Terramate directories")

	dirs := []string{}

	for _, dirEntry := range dirEntries {
		logger := logger.With().
			Str("entryName", dirEntry.Name()).
			Logger()

		if !dirEntry.IsDir() {
			logger.Trace().Msg("ignoring non-dir")
			continue
		}

		if strings.HasPrefix(dirEntry.Name(), ".") {
			logger.Trace().Msg("ignoring dotdir")
			continue
		}

		dirs = append(dirs, dirEntry.Name())
	}

	return dirs, nil
}

func isTerramateFile(filename string) bool {
	return strings.HasSuffix(filename, ".tm") || strings.HasSuffix(filename, ".tm.hcl")
}
