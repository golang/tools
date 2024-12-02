# Gopls: Analyzers

<!-- No Table of Contents: GitHub's Markdown renderer synthesizes it. -->

Gopls contains a driver for pluggable, modular static
[analyzers](https://pkg.go.dev/golang.org/x/tools/go/analysis#hdr-Analyzer),
such as those used by [go vet](https://pkg.go.dev/cmd/vet).

Most analyzers report mistakes in your code;
some suggest "quick fixes" that can be directly applied in your editor.
Every time you edit your code, gopls re-runs its analyzers.
Analyzer diagnostics help you detect bugs sooner,
before you run your tests, or even before you save your files.

This document describes the suite of analyzers available in gopls,
which aggregates analyzers from a variety of sources:

- all the usual bug-finding analyzers from the `go vet` suite (e.g. `printf`; see [`go tool vet help`](https://pkg.go.dev/cmd/vet) for the complete list);
- a number of analyzers with more substantial dependencies that prevent them from being used in `go vet` (e.g. `nilness`);
- analyzers that augment compilation errors by suggesting quick fixes to common mistakes (e.g. `fillreturns`); and
- a handful of analyzers that suggest possible style improvements (e.g. `simplifyrange`).

To enable or disable analyzers, use the [analyses](settings.md#analyses) setting.

In addition, gopls includes the [`staticcheck` suite](https://staticcheck.dev/docs/checks),
though these analyzers are off by default.
Use the [`staticcheck`](settings.md#staticcheck`) setting to enable them,
and consult staticcheck's documentation for analyzer details.

<!-- When staticcheck=true, we currently use the {S SA ST QF} suites, sans {SA5009, SA5011} -->


<!-- BEGIN Analyzers: DO NOT MANUALLY EDIT THIS SECTION -->
<a id='appends'></a>
## `appends`: check for missing values after append


This checker reports calls to append that pass
no values to be appended to the slice.

	s := []string{"a", "b", "c"}
	_ = append(s)

Such calls are always no-ops and often indicate an
underlying mistake.

Default: on.

Package documentation: [appends](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/appends)

<a id='asmdecl'></a>
## `asmdecl`: report mismatches between assembly files and Go declarations



Default: on.

Package documentation: [asmdecl](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/asmdecl)

<a id='assign'></a>
## `assign`: check for useless assignments


This checker reports assignments of the form x = x or a[i] = a[i].
These are almost always useless, and even when they aren't they are
usually a mistake.

Default: on.

Package documentation: [assign](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/assign)

<a id='atomic'></a>
## `atomic`: check for common mistakes using the sync/atomic package


The atomic checker looks for assignment statements of the form:

	x = atomic.AddUint64(&x, 1)

which are not atomic.

Default: on.

Package documentation: [atomic](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/atomic)

<a id='atomicalign'></a>
## `atomicalign`: check for non-64-bits-aligned arguments to sync/atomic functions



Default: on.

Package documentation: [atomicalign](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/atomicalign)

<a id='bools'></a>
## `bools`: check for common mistakes involving boolean operators



Default: on.

Package documentation: [bools](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/bools)

<a id='buildtag'></a>
## `buildtag`: check //go:build and // +build directives



Default: on.

Package documentation: [buildtag](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/buildtag)

<a id='cgocall'></a>
## `cgocall`: detect some violations of the cgo pointer passing rules


Check for invalid cgo pointer passing.
This looks for code that uses cgo to call C code passing values
whose types are almost always invalid according to the cgo pointer
sharing rules.
Specifically, it warns about attempts to pass a Go chan, map, func,
or slice to C, either directly, or via a pointer, array, or struct.

Default: on.

Package documentation: [cgocall](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/cgocall)

<a id='composites'></a>
## `composites`: check for unkeyed composite literals


This analyzer reports a diagnostic for composite literals of struct
types imported from another package that do not use the field-keyed
syntax. Such literals are fragile because the addition of a new field
(even if unexported) to the struct will cause compilation to fail.

As an example,

	err = &net.DNSConfigError{err}

should be replaced by:

	err = &net.DNSConfigError{Err: err}


Default: on.

Package documentation: [composites](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/composite)

<a id='copylocks'></a>
## `copylocks`: check for locks erroneously passed by value


Inadvertently copying a value containing a lock, such as sync.Mutex or
sync.WaitGroup, may cause both copies to malfunction. Generally such
values should be referred to through a pointer.

Default: on.

Package documentation: [copylocks](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/copylock)

<a id='deepequalerrors'></a>
## `deepequalerrors`: check for calls of reflect.DeepEqual on error values


The deepequalerrors checker looks for calls of the form:

    reflect.DeepEqual(err1, err2)

where err1 and err2 are errors. Using reflect.DeepEqual to compare
errors is discouraged.

Default: on.

Package documentation: [deepequalerrors](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/deepequalerrors)

<a id='defers'></a>
## `defers`: report common mistakes in defer statements


The defers analyzer reports a diagnostic when a defer statement would
result in a non-deferred call to time.Since, as experience has shown
that this is nearly always a mistake.

For example:

	start := time.Now()
	...
	defer recordLatency(time.Since(start)) // error: call to time.Since is not deferred

The correct code is:

	defer func() { recordLatency(time.Since(start)) }()

Default: on.

Package documentation: [defers](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/defers)

<a id='deprecated'></a>
## `deprecated`: check for use of deprecated identifiers


The deprecated analyzer looks for deprecated symbols and package
imports.

See https://go.dev/wiki/Deprecated to learn about Go's convention
for documenting and signaling deprecated identifiers.

Default: on.

Package documentation: [deprecated](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/deprecated)

<a id='directive'></a>
## `directive`: check Go toolchain directives such as //go:debug


This analyzer checks for problems with known Go toolchain directives
in all Go source files in a package directory, even those excluded by
//go:build constraints, and all non-Go source files too.

For //go:debug (see https://go.dev/doc/godebug), the analyzer checks
that the directives are placed only in Go source files, only above the
package comment, and only in package main or *_test.go files.

Support for other known directives may be added in the future.

This analyzer does not check //go:build, which is handled by the
buildtag analyzer.


Default: on.

Package documentation: [directive](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/directive)

<a id='embed'></a>
## `embed`: check //go:embed directive usage


This analyzer checks that the embed package is imported if //go:embed
directives are present, providing a suggested fix to add the import if
it is missing.

This analyzer also checks that //go:embed directives precede the
declaration of a single variable.

Default: on.

Package documentation: [embed](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/embeddirective)

<a id='errorsas'></a>
## `errorsas`: report passing non-pointer or non-error values to errors.As


The errorsas analysis reports calls to errors.As where the type
of the second argument is not a pointer to a type implementing error.

Default: on.

Package documentation: [errorsas](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/errorsas)

<a id='fillreturns'></a>
## `fillreturns`: suggest fixes for errors due to an incorrect number of return values


This checker provides suggested fixes for type errors of the
type "wrong number of return values (want %d, got %d)". For example:

	func m() (int, string, *bool, error) {
		return
	}

will turn into

	func m() (int, string, *bool, error) {
		return 0, "", nil, nil
	}

This functionality is similar to https://github.com/sqs/goreturns.

Default: on.

Package documentation: [fillreturns](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/fillreturns)

<a id='framepointer'></a>
## `framepointer`: report assembly that clobbers the frame pointer before saving it



Default: on.

Package documentation: [framepointer](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/framepointer)

<a id='httpresponse'></a>
## `httpresponse`: check for mistakes using HTTP responses


A common mistake when using the net/http package is to defer a function
call to close the http.Response Body before checking the error that
determines whether the response is valid:

	resp, err := http.Head(url)
	defer resp.Body.Close()
	if err != nil {
		log.Fatal(err)
	}
	// (defer statement belongs here)

This checker helps uncover latent nil dereference bugs by reporting a
diagnostic for such mistakes.

Default: on.

Package documentation: [httpresponse](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/httpresponse)

<a id='ifaceassert'></a>
## `ifaceassert`: detect impossible interface-to-interface type assertions


This checker flags type assertions v.(T) and corresponding type-switch cases
in which the static type V of v is an interface that cannot possibly implement
the target interface T. This occurs when V and T contain methods with the same
name but different signatures. Example:

	var v interface {
		Read()
	}
	_ = v.(io.Reader)

The Read method in v has a different signature than the Read method in
io.Reader, so this assertion cannot succeed.

Default: on.

Package documentation: [ifaceassert](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/ifaceassert)

<a id='infertypeargs'></a>
## `infertypeargs`: check for unnecessary type arguments in call expressions


Explicit type arguments may be omitted from call expressions if they can be
inferred from function arguments, or from other type arguments:

	func f[T any](T) {}
	
	func _() {
		f[string]("foo") // string could be inferred
	}


Default: on.

Package documentation: [infertypeargs](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/infertypeargs)

<a id='loopclosure'></a>
## `loopclosure`: check references to loop variables from within nested functions


This analyzer reports places where a function literal references the
iteration variable of an enclosing loop, and the loop calls the function
in such a way (e.g. with go or defer) that it may outlive the loop
iteration and possibly observe the wrong value of the variable.

Note: An iteration variable can only outlive a loop iteration in Go versions <=1.21.
In Go 1.22 and later, the loop variable lifetimes changed to create a new
iteration variable per loop iteration. (See go.dev/issue/60078.)

In this example, all the deferred functions run after the loop has
completed, so all observe the final value of v [<go1.22].

	for _, v := range list {
	    defer func() {
	        use(v) // incorrect
	    }()
	}

One fix is to create a new variable for each iteration of the loop:

	for _, v := range list {
	    v := v // new var per iteration
	    defer func() {
	        use(v) // ok
	    }()
	}

After Go version 1.22, the previous two for loops are equivalent
and both are correct.

The next example uses a go statement and has a similar problem [<go1.22].
In addition, it has a data race because the loop updates v
concurrent with the goroutines accessing it.

	for _, v := range elem {
	    go func() {
	        use(v)  // incorrect, and a data race
	    }()
	}

A fix is the same as before. The checker also reports problems
in goroutines started by golang.org/x/sync/errgroup.Group.
A hard-to-spot variant of this form is common in parallel tests:

	func Test(t *testing.T) {
	    for _, test := range tests {
	        t.Run(test.name, func(t *testing.T) {
	            t.Parallel()
	            use(test) // incorrect, and a data race
	        })
	    }
	}

The t.Parallel() call causes the rest of the function to execute
concurrent with the loop [<go1.22].

The analyzer reports references only in the last statement,
as it is not deep enough to understand the effects of subsequent
statements that might render the reference benign.
("Last statement" is defined recursively in compound
statements such as if, switch, and select.)

See: https://golang.org/doc/go_faq.html#closures_and_goroutines

Default: on.

Package documentation: [loopclosure](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/loopclosure)

<a id='lostcancel'></a>
## `lostcancel`: check cancel func returned by context.WithCancel is called


The cancellation function returned by context.WithCancel, WithTimeout,
WithDeadline and variants such as WithCancelCause must be called,
or the new context will remain live until its parent context is cancelled.
(The background context is never cancelled.)

Default: on.

Package documentation: [lostcancel](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/lostcancel)

<a id='nilfunc'></a>
## `nilfunc`: check for useless comparisons between functions and nil


A useless comparison is one like f == nil as opposed to f() == nil.

Default: on.

Package documentation: [nilfunc](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/nilfunc)

<a id='nilness'></a>
## `nilness`: check for redundant or impossible nil comparisons


The nilness checker inspects the control-flow graph of each function in
a package and reports nil pointer dereferences, degenerate nil
pointers, and panics with nil values. A degenerate comparison is of the form
x==nil or x!=nil where x is statically known to be nil or non-nil. These are
often a mistake, especially in control flow related to errors. Panics with nil
values are checked because they are not detectable by

	if r := recover(); r != nil {

This check reports conditions such as:

	if f == nil { // impossible condition (f is a function)
	}

and:

	p := &v
	...
	if p != nil { // tautological condition
	}

and:

	if p == nil {
		print(*p) // nil dereference
	}

and:

	if p == nil {
		panic(p)
	}

Sometimes the control flow may be quite complex, making bugs hard
to spot. In the example below, the err.Error expression is
guaranteed to panic because, after the first return, err must be
nil. The intervening loop is just a distraction.

	...
	err := g.Wait()
	if err != nil {
		return err
	}
	partialSuccess := false
	for _, err := range errs {
		if err == nil {
			partialSuccess = true
			break
		}
	}
	if partialSuccess {
		reportStatus(StatusMessage{
			Code:   code.ERROR,
			Detail: err.Error(), // "nil dereference in dynamic method call"
		})
		return nil
	}

...

Default: on.

Package documentation: [nilness](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/nilness)

<a id='nonewvars'></a>
## `nonewvars`: suggested fixes for "no new vars on left side of :="


This checker provides suggested fixes for type errors of the
type "no new vars on left side of :=". For example:

	z := 1
	z := 2

will turn into

	z := 1
	z = 2

Default: on.

Package documentation: [nonewvars](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/nonewvars)

<a id='noresultvalues'></a>
## `noresultvalues`: suggested fixes for unexpected return values


This checker provides suggested fixes for type errors of the
type "no result values expected" or "too many return values".
For example:

	func z() { return nil }

will turn into

	func z() { return }

Default: on.

Package documentation: [noresultvalues](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/noresultvalues)

<a id='printf'></a>
## `printf`: check consistency of Printf format strings and arguments


The check applies to calls of the formatting functions such as
[fmt.Printf] and [fmt.Sprintf], as well as any detected wrappers of
those functions such as [log.Printf]. It reports a variety of
mistakes such as syntax errors in the format string and mismatches
(of number and type) between the verbs and their arguments.

See the documentation of the fmt package for the complete set of
format operators and their operand types.

Default: on.

Package documentation: [printf](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/printf)

<a id='shadow'></a>
## `shadow`: check for possible unintended shadowing of variables


This analyzer check for shadowed variables.
A shadowed variable is a variable declared in an inner scope
with the same name and type as a variable in an outer scope,
and where the outer variable is mentioned after the inner one
is declared.

(This definition can be refined; the module generates too many
false positives and is not yet enabled by default.)

For example:

	func BadRead(f *os.File, buf []byte) error {
		var err error
		for {
			n, err := f.Read(buf) // shadows the function variable 'err'
			if err != nil {
				break // causes return of wrong value
			}
			foo(buf)
		}
		return err
	}

Default: off. Enable by setting `"analyses": {"shadow": true}`.

Package documentation: [shadow](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/shadow)

<a id='shift'></a>
## `shift`: check for shifts that equal or exceed the width of the integer



Default: on.

Package documentation: [shift](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/shift)

<a id='sigchanyzer'></a>
## `sigchanyzer`: check for unbuffered channel of os.Signal


This checker reports call expression of the form

	signal.Notify(c <-chan os.Signal, sig ...os.Signal),

where c is an unbuffered channel, which can be at risk of missing the signal.

Default: on.

Package documentation: [sigchanyzer](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/sigchanyzer)

<a id='simplifycompositelit'></a>
## `simplifycompositelit`: check for composite literal simplifications


An array, slice, or map composite literal of the form:

	[]T{T{}, T{}}

will be simplified to:

	[]T{{}, {}}

This is one of the simplifications that "gofmt -s" applies.

This analyzer ignores generated code.

Default: on.

Package documentation: [simplifycompositelit](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/simplifycompositelit)

<a id='simplifyrange'></a>
## `simplifyrange`: check for range statement simplifications


A range of the form:

	for x, _ = range v {...}

will be simplified to:

	for x = range v {...}

A range of the form:

	for _ = range v {...}

will be simplified to:

	for range v {...}

This is one of the simplifications that "gofmt -s" applies.

This analyzer ignores generated code.

Default: on.

Package documentation: [simplifyrange](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/simplifyrange)

<a id='simplifyslice'></a>
## `simplifyslice`: check for slice simplifications


A slice expression of the form:

	s[a:len(s)]

will be simplified to:

	s[a:]

This is one of the simplifications that "gofmt -s" applies.

This analyzer ignores generated code.

Default: on.

Package documentation: [simplifyslice](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/simplifyslice)

<a id='slog'></a>
## `slog`: check for invalid structured logging calls


The slog checker looks for calls to functions from the log/slog
package that take alternating key-value pairs. It reports calls
where an argument in a key position is neither a string nor a
slog.Attr, and where a final key is missing its value.
For example,it would report

	slog.Warn("message", 11, "k") // slog.Warn arg "11" should be a string or a slog.Attr

and

	slog.Info("message", "k1", v1, "k2") // call to slog.Info missing a final value

Default: on.

Package documentation: [slog](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/slog)

<a id='sortslice'></a>
## `sortslice`: check the argument type of sort.Slice


sort.Slice requires an argument of a slice type. Check that
the interface{} value passed to sort.Slice is actually a slice.

Default: on.

Package documentation: [sortslice](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/sortslice)

<a id='stdmethods'></a>
## `stdmethods`: check signature of methods of well-known interfaces


Sometimes a type may be intended to satisfy an interface but may fail to
do so because of a mistake in its method signature.
For example, the result of this WriteTo method should be (int64, error),
not error, to satisfy io.WriterTo:

	type myWriterTo struct{...}
	func (myWriterTo) WriteTo(w io.Writer) error { ... }

This check ensures that each method whose name matches one of several
well-known interface methods from the standard library has the correct
signature for that interface.

Checked method names include:

	Format GobEncode GobDecode MarshalJSON MarshalXML
	Peek ReadByte ReadFrom ReadRune Scan Seek
	UnmarshalJSON UnreadByte UnreadRune WriteByte
	WriteTo

Default: on.

Package documentation: [stdmethods](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/stdmethods)

<a id='stdversion'></a>
## `stdversion`: report uses of too-new standard library symbols


The stdversion analyzer reports references to symbols in the standard
library that were introduced by a Go release higher than the one in
force in the referring file. (Recall that the file's Go version is
defined by the 'go' directive its module's go.mod file, or by a
"//go:build go1.X" build tag at the top of the file.)

The analyzer does not report a diagnostic for a reference to a "too
new" field or method of a type that is itself "too new", as this may
have false positives, for example if fields or methods are accessed
through a type alias that is guarded by a Go version constraint.


Default: on.

Package documentation: [stdversion](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/stdversion)

<a id='stringintconv'></a>
## `stringintconv`: check for string(int) conversions


This checker flags conversions of the form string(x) where x is an integer
(but not byte or rune) type. Such conversions are discouraged because they
return the UTF-8 representation of the Unicode code point x, and not a decimal
string representation of x as one might expect. Furthermore, if x denotes an
invalid code point, the conversion cannot be statically rejected.

For conversions that intend on using the code point, consider replacing them
with string(rune(x)). Otherwise, strconv.Itoa and its equivalents return the
string representation of the value in the desired base.

Default: on.

Package documentation: [stringintconv](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/stringintconv)

<a id='structtag'></a>
## `structtag`: check that struct field tags conform to reflect.StructTag.Get


Also report certain struct tags (json, xml) used with unexported fields.

Default: on.

Package documentation: [structtag](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/structtag)

<a id='testinggoroutine'></a>
## `testinggoroutine`: report calls to (*testing.T).Fatal from goroutines started by a test


Functions that abruptly terminate a test, such as the Fatal, Fatalf, FailNow, and
Skip{,f,Now} methods of *testing.T, must be called from the test goroutine itself.
This checker detects calls to these functions that occur within a goroutine
started by the test. For example:

	func TestFoo(t *testing.T) {
	    go func() {
	        t.Fatal("oops") // error: (*T).Fatal called from non-test goroutine
	    }()
	}

Default: on.

Package documentation: [testinggoroutine](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/testinggoroutine)

<a id='tests'></a>
## `tests`: check for common mistaken usages of tests and examples


The tests checker walks Test, Benchmark, Fuzzing and Example functions checking
malformed names, wrong signatures and examples documenting non-existent
identifiers.

Please see the documentation for package testing in golang.org/pkg/testing
for the conventions that are enforced for Tests, Benchmarks, and Examples.

Default: on.

Package documentation: [tests](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/tests)

<a id='timeformat'></a>
## `timeformat`: check for calls of (time.Time).Format or time.Parse with 2006-02-01


The timeformat checker looks for time formats with the 2006-02-01 (yyyy-dd-mm)
format. Internationally, "yyyy-dd-mm" does not occur in common calendar date
standards, and so it is more likely that 2006-01-02 (yyyy-mm-dd) was intended.

Default: on.

Package documentation: [timeformat](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/timeformat)

<a id='unmarshal'></a>
## `unmarshal`: report passing non-pointer or non-interface values to unmarshal


The unmarshal analysis reports calls to functions such as json.Unmarshal
in which the argument type is not a pointer or an interface.

Default: on.

Package documentation: [unmarshal](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/unmarshal)

<a id='unreachable'></a>
## `unreachable`: check for unreachable code


The unreachable analyzer finds statements that execution can never reach
because they are preceded by an return statement, a call to panic, an
infinite loop, or similar constructs.

Default: on.

Package documentation: [unreachable](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/unreachable)

<a id='unsafeptr'></a>
## `unsafeptr`: check for invalid conversions of uintptr to unsafe.Pointer


The unsafeptr analyzer reports likely incorrect uses of unsafe.Pointer
to convert integers to pointers. A conversion from uintptr to
unsafe.Pointer is invalid if it implies that there is a uintptr-typed
word in memory that holds a pointer value, because that word will be
invisible to stack copying and to the garbage collector.

Default: on.

Package documentation: [unsafeptr](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/unsafeptr)

<a id='unusedparams'></a>
## `unusedparams`: check for unused parameters of functions


The unusedparams analyzer checks functions to see if there are
any parameters that are not being used.

To ensure soundness, it ignores:
  - "address-taken" functions, that is, functions that are used as
    a value rather than being called directly; their signatures may
    be required to conform to a func type.
  - exported functions or methods, since they may be address-taken
    in another package.
  - unexported methods whose name matches an interface method
    declared in the same package, since the method's signature
    may be required to conform to the interface type.
  - functions with empty bodies, or containing just a call to panic.
  - parameters that are unnamed, or named "_", the blank identifier.

The analyzer suggests a fix of replacing the parameter name by "_",
but in such cases a deeper fix can be obtained by invoking the
"Refactor: remove unused parameter" code action, which will
eliminate the parameter entirely, along with all corresponding
arguments at call sites, while taking care to preserve any side
effects in the argument expressions; see
https://github.com/golang/tools/releases/tag/gopls%2Fv0.14.

Default: on.

Package documentation: [unusedparams](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/unusedparams)

<a id='unusedresult'></a>
## `unusedresult`: check for unused results of calls to some functions


Some functions like fmt.Errorf return a result and have no side
effects, so it is always a mistake to discard the result. Other
functions may return an error that must not be ignored, or a cleanup
operation that must be called. This analyzer reports calls to
functions like these when the result of the call is ignored.

The set of functions may be controlled using flags.

Default: on.

Package documentation: [unusedresult](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/unusedresult)

<a id='unusedvariable'></a>
## `unusedvariable`: check for unused variables and suggest fixes



Default: off. Enable by setting `"analyses": {"unusedvariable": true}`.

Package documentation: [unusedvariable](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/unusedvariable)

<a id='unusedwrite'></a>
## `unusedwrite`: checks for unused writes


The analyzer reports instances of writes to struct fields and
arrays that are never read. Specifically, when a struct object
or an array is copied, its elements are copied implicitly by
the compiler, and any element write to this copy does nothing
with the original object.

For example:

	type T struct { x int }

	func f(input []T) {
		for i, v := range input {  // v is a copy
			v.x = i  // unused write to field x
		}
	}

Another example is about non-pointer receiver:

	type T struct { x int }

	func (t T) f() {  // t is a copy
		t.x = i  // unused write to field x
	}

Default: on.

Package documentation: [unusedwrite](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/unusedwrite)

<a id='useany'></a>
## `useany`: check for constraints that could be simplified to "any"



Default: off. Enable by setting `"analyses": {"useany": true}`.

Package documentation: [useany](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/useany)

<a id='waitgroup'></a>
## `waitgroup`: check for misuses of sync.WaitGroup


This analyzer detects mistaken calls to the (*sync.WaitGroup).Add
method from inside a new goroutine, causing Add to race with Wait:

	// WRONG
	var wg sync.WaitGroup
	go func() {
	        wg.Add(1) // "WaitGroup.Add called from inside new goroutine"
	        defer wg.Done()
	        ...
	}()
	wg.Wait() // (may return prematurely before new goroutine starts)

The correct code calls Add before starting the goroutine:

	// RIGHT
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		...
	}()
	wg.Wait()

Default: on.

Package documentation: [waitgroup](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/waitgroup)

<a id='yield'></a>
## `yield`: report calls to yield where the result is ignored


After a yield function returns false, the caller should not call
the yield function again; generally the iterator should return
promptly.

This example fails to check the result of the call to yield,
causing this analyzer to report a diagnostic:

	yield(1) // yield may be called again (on L2) after returning false
	yield(2)

The corrected code is either this:

	if yield(1) { yield(2) }

or simply:

	_ = yield(1) && yield(2)

It is not always a mistake to ignore the result of yield.
For example, this is a valid single-element iterator:

	yield(1) // ok to ignore result
	return

It is only a mistake when the yield call that returned false may be
followed by another call.

Default: on.

Package documentation: [yield](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/yield)

<!-- END Analyzers: DO NOT MANUALLY EDIT THIS SECTION -->
