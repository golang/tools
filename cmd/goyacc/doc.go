// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*

Goyacc is a version of yacc for Go.
It is written in Go and generates parsers written in Go.

Usage:

	goyacc args...

It is largely transliterated from the Inferno version written in Limbo
which in turn was largely transliterated from the Plan 9 version
written in C and documented at

	https://9p.io/magic/man2html/1/yacc

Adepts of the original yacc will have no trouble adapting to this
form of the tool.

The directory $GOPATH/src/golang.org/x/tools/cmd/goyacc/testdata/expr
is a yacc program for a very simple expression parser. See expr.y and
main.go in that directory for examples of how to write and build
goyacc programs.

The generated parser is reentrant. The parsing function yyParse expects
to be given an argument that conforms to the following interface:

	type yyLexer interface {
		Lex(lval *yySymType) int
		Error(e string)
	}

    The parser will also define the following

	type yyLexerErrLoc interface {
		Lex(lval *yySymType) int
		Error(e string)
		ErrorLoc(e string, loc *yySymLoc)
	}

If token location tracking in error messages is desired, the lexer
should also define the following method, in addition to Error:

func (yylex *Lexer) ErrorLoc(e string, errLoc *yySymLoc) {
...
}

If this method is present, the generated parser will call the ErrorLoc
method, passing it the error string, as well as the token location
information for the offending token.

Lex should return the token identifier, and place other token
information in lval (which replaces the usual yylval).
Error is equivalent to yyerror in the original yacc.

Code inside the grammar actions may refer to the variable yylex,
which holds the yyLexer passed to yyParse.

Clients that need to understand more about the parser state can
create the parser separately from invoking it. The function yyNewParser
returns a yyParser conforming to the following interface:

	type yyParser interface {
		Parse(yyLex) int
		Lookahead() int
	}

Parse runs the parser; the top-level call yyParse(yylex) is equivalent
to yyNewParser().Parse(yylex).

Lookahead can be called during grammar actions to read (but not consume)
the value of the current lookahead token, as returned by yylex.Lex.
If there is no current lookahead token (because the parser has not called Lex
or has consumed the token returned by the most recent call to Lex),
Lookahead returns -1. Calling Lookahead is equivalent to reading
yychar from within in a grammar action.

Multiple grammars compiled into a single program should be placed in
distinct packages.  If that is impossible, the "-p prefix" flag to
goyacc sets the prefix, by default yy, that begins the names of
symbols, including types, the parser, and the lexer, generated and
referenced by yacc's generated code.  Setting it to distinct values
allows multiple grammars to be placed in a single package.

goyacc will generate a parser compatible with bison's token location
tracking semantics.  For more details:

https://www.gnu.org/software/bison/manual/html_node/Tracking-Locations.html
https://www.gnu.org/software/bison/manual/html_node/Token-Locations.html

The generated Go parser will define two types:

type yyPos struct {
        line   int
        column int
}

type yySymLoc struct {
        pos yyPos
        end yyPos
}

The pos field refers to the beginning of the token in question,
and the end field to the end it.  To avoid having to change the
definition of the lexer's Error method, just before the parser calls
the Error method, it will set a global variable, yyErrLoc to the
address of the problematic token's yySymLoc structure.  Since the
lexer provides location information to the parser, and in turn is
provided it, if needed, it's up to the lexer to do so consistently.

As in the above-cited BISON web pages, goyacc will support the use
of @N, where N is an integer from 1 to 9, and will be expanded in
the generated parser to the appropriate variable. If an action rule
wants to print a specific error message, the lexer should be written
to provide one to the parser.

If goyacc was invoked with an explicit prefix, via the '-p' switch,
the above types and variables will have the appropriate prefix.

The token tracking structure yySymLoc is stored inside the yySymType
structure.  This simplifies the changes, since goyacc already has to
copy the structure in question.

*/
package main
