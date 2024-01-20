# Analyzers

This document describes the analyzers that `gopls` uses inside the editor.

Details about how to enable/disable these analyses can be found
[here](settings.md#analyses).

<!-- BEGIN Analyzers: DO NOT MANUALLY EDIT THIS SECTION -->
## **appends**

appends: check for missing values after append

This checker reports calls to append that pass
no values to be appended to the slice.

	s := []string{"a", "b", "c"}
	_ = append(s)

Such calls are always no-ops and often indicate an
underlying mistake.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/appends)

**Enabled by default.**

## **asmdecl**

asmdecl: report mismatches between assembly files and Go declarations

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/asmdecl)

**Enabled by default.**

## **assign**

assign: check for useless assignments

This checker reports assignments of the form x = x or a[i] = a[i].
These are almost always useless, and even when they aren't they are
usually a mistake.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/assign)

**Enabled by default.**

## **atomic**

atomic: check for common mistakes using the sync/atomic package

The atomic checker looks for assignment statements of the form:

	x = atomic.AddUint64(&x, 1)

which are not atomic.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/atomic)

**Enabled by default.**

## **atomicalign**

atomicalign: check for non-64-bits-aligned arguments to sync/atomic functions

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/atomicalign)

**Enabled by default.**

## **bools**

bools: check for common mistakes involving boolean operators

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/bools)

**Enabled by default.**

## **buildtag**

buildtag: check //go:build and // +build directives

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/buildtag)

**Enabled by default.**

## **cgocall**

cgocall: detect some violations of the cgo pointer passing rules

Check for invalid cgo pointer passing.
This looks for code that uses cgo to call C code passing values
whose types are almost always invalid according to the cgo pointer
sharing rules.
Specifically, it warns about attempts to pass a Go chan, map, func,
or slice to C, either directly, or via a pointer, array, or struct.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/cgocall)

**Enabled by default.**

## **composites**

composites: check for unkeyed composite literals

This analyzer reports a diagnostic for composite literals of struct
types imported from another package that do not use the field-keyed
syntax. Such literals are fragile because the addition of a new field
(even if unexported) to the struct will cause compilation to fail.

As an example,

	err = &net.DNSConfigError{err}

should be replaced by:

	err = &net.DNSConfigError{Err: err}


[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/composite)

**Enabled by default.**

## **copylocks**

copylocks: check for locks erroneously passed by value

Inadvertently copying a value containing a lock, such as sync.Mutex or
sync.WaitGroup, may cause both copies to malfunction. Generally such
values should be referred to through a pointer.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/copylocks)

**Enabled by default.**

## **deepequalerrors**

deepequalerrors: check for calls of reflect.DeepEqual on error values

The deepequalerrors checker looks for calls of the form:

    reflect.DeepEqual(err1, err2)

where err1 and err2 are errors. Using reflect.DeepEqual to compare
errors is discouraged.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/deepequalerrors)

**Enabled by default.**

## **defers**

defers: report common mistakes in defer statements

The defers analyzer reports a diagnostic when a defer statement would
result in a non-deferred call to time.Since, as experience has shown
that this is nearly always a mistake.

For example:

	start := time.Now()
	...
	defer recordLatency(time.Since(start)) // error: call to time.Since is not deferred

The correct code is:

	defer func() { recordLatency(time.Since(start)) }()

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/defers)

**Enabled by default.**

## **deprecated**

deprecated: check for use of deprecated identifiers

The deprecated analyzer looks for deprecated symbols and package
imports.

See https://go.dev/wiki/Deprecated to learn about Go's convention
for documenting and signaling deprecated identifiers.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/deprecated)

**Enabled by default.**

## **directive**

directive: check Go toolchain directives such as //go:debug

This analyzer checks for problems with known Go toolchain directives
in all Go source files in a package directory, even those excluded by
//go:build constraints, and all non-Go source files too.

For //go:debug (see https://go.dev/doc/godebug), the analyzer checks
that the directives are placed only in Go source files, only above the
package comment, and only in package main or *_test.go files.

Support for other known directives may be added in the future.

This analyzer does not check //go:build, which is handled by the
buildtag analyzer.


[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/directive)

**Enabled by default.**

## **embed**

embed: check //go:embed directive usage

This analyzer checks that the embed package is imported if //go:embed
directives are present, providing a suggested fix to add the import if
it is missing.

This analyzer also checks that //go:embed directives precede the
declaration of a single variable.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/embeddirective)

**Enabled by default.**

## **errorsas**

errorsas: report passing non-pointer or non-error values to errors.As

The errorsas analysis reports calls to errors.As where the type
of the second argument is not a pointer to a type implementing error.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/errorsas)

**Enabled by default.**

## **fieldalignment**

fieldalignment: find structs that would use less memory if their fields were sorted

This analyzer find structs that can be rearranged to use less memory, and provides
a suggested edit with the most compact order.

Note that there are two different diagnostics reported. One checks struct size,
and the other reports "pointer bytes" used. Pointer bytes is how many bytes of the
object that the garbage collector has to potentially scan for pointers, for example:

	struct { uint32; string }

have 16 pointer bytes because the garbage collector has to scan up through the string's
inner pointer.

	struct { string; *uint32 }

has 24 pointer bytes because it has to scan further through the *uint32.

	struct { string; uint32 }

has 8 because it can stop immediately after the string pointer.

Be aware that the most compact order is not always the most efficient.
In rare cases it may cause two variables each updated by its own goroutine
to occupy the same CPU cache line, inducing a form of memory contention
known as "false sharing" that slows down both goroutines.


[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/fieldalignment)

**Disabled by default. Enable it by setting `"analyses": {"fieldalignment": true}`.**

## **httpresponse**

httpresponse: check for mistakes using HTTP responses

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

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/httpresponse)

**Enabled by default.**

## **ifaceassert**

ifaceassert: detect impossible interface-to-interface type assertions

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

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/ifaceassert)

**Enabled by default.**

## **loopclosure**

loopclosure: check references to loop variables from within nested functions

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

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/loopclosure)

**Enabled by default.**

## **lostcancel**

lostcancel: check cancel func returned by context.WithCancel is called

The cancellation function returned by context.WithCancel, WithTimeout,
and WithDeadline must be called or the new context will remain live
until its parent context is cancelled.
(The background context is never cancelled.)

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/lostcancel)

**Enabled by default.**

## **nilfunc**

nilfunc: check for useless comparisons between functions and nil

A useless comparison is one like f == nil as opposed to f() == nil.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/nilfunc)

**Enabled by default.**

## **nilness**

nilness: check for redundant or impossible nil comparisons

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

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/nilness)

**Enabled by default.**

## **printf**

printf: check consistency of Printf format strings and arguments

The check applies to calls of the formatting functions such as
[fmt.Printf] and [fmt.Sprintf], as well as any detected wrappers of
those functions.

In this example, the %d format operator requires an integer operand:

	fmt.Printf("%d", "hello") // fmt.Printf format %d has arg "hello" of wrong type string

See the documentation of the fmt package for the complete set of
format operators and their operand types.

To enable printf checking on a function that is not found by this
analyzer's heuristics (for example, because control is obscured by
dynamic method calls), insert a bogus call:

	func MyPrintf(format string, args ...any) {
		if false {
			_ = fmt.Sprintf(format, args...) // enable printf checker
		}
		...
	}

The -funcs flag specifies a comma-separated list of names of additional
known formatting functions or methods. If the name contains a period,
it must denote a specific function using one of the following forms:

	dir/pkg.Function
	dir/pkg.Type.Method
	(*dir/pkg.Type).Method

Otherwise the name is interpreted as a case-insensitive unqualified
identifier such as "errorf". Either way, if a listed name ends in f, the
function is assumed to be Printf-like, taking a format string before the
argument list. Otherwise it is assumed to be Print-like, taking a list
of arguments with no format string.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/printf)

**Enabled by default.**

## **shadow**

shadow: check for possible unintended shadowing of variables

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

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/shadow)

**Disabled by default. Enable it by setting `"analyses": {"shadow": true}`.**

## **shift**

shift: check for shifts that equal or exceed the width of the integer

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/shift)

**Enabled by default.**

## **simplifycompositelit**

simplifycompositelit: check for composite literal simplifications

An array, slice, or map composite literal of the form:

	[]T{T{}, T{}}

will be simplified to:

	[]T{{}, {}}

This is one of the simplifications that "gofmt -s" applies.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/simplifycompositelit)

**Enabled by default.**

## **simplifyrange**

simplifyrange: check for range statement simplifications

A range of the form:

	for x, _ = range v {...}

will be simplified to:

	for x = range v {...}

A range of the form:

	for _ = range v {...}

will be simplified to:

	for range v {...}

This is one of the simplifications that "gofmt -s" applies.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/simplifyrange)

**Enabled by default.**

## **simplifyslice**

simplifyslice: check for slice simplifications

A slice expression of the form:

	s[a:len(s)]

will be simplified to:

	s[a:]

This is one of the simplifications that "gofmt -s" applies.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/simplifyslice)

**Enabled by default.**

## **slog**

slog: check for invalid structured logging calls

The slog checker looks for calls to functions from the log/slog
package that take alternating key-value pairs. It reports calls
where an argument in a key position is neither a string nor a
slog.Attr, and where a final key is missing its value.
For example,it would report

	slog.Warn("message", 11, "k") // slog.Warn arg "11" should be a string or a slog.Attr

and

	slog.Info("message", "k1", v1, "k2") // call to slog.Info missing a final value

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/slog)

**Enabled by default.**

## **sortslice**

sortslice: check the argument type of sort.Slice

sort.Slice requires an argument of a slice type. Check that
the interface{} value passed to sort.Slice is actually a slice.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/sortslice)

**Enabled by default.**

## **stdmethods**

stdmethods: check signature of methods of well-known interfaces

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

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/stdmethods)

**Enabled by default.**

## **stringintconv**

stringintconv: check for string(int) conversions

This checker flags conversions of the form string(x) where x is an integer
(but not byte or rune) type. Such conversions are discouraged because they
return the UTF-8 representation of the Unicode code point x, and not a decimal
string representation of x as one might expect. Furthermore, if x denotes an
invalid code point, the conversion cannot be statically rejected.

For conversions that intend on using the code point, consider replacing them
with string(rune(x)). Otherwise, strconv.Itoa and its equivalents return the
string representation of the value in the desired base.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/stringintconv)

**Enabled by default.**

## **structtag**

structtag: check that struct field tags conform to reflect.StructTag.Get

Also report certain struct tags (json, xml) used with unexported fields.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/structtag)

**Enabled by default.**

## **testinggoroutine**

testinggoroutine: report calls to (*testing.T).Fatal from goroutines started by a test

Functions that abruptly terminate a test, such as the Fatal, Fatalf, FailNow, and
Skip{,f,Now} methods of *testing.T, must be called from the test goroutine itself.
This checker detects calls to these functions that occur within a goroutine
started by the test. For example:

	func TestFoo(t *testing.T) {
	    go func() {
	        t.Fatal("oops") // error: (*T).Fatal called from non-test goroutine
	    }()
	}

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/testinggoroutine)

**Enabled by default.**

## **tests**

tests: check for common mistaken usages of tests and examples

The tests checker walks Test, Benchmark, Fuzzing and Example functions checking
malformed names, wrong signatures and examples documenting non-existent
identifiers.

Please see the documentation for package testing in golang.org/pkg/testing
for the conventions that are enforced for Tests, Benchmarks, and Examples.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/tests)

**Enabled by default.**

## **timeformat**

timeformat: check for calls of (time.Time).Format or time.Parse with 2006-02-01

The timeformat checker looks for time formats with the 2006-02-01 (yyyy-dd-mm)
format. Internationally, "yyyy-dd-mm" does not occur in common calendar date
standards, and so it is more likely that 2006-01-02 (yyyy-mm-dd) was intended.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/timeformat)

**Enabled by default.**

## **unmarshal**

unmarshal: report passing non-pointer or non-interface values to unmarshal

The unmarshal analysis reports calls to functions such as json.Unmarshal
in which the argument type is not a pointer or an interface.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/unmarshal)

**Enabled by default.**

## **unreachable**

unreachable: check for unreachable code

The unreachable analyzer finds statements that execution can never reach
because they are preceded by an return statement, a call to panic, an
infinite loop, or similar constructs.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/unreachable)

**Enabled by default.**

## **unsafeptr**

unsafeptr: check for invalid conversions of uintptr to unsafe.Pointer

The unsafeptr analyzer reports likely incorrect uses of unsafe.Pointer
to convert integers to pointers. A conversion from uintptr to
unsafe.Pointer is invalid if it implies that there is a uintptr-typed
word in memory that holds a pointer value, because that word will be
invisible to stack copying and to the garbage collector.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/unsafeptr)

**Enabled by default.**

## **unusedparams**

unusedparams: check for unused parameters of functions

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

[Full documentation](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/unusedparams)

**Enabled by default.**

## **unusedresult**

unusedresult: check for unused results of calls to some functions

Some functions like fmt.Errorf return a result and have no side
effects, so it is always a mistake to discard the result. Other
functions may return an error that must not be ignored, or a cleanup
operation that must be called. This analyzer reports calls to
functions like these when the result of the call is ignored.

The set of functions may be controlled using flags.

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/unusedresult)

**Enabled by default.**

## **unusedwrite**

unusedwrite: checks for unused writes

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

[Full documentation](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/unusedwrite)

**Disabled by default. Enable it by setting `"analyses": {"unusedwrite": true}`.**

## **useany**

useany: check for constraints that could be simplified to "any"

[Full documentation](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/useany)

**Disabled by default. Enable it by setting `"analyses": {"useany": true}`.**

## **fillreturns**

fillreturns: suggest fixes for errors due to an incorrect number of return values

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

[Full documentation](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/fillreturns)

**Enabled by default.**

## **nonewvars**

nonewvars: suggested fixes for "no new vars on left side of :="

This checker provides suggested fixes for type errors of the
type "no new vars on left side of :=". For example:

	z := 1
	z := 2

will turn into

	z := 1
	z = 2

[Full documentation](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/nonewvars)

**Enabled by default.**

## **noresultvalues**

noresultvalues: suggested fixes for unexpected return values

This checker provides suggested fixes for type errors of the
type "no result values expected" or "too many return values".
For example:

	func z() { return nil }

will turn into

	func z() { return }

[Full documentation](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/noresultvars)

**Enabled by default.**

## **undeclaredname**

undeclaredname: suggested fixes for "undeclared name: <>"

This checker provides suggested fixes for type errors of the
type "undeclared name: <>". It will either insert a new statement,
such as:

	<> :=

or a new function declaration, such as:

	func <>(inferred parameters) {
		panic("implement me!")
	}

[Full documentation](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/undeclaredname)

**Enabled by default.**

## **unusedvariable**

unusedvariable: check for unused variables and suggest fixes

[Full documentation](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/unusedvariable)

**Disabled by default. Enable it by setting `"analyses": {"unusedvariable": true}`.**

## **fillstruct**

fillstruct: note incomplete struct initializations

This analyzer provides diagnostics for any struct literals that do not have
any fields initialized. Because the suggested fix for this analysis is
expensive to compute, callers should compute it separately, using the
SuggestedFix function below.


[Full documentation](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/fillstruct)

**Enabled by default.**

## **infertypeargs**

infertypeargs: check for unnecessary type arguments in call expressions

Explicit type arguments may be omitted from call expressions if they can be
inferred from function arguments, or from other type arguments:

	func f[T any](T) {}
	
	func _() {
		f[string]("foo") // string could be inferred
	}


[Full documentation](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/infertypeargs)

**Enabled by default.**

## **stubmethods**

stubmethods: detect missing methods and fix with stub implementations

This analyzer detects type-checking errors due to missing methods
in assignments from concrete types to interface types, and offers
a suggested fix that will create a set of stub methods so that
the concrete type satisfies the interface.

For example, this function will not compile because the value
NegativeErr{} does not implement the "error" interface:

	func sqrt(x float64) (float64, error) {
		if x < 0 {
			return 0, NegativeErr{} // error: missing method
		}
		...
	}

	type NegativeErr struct{}

This analyzer will suggest a fix to declare this method:

	// Error implements error.Error.
	func (NegativeErr) Error() string {
		panic("unimplemented")
	}

(At least, it appears to behave that way, but technically it
doesn't use the SuggestedFix mechanism and the stub is created by
logic in gopls's source.stub function.)

[Full documentation](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/stubmethods)

**Enabled by default.**

<!-- END Analyzers: DO NOT MANUALLY EDIT THIS SECTION -->
