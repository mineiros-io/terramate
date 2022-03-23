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

package eval

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/madlambda/spells/errutil"
)

const (
	ErrPartialEval         errutil.Error = "partial evaluation failed"
	ErrForExprDisallowEval errutil.Error = "`for` expression disallow globals/terramate variables"
	ErrInterpolationEval   errutil.Error = "interpolation failed"
)

/*

Here be dragons. Thou art forewarned


                                             _   __,----'~~~~~~~~~`-----.__
                                      .  .    `//====-              ____,-'~`
                      -.            \_|// .   /||\\  `~~~~`---.___./
                ______-==.       _-~o  `\/    |||  \\           _,'`
          __,--'   ,=='||\=_    ;_,_,/ _-'|-   |`\   \\        ,'
       _-'      ,='    | \\`.    '',/~7  /-   /  ||   `\.     /
     .'       ,'       |  \\  \_  "  /  /-   /   ||      \   /
    / _____  /         |     \\.`-_/  /|- _/   ,||       \ /
   ,-'     `-|--'~~`--_ \     `==-/  `| \'--===-'       _/`
             '         `-|      /|    )-'\~'      _,--"'
                         '-~^\_/ |    |   `\_   ,^             /\
                              /  \     \__   \/~               `\__
                          _,-' _/'\ ,-'~____-'`-/                 ``===\
                         ((->/'    \|||' `.     `\.  ,                _||
           ./                       \_     `\      `~---|__i__i__\--~'_/
          <_n_                     __-^-_    `)  \-.______________,-~'
           `B'\)                  ///,-'~`__--^-  |-------~~~~^'
           /^>                           ///,--~`-\
          `  `
*/

// node represents a grammar node but in terms of its original source tokens and
// the rewritten (evaluated) ones.
type node struct {
	source    hclwrite.Tokens
	evaluated hclwrite.Tokens

	hasCond bool
	hasOp   bool
}

type engine struct {
	tokens hclwrite.Tokens
	pos    int
	ctx    *Context

	// evalstack is a stack of evaluated nodes.
	// The engine walks through the token list evaluating them as needed into a
	// separated node struct placed in this stack.
	evalstack []*node

	nparen int
}

// addsrc adds source token.
func (n *node) addsrc(toks ...*hclwrite.Token) {
	n.source = append(n.source, toks...)
}

// add evaluated token.
func (n *node) add(toks ...*hclwrite.Token) {
	n.evaluated = append(n.evaluated, toks...)
}

func newPartialEngine(tokens hclwrite.Tokens, ctx *Context) *engine {
	return &engine{
		tokens: tokens,
		ctx:    ctx,
	}
}

// PartialEval evaluates only the terramate variable expressions from the list
// of tokens, leaving all the rest as-is. It returns a modified list of tokens
// with  no reference to terramate namespaced variables (globals and terramate)
// and functions (tm_ prefixed functions).
func (e *engine) PartialEval() (hclwrite.Tokens, error) {
	e.newnode()
	for e.hasTokens() {
		err := e.evalExpr()
		if err != nil {
			return nil, errutil.Chain(ErrPartialEval, err)
		}
		e.commit()
	}

	if len(e.evalstack) != 1 {
		panic("invalid number of scratch spaces")
	}

	return e.evalstack[0].evaluated, nil
}

func (e *engine) hasTokens() bool {
	return e.pos < len(e.tokens)
}

func (e *engine) peek() *hclwrite.Token {
	return e.tokens[e.pos]
}

func (e *engine) peekn(n int) *hclwrite.Token {
	return e.tokens[e.pos+n]
}

func (e *engine) newnode() (int, *node) {
	n := &node{}
	e.evalstack = append(e.evalstack, n)
	return e.tailpos(), n
}

func (e *engine) commit() {
	if e.tailpos() == e.headpos() {
		panic("everything committed")
	}

	tail := e.tail()

	mergeat := e.tailpos() - 1
	merge := e.evalstack[mergeat]
	merge.add(tail.evaluated...)
	merge.addsrc(tail.source...)
	if tail.hasCond {
		merge.hasCond = true
	}
	if tail.hasOp {
		merge.hasOp = true
	}
	e.evalstack = e.evalstack[e.headpos() : mergeat+1]
}

func (e *engine) tail() *node { return e.evalstack[e.tailpos()] }

func (e *engine) emit() {
	tail := e.tail()
	tail.evaluated = append(tail.evaluated, e.peek())
	tail.source = append(tail.source, e.peek())
	e.pos++
}

func (e *engine) emitn(n int) {
	for i := 0; e.hasTokens() && i < n; i++ {
		e.emit()
	}
}

func (e *engine) emitVariable(v variable) {
	tail := e.tail()
	tail.evaluated = append(tail.evaluated, v.alltokens()...)
	for i := 0; i < v.size(); i++ {
		tail.source = append(tail.source, e.peek())
		e.pos++
	}
}

func (e *engine) emitTokens(source hclwrite.Tokens, evaluated hclwrite.Tokens) {
	tail := e.tail()
	tail.evaluated = append(tail.evaluated, evaluated...)
	tail.source = append(tail.source, source...)
}

func (e *engine) emitnl() {
	for e.hasTokens() && (e.peek().Type == hclsyntax.TokenNewline ||
		e.peek().Type == hclsyntax.TokenComment) {
		e.emit()
	}
}

func (e *engine) emitnlparens() {
	if e.nparen > 0 {
		e.emitnl()
	}
}

func (e *engine) skipNewLines(from int) int {
	i := from
	for e.hasTokens() && (e.peekn(i).Type == hclsyntax.TokenNewline ||
		e.peekn(i).Type == hclsyntax.TokenComment) {
		i++
	}
	return i
}

func (e *engine) evalExpr() error {
	_, thisNode := e.newnode()

loop:
	for {
		e.emitnlparens()
		switch e.peek().Type {
		case hclsyntax.TokenBang, hclsyntax.TokenMinus, hclsyntax.TokenComment:
			e.emit()
		default:
			break loop
		}
	}

	e.emitnlparens()
	beginPos := e.pos
	tok := e.peek()
	// exprTerm
	switch tok.Type {
	case hclsyntax.TokenEOF:
		e.emit()
	case hclsyntax.TokenOHeredoc:
		e.emit()

		for e.hasTokens() &&
			e.peek().Type != hclsyntax.TokenCHeredoc &&
			e.peek().Type != hclsyntax.TokenEOF { // TODO(i4k): hack to imitate hashicorp lib
			e.emit()
		}
		if !e.hasTokens() {
			panic("expect close heredoc")
		}

		e.emit()
	case hclsyntax.TokenOQuote:
		err := e.evalString()
		if err != nil {
			return err
		}
		e.commit()
	case hclsyntax.TokenIdent:
		switch string(tok.Bytes) {
		case "true", "false", "null":
			e.emit()
		default:
			if e.canEvaluateIdent() {
				err := e.evalIdent()
				if err != nil {
					return err
				}
				e.commit()

			} else {
				e.emit()
			}
		}
	case hclsyntax.TokenOParen:
		e.emit()
		e.emitnl()

		e.nparen++

		err := e.evalExpr()
		if err != nil {
			return err
		}

		e.commit()
		e.emitnl()

		if e.peek().Type != hclsyntax.TokenCParen {
			panic(e.peek().Type)
		}

		e.emit()
		e.nparen--
	case hclsyntax.TokenOBrace, hclsyntax.TokenOBrack:
		var err error

		var closeToken hclsyntax.TokenType

		openToken := tok.Type
		if openToken == hclsyntax.TokenOBrace {
			closeToken = hclsyntax.TokenCBrace
		} else {
			closeToken = hclsyntax.TokenCBrack
		}

		pos := e.skipNewLines(1)

		next := e.peekn(pos)
		switch {
		case isForExpr(next):
			err = e.evalForExpr(openToken, closeToken)
		case openToken == hclsyntax.TokenOBrace:
			err = e.evalObject()
		case openToken == hclsyntax.TokenOBrack:
			err = e.evalList()
		default:
			panic("unexpected")
		}

		if err != nil {
			return err
		}

		e.commit()

	case hclsyntax.TokenNumberLit:
		e.emit()
	}

	if e.pos == beginPos {
		panic(sprintf("bug: no advance in the position: %s (%s)", e.peek().Type, e.tokens[e.pos:].Bytes()))
	}

	if !e.hasTokens() {
		return nil
	}

	e.emitnlparens()

	// exprTerm INDEX,GETATTR,SPLAT (expression acessors)
	tok = e.peek()
	switch tok.Type {
	case hclsyntax.TokenOBrack, hclsyntax.TokenDot:
		err := e.evalAcessors()
		if err != nil {
			return err
		}
		e.commit()
	}

	e.emitnlparens()

	// operation && conditional

	tok = e.peek()
	switch t := tok.Type; {
	case isBinOp(t):
		e.emit()
		e.emitnlparens()
		err := e.evalExpr()
		if err != nil {
			return err
		}
		e.commit()
		thisNode.hasOp = true

	case t == hclsyntax.TokenQuestion:
		e.emit()
		e.emitnlparens()
		err := e.evalExpr()
		if err != nil {
			return err
		}

		e.commit()

		if e.peek().Type != hclsyntax.TokenColon {
			panic(errorf(
				"expected `:` but found a %s (%s)",
				e.peek().Bytes, e.peek().Type,
			))
		}

		e.emit()
		e.emitnlparens()
		err = e.evalExpr()
		if err != nil {
			return err
		}
		e.commit()

		thisNode.hasCond = true
	}

	return nil
}

func (e *engine) evalAcessors() error {
	e.newnode()

	tok := e.peek()
	if tok.Type != hclsyntax.TokenOBrack &&
		tok.Type != hclsyntax.TokenDot {
		panic("not an acessor")
	}

	for e.hasTokens() {
		tok := e.peek()
		switch tok.Type {
		default:
			// parsed whole acessor sequence.
			return nil
		case hclsyntax.TokenOBrack:
			err := e.evalIndex()
			if err != nil {
				return err
			}
			e.commit()
		case hclsyntax.TokenDot:
			parsed := false

			pos := 1
			if e.nparen > 0 {
				pos = e.skipNewLines(1)
			}
			next := e.peekn(pos)
			if next.Type == hclsyntax.TokenStar {
				e.emitn(pos + 1)
				parsed = true
			}

			if e.peek().Type == hclsyntax.TokenDot {
				err := e.evalGetAttr()
				if err != nil {
					return err
				}
				e.commit()
				parsed = true
			}

			e.emitnlparens()

			if !parsed {
				panic("unexpected acessor sequence")
			}
		}
	}

	return nil
}

func (e *engine) evalIndex() error {
	e.newnode()
	e.nparen++
	tok := e.peek()
	if tok.Type != hclsyntax.TokenOBrack {
		panic("expect a '['")
	}

	e.emit()
	if e.peek().Type == hclsyntax.TokenStar {
		// splat: <expr>[*]
		e.emit()
	} else {
		err := e.evalExpr()
		if err != nil {
			return err
		}
		e.commit()
	}

	tok = e.peek()
	if tok.Type != hclsyntax.TokenCBrack {
		panic("expect a ']'")
	}

	e.emit()
	tok = e.peek()
	switch tok.Type {
	case hclsyntax.TokenOBrack, hclsyntax.TokenDot:
		err := e.evalAcessors()
		if err != nil {
			return err
		}
		e.commit()
	}

	e.nparen--

	return nil
}

func (e *engine) evalGetAttr() error {
	e.newnode()
	if e.peek().Type != hclsyntax.TokenDot {
		panic("expected . IDENT (getAttr)")
	}

	e.emit()
	e.emitnlparens()
	tok := e.peek()
	if tok.Type == hclsyntax.TokenIdent ||
		tok.Type == hclsyntax.TokenNumberLit {
		e.emit()
	} else {
		panic(sprintf("expect an IDENT or number: %s %t", e.peek().Type, e.nparen))
	}

	return nil
}

func (e *engine) evalIdent() error {
	e.newnode()
	if !e.canEvaluateIdent() {
		return errorf("malformed code")
	}

	tok := e.peek()
	if tok.Type != hclsyntax.TokenIdent {
		panic(errorf("evalIdent: unexpected token '%s' (%s)", tok.Bytes, tok.Type))
	}

	i := e.skipNewLines(1)

	next := e.peekn(i)
	switch next.Type {
	case hclsyntax.TokenDot:
		err := e.evalVar()
		if err != nil {
			return err
		}
		e.commit()
	case hclsyntax.TokenOParen:
		err := e.evalFuncall()
		if err != nil {
			return err
		}
		e.commit()
	default:
		panic("ident cannot be evaluated")
	}

	return nil
}

func (e *engine) evalList() error {
	e.newnode()
	tok := e.peek()
	if tok.Type != hclsyntax.TokenOBrack {
		panic("bug")
	}

	e.nparen++

	e.emit()
	e.emitnl()
	for e.hasTokens() && e.peek().Type != hclsyntax.TokenCBrack {
		err := e.evalExpr()
		if err != nil {
			return err
		}

		e.commit()
		tok := e.peek()
		if tok.Type == hclsyntax.TokenComma {
			e.emit()
		}
		e.emitnl()
	}

	if !e.hasTokens() {
		panic("malformed list")
	}

	tok = e.peek()
	if tok.Type != hclsyntax.TokenCBrack {
		panic(errorf("malformed list, unexpected %s", tok.Bytes))
	}

	e.nparen--

	e.emit()
	return nil
}

func (e *engine) evalObject() error {
	e.newnode()
	tok := e.peek()
	if tok.Type != hclsyntax.TokenOBrace {
		panic("bug")
	}

	e.emit()
	e.emitnl()
	for e.hasTokens() && e.peek().Type != hclsyntax.TokenCBrace {
		err := e.evalExpr()
		if err != nil {
			return err
		}

		e.commit()
		e.emitnl()
		tok = e.peek()
		if tok.Type != hclsyntax.TokenEqual && tok.Type != hclsyntax.TokenColon {
			panic(errorf("evalObject: unexpected token '%s' (%s)", tok.Bytes, tok.Type))
		}

		e.emit()
		err = e.evalExpr()
		if err != nil {
			return err
		}

		e.commit()
		tok = e.peek()
		if tok.Type == hclsyntax.TokenComma {
			e.emit()
		}

		e.emitnl()
	}

	if !e.hasTokens() {
		panic("malformed object")
	}

	tok = e.peek()
	if tok.Type != hclsyntax.TokenCBrace {
		panic(errorf("malformed object, unexpected %s", tok.Bytes))
	}

	e.emit()
	return nil
}

func (e *engine) evalForExpr(matchOpenType, matchCloseType hclsyntax.TokenType) error {
	_, thisNode := e.newnode()
	// { | [
	tok := e.peek()
	if tok.Type != matchOpenType {
		panic(sprintf("evalForExpr: malformed `for` expression: %s", tok.Bytes))
	}

	e.emit()
	e.emitnl()

	// { for
	tok = e.peek()
	if tok.Type != hclsyntax.TokenIdent || string(tok.Bytes) != "for" {
		panic(sprintf("evalForExpr: malformed `for` expression: %s", tok.Bytes))
	}

	e.emit()
	// { for <ident>,<ident>,...
	for e.hasTokens() && string(e.peek().Bytes) != "in" {
		e.emitnl()
		tok = e.peek()
		if tok.Type != hclsyntax.TokenIdent {
			return errorf("invalid `for` expression: found %s", tok.Type)
		}

		e.emit()
		e.emitnl()
		tok = e.peek()
		if tok.Type == hclsyntax.TokenComma {
			e.emit()
		}
	}

	tok = e.peek()
	if tok.Type != hclsyntax.TokenIdent {
		panic(errorf("found the `in` bytes of %s type instead of IDENT", tok.Type))
	}

	e.emit()

	// consume everything and give errors in case of terramate variables being
	// used in the `for`.
	matchingCollectionTokens := 1
	for e.hasTokens() && matchingCollectionTokens > 0 {
		tok = e.peek()
		switch tok.Type {
		case matchOpenType:
			matchingCollectionTokens++
		case matchCloseType:
			matchingCollectionTokens--
		case hclsyntax.TokenQuestion:
			thisNode.hasCond = true
		}
		v, found := e.parseVariable(e.tokens[e.pos:])
		if found {
			if v.isTerramate {
				return errutil.Chain(
					ErrForExprDisallowEval,
					errorf("evaluating expression: %s", v.alltokens().Bytes()),
				)
			}

			e.emitVariable(v)
		} else {
			e.emit()
		}
	}

	return nil
}

func (e *engine) evalTmFuncall() error {
	e.newnode()
	if len(e.tokens[e.pos:]) < 3 {
		return errorf("not a funcall")
	}

	begin := e.pos
	tok := e.peek()
	if !isTmFuncall(tok) {
		panic("not a `tm_` function")
	}

	if e.peekn(1).Type != hclsyntax.TokenOParen {
		panic(errorf("not a funcall: %s", e.tokens[e.pos:].Bytes()))
	}

	matchingParens := 1
	e.pos += 2
	for e.hasTokens() {
		switch e.peek().Type {
		case hclsyntax.TokenOParen:
			matchingParens++
		case hclsyntax.TokenCParen:
			matchingParens--
		}

		if matchingParens == 0 {
			break
		}

		e.pos++
	}

	if matchingParens > 0 || e.peek().Type != hclsyntax.TokenCParen {
		panic(errorf("malformed funcall: %s", e.tokens.Bytes()))
	}

	e.pos++

	var expr []byte

	for _, et := range e.tokens[begin:e.pos] {
		expr = append(expr, et.Bytes...)
	}

	exprParsed, diags := hclsyntax.ParseExpression(expr, "gen.hcl", hcl.Pos{})
	if diags.HasErrors() {
		return errorf("failed to parse expr ('%s'): %v", expr, diags.Error())
	}

	val, err := e.ctx.Eval(exprParsed)
	if err != nil {
		return err
	}

	e.emitTokens(e.tokens[begin:e.pos], hclwrite.TokensForValue(val))
	return nil
}

func (e *engine) evalFuncall() error {
	if len(e.tokens[e.pos:]) < 3 {
		return errorf("not a funcall")
	}

	tok := e.peek()
	if tok.Type != hclsyntax.TokenIdent {
		panic(errorf("malformed funcall, not start with IDENT"))
	}

	if isTmFuncall(tok) {
		return e.evalTmFuncall()
	}

	e.newnode()
	e.emit()
	e.emitnl()
	if e.peek().Type != hclsyntax.TokenOParen {
		panic(errorf("not a funcall: %s", e.tokens[e.pos:].Bytes()))
	}

	e.emit()
	e.emitnl()
	e.nparen++
	for e.hasTokens() && e.peek().Type != hclsyntax.TokenCParen {
		err := e.evalExpr()
		if err != nil {
			return err
		}
		e.commit()
		e.emitnl()

		if e.peek().Type == hclsyntax.TokenComma ||
			e.peek().Type == hclsyntax.TokenEllipsis {
			e.emit()
		} else if e.peek().Type != hclsyntax.TokenCParen {
			panic(errorf("expect a comma or ) but found %s", e.tokens[e.pos].Type))
		}
		e.emitnl()
	}
	e.nparen--

	if !e.hasTokens() {
		panic(errorf("malformed funcall: %s", e.tokens.Bytes()))
	}

	if e.peek().Type != hclsyntax.TokenCParen {
		panic("bug: funcall not closed")
	}

	e.emit()
	return nil
}

func (e *engine) evalVar() error {
	e.newnode()
	v, found := e.parseVariable(e.tokens[e.pos:])
	if !found {
		panic("expect a variable")
	}

	if !v.isTerramate {
		e.emitVariable(v)
		return nil
	}

	var expr []byte
	for _, et := range v.alltokens() {
		expr = append(expr, et.Bytes...)
	}

	parsedExpr, diags := hclsyntax.ParseExpression(expr, "gen.hcl", hcl.Pos{})
	if diags.HasErrors() {
		return errorf("failed to parse expr %s: %v", expr, diags.Error())
	}

	val, err := e.ctx.Eval(parsedExpr)
	if err != nil {
		return err
	}

	e.emitTokens(e.tokens[e.pos:e.pos+v.size()], hclwrite.TokensForValue(val))
	e.pos += v.size()
	return nil
}

func (e *engine) evalInterp() error {
	e.newnode()
	tok := e.peek()

	if tok.Type != hclsyntax.TokenTemplateInterp {
		panic("unexpected token")
	}

	interpOpen := tok

	e.pos++
	err := e.evalExpr()
	if err != nil {
		return err
	}

	e.commit()

	tok = e.peek()
	if tok.Type != hclsyntax.TokenTemplateSeqEnd {
		panic("malformed interpolation expression, missing }")
	}

	interpClose := tok

	e.pos++

	// TODO(i4k):
	//
	// We should emit a `${` and `}` when the expression has non-evaluated parts
	// but there's no easy way of figuring out this without an AST.
	// The naive approach is defined below:
	//   1. check if there's any Operation | Conditional.
	//   2. check if the expression is not fully evaluated.
	//
	// if any of the checks are true, then we need to emit the interp tokens.
	//
	// But there's no way to correctly check 1 without building a AST, as some
	// tokens are used in different grammar constructs (eg.: the ":" is by
	// ConditionalExpr and ForExpr...).
	// So for now we do a lazy (incorrect) check, but this needs to be improved.
	isCombinedExpr := func(n *node) bool {
		return n.hasCond || n.hasOp
	}

	needsEval := func(n *node) bool {
		if isSameTokens(n.source, n.evaluated) {
			return true
		}

		for i := 0; i < len(n.evaluated)-2; i++ {
			tok1 := n.evaluated[i]
			tok2 := n.evaluated[i+1]
			tok3 := n.evaluated[i+2]

			if (tok1.Type == hclsyntax.TokenIdent &&
				tok2.Type == hclsyntax.TokenDot &&
				tok3.Type == hclsyntax.TokenIdent) ||
				(tok1.Type == hclsyntax.TokenIdent &&
					tok2.Type == hclsyntax.TokenOParen) {
				return true
			}
		}

		return false
	}

	n := e.tail()
	rewritten := &node{}

	shouldEmitInterp := isCombinedExpr(n) || needsEval(n)

	if shouldEmitInterp {
		rewritten.add(interpOpen)
		rewritten.addsrc(interpOpen)
	}

	rewritten.add(n.evaluated...)
	rewritten.addsrc(n.source...)

	if shouldEmitInterp {
		rewritten.add(interpClose)
		rewritten.addsrc(interpClose)
	}

	e.evalstack[e.tailpos()] = rewritten
	return nil
}

func (e *engine) evalString() error {
	nodePos, _ := e.newnode()
	tok := e.peek()
	if tok.Type != hclsyntax.TokenOQuote {
		return errorf("bug: not a quoted string")
	}

	e.pos++
	for e.hasTokens() && e.peek().Type != hclsyntax.TokenCQuote {
		tok := e.peek()
		switch tok.Type {
		case hclsyntax.TokenQuotedLit:
			e.newnode()
			e.emit()
		case hclsyntax.TokenTemplateInterp:
			err := e.evalInterp()
			if err != nil {
				return errutil.Chain(ErrInterpolationEval, err)
			}
		default:
			panic(errorf("evalString: unexpected token %s (%s)", tok.Bytes, tok.Type))
		}
	}

	if !e.hasTokens() {
		panic(errorf("malformed quoted string %d %d", len(e.tokens[e.pos:]), e.pos))
	}

	tok = e.peek()
	if tok.Type != hclsyntax.TokenCQuote {
		return errorf("malformed quoted string, expected '\"' (close quote)")
	}

	e.pos++

	rewritten := &node{}
	rewritten.addsrc(tokenOQuote())
	rewritten.add(tokenOQuote())

	// handles the case of a single interpolated object, examples:
	// - "${a.b}"
	// - "${a}"
	// - "${0}"
	// - "${global.something}"
	if e.tailpos()-nodePos == 1 {
		e.commit()
		tail := e.tail()
		switch tail.evaluated[0].Type {
		case hclsyntax.TokenQuotedLit, hclsyntax.TokenTemplateInterp:
			rewritten.add(e.tail().evaluated...)
			rewritten.addsrc(e.tail().source...)
			rewritten.add(tokenCQuote())
			rewritten.addsrc(tokenCQuote())
			e.evalstack[e.tailpos()] = rewritten
		}

		return nil
	}

	// handle advanced interpolation cases:
	// - "${0 + 1}" and anything mathing ${<any> <op> <any>}
	// - "${funcall(0)}"
	// - "<string>${<anything>}<string>" and all variants.

	// at this point the stack looks like:
	//
	//                           .
	//                           . (nodePos - 1)
	//                           . scratchPos (this string node)
	//                           . 1st interpolation part
	//                           . 2nd interpolation part
	//                           . nth interpolation part
	//
	// The code below will merge all interpolation parts into this node.

	// we merge subsequent string interpolation into previous (last) TokenQuotedLit.
	var last *hclwrite.Token
	for i := nodePos + 1; i <= e.tailpos(); i++ {
		switch e.evalstack[i].evaluated[0].Type {
		case hclsyntax.TokenOBrace, hclsyntax.TokenOBrack:
			return errutil.Chain(
				ErrInterpolationEval,
				errorf("serialization of collection value is not supported"),
			)
		case hclsyntax.TokenQuotedLit:
			if len(e.evalstack[i].evaluated) > 1 {
				panic("unexpected case")
			}

			rewritten.add(e.evalstack[i].evaluated...)
			rewritten.addsrc(e.evalstack[i].source...)
			last = rewritten.evaluated[len(rewritten.evaluated)-1]
		case hclsyntax.TokenTemplateInterp:
			rewritten.add(e.evalstack[i].evaluated...)
			rewritten.addsrc(e.evalstack[i].source...)
			last = rewritten.evaluated[len(rewritten.evaluated)-1]
		case hclsyntax.TokenNumberLit, hclsyntax.TokenIdent:
			if len(e.evalstack[i].evaluated) > 1 {
				panic("expects one part")
			}

			if last == nil {
				rewritten.add(&hclwrite.Token{
					Type:  hclsyntax.TokenQuotedLit,
					Bytes: e.evalstack[i].evaluated[0].Bytes,
				})
				rewritten.addsrc(e.evalstack[i].source...)
				last = rewritten.evaluated[len(rewritten.evaluated)-1]
			} else {
				last.Bytes = append(last.Bytes, e.evalstack[i].evaluated[0].Bytes...)
			}

		case hclsyntax.TokenOQuote:
			if len(e.evalstack[i].evaluated) != 3 {
				panic(sprintf(
					"unexpected string case: %s (%d)",
					e.evalstack[i].evaluated.Bytes(), len(e.evalstack[i].evaluated)))
			}

			if last == nil {
				rewritten.add(&hclwrite.Token{
					Type:  hclsyntax.TokenQuotedLit,
					Bytes: e.evalstack[i].evaluated[1].Bytes,
				})
				rewritten.addsrc(e.evalstack[i].source...)
				last = rewritten.evaluated[len(rewritten.evaluated)-1]
			} else {
				last.Bytes = append(last.Bytes, e.evalstack[i].evaluated[1].Bytes...)
			}

		default:
			panic(sprintf("unexpected interpolation type: %s (%s)",
				e.evalstack[i].evaluated[0].Bytes, e.evalstack[i].evaluated[0].Type))
		}
	}

	rewritten.add(tokenCQuote())
	rewritten.addsrc(tokenCQuote())
	e.evalstack[nodePos] = rewritten
	e.evalstack = e.evalstack[e.headpos() : nodePos+1]

	return nil
}

func (e *engine) parseVariable(tokens hclwrite.Tokens) (v variable, found bool) {
	if len(tokens) < 3 {
		// a variable has at least the format: a.b
		return variable{}, false
	}

	if tokens[0].Type != hclsyntax.TokenIdent {
		return variable{}, false
	}

	pos := e.skipNewLines(1)
	wantDot := true
	for pos < len(tokens) {
		if e.nparen > 0 {
			pos = e.skipNewLines(pos)
		}

		tok := tokens[pos]

		if wantDot {
			if tok.Type != hclsyntax.TokenDot {
				break
			}
		} else if tok.Type != hclsyntax.TokenIdent &&
			tok.Type != hclsyntax.TokenNumberLit &&
			tok.Type != hclsyntax.TokenStar {
			break
		}

		pos++
		wantDot = !wantDot
	}

	if pos < 3 {
		// found <IDENT> <DOT> so not a variable...
		return variable{}, false
	}

	v.name = tokens[:pos]
	nsvar := string(v.name[0].Bytes)
	v.isTerramate = nsvar == "global" || nsvar == "terramate"

	if pos < len(tokens) && tokens[pos].Type == hclsyntax.TokenOBrack {
		v.index = parseIndexing(tokens[pos:])
	}

	return v, true
}

func parseIndexing(tokens hclwrite.Tokens) hclwrite.Tokens {
	if tokens[0].Type != hclsyntax.TokenOBrack {
		panic("not an indexing")
	}

	pos := 1
	matchingBracks := 1
	for pos < len(tokens) {
		// here be dragons
		// in other words: we don't validate the index expression, as it's going
		// to be evaluated by hashicorp library anyway (if global/terramate) or
		// ignored otherwise. Let's trust that hcl.Parse() catches all the
		// issues.

		switch tokens[pos].Type {
		case hclsyntax.TokenOBrack:
			matchingBracks++
		case hclsyntax.TokenCBrack:
			matchingBracks--
		}

		if matchingBracks == 0 {
			if tokens[pos+1].Type == hclsyntax.TokenOBrack {
				// beginning of next '[' sequence.
				// this is for the parsing of a.b[<expr][<expr2]...
				matchingBracks++
				pos += 2
				continue
			}

			break
		}

		pos++
	}

	if tokens[pos].Type != hclsyntax.TokenCBrack {
		panic("unexpected")
	}

	return tokens[1:pos]
}

func (e *engine) canEvaluateIdent() bool {
	if len(e.tokens[e.pos:]) < 2 {
		return false
	}

	if e.peek().Type != hclsyntax.TokenIdent {
		panic("bug: expects an IDENT at pos 0")
	}

	i := e.skipNewLines(1)

	next := e.peekn(i)
	return next.Type == hclsyntax.TokenDot || next.Type == hclsyntax.TokenOParen
}

func (e *engine) headpos() int {
	if len(e.evalstack) == 0 {
		panic("no evaluated elements")
	}
	return 0
}

func (e *engine) tailpos() int {
	var pos int
	if len(e.evalstack) > 0 {
		pos = len(e.evalstack) - 1
	} else {
		pos = 0
	}
	return pos
}

func isCmpOp(t hclsyntax.TokenType) bool {
	switch t {
	case hclsyntax.TokenEqualOp, hclsyntax.TokenNotEqual,
		hclsyntax.TokenLessThan, hclsyntax.TokenLessThanEq,
		hclsyntax.TokenGreaterThan, hclsyntax.TokenGreaterThanEq:
		return true
	}
	return false
}

func isLogicOp(t hclsyntax.TokenType) bool {
	switch t {
	case hclsyntax.TokenOr, hclsyntax.TokenAnd, hclsyntax.TokenBang:
		return true
	}
	return false
}

func isArithOp(t hclsyntax.TokenType) bool {
	switch t {
	case hclsyntax.TokenPlus, hclsyntax.TokenMinus,
		hclsyntax.TokenStar, hclsyntax.TokenSlash, hclsyntax.TokenPercent:
		return true
	}
	return false
}

func isBinOp(t hclsyntax.TokenType) bool {
	switch {
	case isCmpOp(t), isArithOp(t), isLogicOp(t):
		return true
	}
	return false
}

func isForExpr(tok *hclwrite.Token) bool {
	return tok.Type == hclsyntax.TokenIdent && string(tok.Bytes) == "for"
}

func isTmFuncall(tok *hclwrite.Token) bool {
	return tok.Type == hclsyntax.TokenIdent &&
		strings.HasPrefix(string(tok.Bytes), "tm_")
}

func isSameTokens(a, b hclwrite.Tokens) bool {
	//fmt.Printf("len(a): %d, len(b): %d: `%s` != `%s`\n", len(a), len(b), a.Bytes(), b.Bytes())
	if len(a) != len(b) {
		return false
	}

	for i := 0; i < len(a); i++ {
		if b[i].Type != a[i].Type ||
			string(b[i].Bytes) != string(a[i].Bytes) {
			//fmt.Printf("a[%d] != b[%d]: `%s` != `%s`\n", i, i, a[i].Bytes, b[i].Bytes)
			return false
		}
	}
	//fmt.Printf("are the same\n")
	return true
}

// variable is a low-level representation of a variable in terms of tokens.
type variable struct {
	name  hclwrite.Tokens
	index hclwrite.Tokens

	isTerramate bool
}

func (v variable) alltokens() hclwrite.Tokens {
	tokens := v.name
	if len(v.index) > 0 {
		tokens = append(tokens, tokenOBrack())
		tokens = append(tokens, v.index...)
		tokens = append(tokens, tokenCBrack())
	}
	return tokens
}

func (v variable) size() int {
	sz := len(v.name)
	if len(v.index) > 0 {
		sz += len(v.index) + 2 // `[` <tokens> `]`
	}
	return sz
}

var sprintf = fmt.Sprintf
var errorf = fmt.Errorf