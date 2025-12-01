---
title: "Gopls: Analyzers"
---

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

In addition, gopls includes the [`staticcheck` suite](https://staticcheck.dev/docs/checks).
When the [`staticcheck`](settings.md#staticcheck`) boolean option is
unset, slightly more than half of these analyzers are enabled by
default; this subset has been chosen for precision and efficiency. Set
`staticcheck` to `true` to enable the complete set, or to `false` to
disable the complete set.

Staticcheck analyzers, like all other analyzers, can be explicitly
enabled or disabled using the `analyzers` configuration setting; this
setting takes precedence over the `staticcheck` setting, so,
regardless of what value of `staticcheck` you use (true/false/unset),
you can make adjustments to your preferred set of analyzers.


<!-- BEGIN Analyzers: DO NOT MANUALLY EDIT THIS SECTION -->
<a id='QF1001'></a>
## `QF1001`: Apply De Morgan's law

Available since

	2021.1


Default: off. Enable by setting `"analyses": {"QF1001": true}`.

Package documentation: [QF1001](https://staticcheck.dev/docs/checks/#QF1001)

<a id='QF1002'></a>
## `QF1002`: Convert untagged switch to tagged switch

An untagged switch that compares a single variable against a series of values can be replaced with a tagged switch.

Before:

	switch {
	case x == 1 || x == 2, x == 3:
	    ...
	case x == 4:
	    ...
	default:
	    ...
	}

After:

	switch x {
	case 1, 2, 3:
	    ...
	case 4:
	    ...
	default:
	    ...
	}

Available since

	2021.1


Default: on.

Package documentation: [QF1002](https://staticcheck.dev/docs/checks/#QF1002)

<a id='QF1003'></a>
## `QF1003`: Convert if/else-if chain to tagged switch

A series of if/else-if checks comparing the same variable against values can be replaced with a tagged switch.

Before:

	if x == 1 || x == 2 {
	    ...
	} else if x == 3 {
	    ...
	} else {
	    ...
	}

After:

	switch x {
	case 1, 2:
	    ...
	case 3:
	    ...
	default:
	    ...
	}

Available since

	2021.1


Default: on.

Package documentation: [QF1003](https://staticcheck.dev/docs/checks/#QF1003)

<a id='QF1004'></a>
## `QF1004`: Use strings.ReplaceAll instead of strings.Replace with n == -1

Available since

	2021.1


Default: on.

Package documentation: [QF1004](https://staticcheck.dev/docs/checks/#QF1004)

<a id='QF1005'></a>
## `QF1005`: Expand call to math.Pow

Some uses of math.Pow can be simplified to basic multiplication.

Before:

	math.Pow(x, 2)

After:

	x * x

Available since

	2021.1


Default: off. Enable by setting `"analyses": {"QF1005": true}`.

Package documentation: [QF1005](https://staticcheck.dev/docs/checks/#QF1005)

<a id='QF1006'></a>
## `QF1006`: Lift if+break into loop condition

Before:

	for {
	    if done {
	        break
	    }
	    ...
	}

After:

	for !done {
	    ...
	}

Available since

	2021.1


Default: off. Enable by setting `"analyses": {"QF1006": true}`.

Package documentation: [QF1006](https://staticcheck.dev/docs/checks/#QF1006)

<a id='QF1007'></a>
## `QF1007`: Merge conditional assignment into variable declaration

Before:

	x := false
	if someCondition {
	    x = true
	}

After:

	x := someCondition

Available since

	2021.1


Default: off. Enable by setting `"analyses": {"QF1007": true}`.

Package documentation: [QF1007](https://staticcheck.dev/docs/checks/#QF1007)

<a id='QF1008'></a>
## `QF1008`: Omit embedded fields from selector expression

Available since

	2021.1


Default: off. Enable by setting `"analyses": {"QF1008": true}`.

Package documentation: [QF1008](https://staticcheck.dev/docs/checks/#QF1008)

<a id='QF1009'></a>
## `QF1009`: Use time.Time.Equal instead of == operator

Available since

	2021.1


Default: on.

Package documentation: [QF1009](https://staticcheck.dev/docs/checks/#QF1009)

<a id='QF1010'></a>
## `QF1010`: Convert slice of bytes to string when printing it

Available since

	2021.1


Default: on.

Package documentation: [QF1010](https://staticcheck.dev/docs/checks/#QF1010)

<a id='QF1011'></a>
## `QF1011`: Omit redundant type from variable declaration

Available since

	2021.1


Default: off. Enable by setting `"analyses": {"QF1011": true}`.

Package documentation: [QF1011](https://staticcheck.dev/docs/checks/#)

<a id='QF1012'></a>
## `QF1012`: Use fmt.Fprintf(x, ...) instead of x.Write(fmt.Sprintf(...))

Available since

	2022.1


Default: on.

Package documentation: [QF1012](https://staticcheck.dev/docs/checks/#QF1012)

<a id='S1000'></a>
## `S1000`: Use plain channel send or receive instead of single-case select

Select statements with a single case can be replaced with a simple send or receive.

Before:

	select {
	case x := <-ch:
	    fmt.Println(x)
	}

After:

	x := <-ch
	fmt.Println(x)

Available since

	2017.1


Default: on.

Package documentation: [S1000](https://staticcheck.dev/docs/checks/#S1000)

<a id='S1001'></a>
## `S1001`: Replace for loop with call to copy

Use copy() for copying elements from one slice to another. For arrays of identical size, you can use simple assignment.

Before:

	for i, x := range src {
	    dst[i] = x
	}

After:

	copy(dst, src)

Available since

	2017.1


Default: on.

Package documentation: [S1001](https://staticcheck.dev/docs/checks/#S1001)

<a id='S1002'></a>
## `S1002`: Omit comparison with boolean constant

Before:

	if x == true {}

After:

	if x {}

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"S1002": true}`.

Package documentation: [S1002](https://staticcheck.dev/docs/checks/#S1002)

<a id='S1003'></a>
## `S1003`: Replace call to strings.Index with strings.Contains

Before:

	if strings.Index(x, y) != -1 {}

After:

	if strings.Contains(x, y) {}

Available since

	2017.1


Default: on.

Package documentation: [S1003](https://staticcheck.dev/docs/checks/#S1003)

<a id='S1004'></a>
## `S1004`: Replace call to bytes.Compare with bytes.Equal

Before:

	if bytes.Compare(x, y) == 0 {}

After:

	if bytes.Equal(x, y) {}

Available since

	2017.1


Default: on.

Package documentation: [S1004](https://staticcheck.dev/docs/checks/#S1004)

<a id='S1005'></a>
## `S1005`: Drop unnecessary use of the blank identifier

In many cases, assigning to the blank identifier is unnecessary.

Before:

	for _ = range s {}
	x, _ = someMap[key]
	_ = <-ch

After:

	for range s{}
	x = someMap[key]
	<-ch

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"S1005": true}`.

Package documentation: [S1005](https://staticcheck.dev/docs/checks/#S1005)

<a id='S1006'></a>
## `S1006`: Use 'for { ... }' for infinite loops

For infinite loops, using for { ... } is the most idiomatic choice.

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"S1006": true}`.

Package documentation: [S1006](https://staticcheck.dev/docs/checks/#S1006)

<a id='S1007'></a>
## `S1007`: Simplify regular expression by using raw string literal

Raw string literals use backticks instead of quotation marks and do not support any escape sequences. This means that the backslash can be used freely, without the need of escaping.

Since regular expressions have their own escape sequences, raw strings can improve their readability.

Before:

	regexp.Compile("\\A(\\w+) profile: total \\d+\\n\\z")

After:

	regexp.Compile(`\A(\w+) profile: total \d+\n\z`)

Available since

	2017.1


Default: on.

Package documentation: [S1007](https://staticcheck.dev/docs/checks/#S1007)

<a id='S1008'></a>
## `S1008`: Simplify returning boolean expression

Before:

	if <expr> {
	    return true
	}
	return false

After:

	return <expr>

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"S1008": true}`.

Package documentation: [S1008](https://staticcheck.dev/docs/checks/#S1008)

<a id='S1009'></a>
## `S1009`: Omit redundant nil check on slices, maps, and channels

The len function is defined for all slices, maps, and channels, even nil ones, which have a length of zero. It is not necessary to check for nil before checking that their length is not zero.

Before:

	if x != nil && len(x) != 0 {}

After:

	if len(x) != 0 {}

Available since

	2017.1


Default: on.

Package documentation: [S1009](https://staticcheck.dev/docs/checks/#S1009)

<a id='S1010'></a>
## `S1010`: Omit default slice index

When slicing, the second index defaults to the length of the value, making s\[n:len(s)] and s\[n:] equivalent.

Available since

	2017.1


Default: on.

Package documentation: [S1010](https://staticcheck.dev/docs/checks/#S1010)

<a id='S1011'></a>
## `S1011`: Use a single append to concatenate two slices

Before:

	for _, e := range y {
	    x = append(x, e)
	}

	for i := range y {
	    x = append(x, y[i])
	}

	for i := range y {
	    v := y[i]
	    x = append(x, v)
	}

After:

	x = append(x, y...)
	x = append(x, y...)
	x = append(x, y...)

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"S1011": true}`.

Package documentation: [S1011](https://staticcheck.dev/docs/checks/#S1011)

<a id='S1012'></a>
## `S1012`: Replace time.Now().Sub(x) with time.Since(x)

The time.Since helper has the same effect as using time.Now().Sub(x) but is easier to read.

Before:

	time.Now().Sub(x)

After:

	time.Since(x)

Available since

	2017.1


Default: on.

Package documentation: [S1012](https://staticcheck.dev/docs/checks/#S1012)

<a id='S1016'></a>
## `S1016`: Use a type conversion instead of manually copying struct fields

Two struct types with identical fields can be converted between each other. In older versions of Go, the fields had to have identical struct tags. Since Go 1.8, however, struct tags are ignored during conversions. It is thus not necessary to manually copy every field individually.

Before:

	var x T1
	y := T2{
	    Field1: x.Field1,
	    Field2: x.Field2,
	}

After:

	var x T1
	y := T2(x)

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"S1016": true}`.

Package documentation: [S1016](https://staticcheck.dev/docs/checks/#S1016)

<a id='S1017'></a>
## `S1017`: Replace manual trimming with strings.TrimPrefix

Instead of using strings.HasPrefix and manual slicing, use the strings.TrimPrefix function. If the string doesn't start with the prefix, the original string will be returned. Using strings.TrimPrefix reduces complexity, and avoids common bugs, such as off-by-one mistakes.

Before:

	if strings.HasPrefix(str, prefix) {
	    str = str[len(prefix):]
	}

After:

	str = strings.TrimPrefix(str, prefix)

Available since

	2017.1


Default: on.

Package documentation: [S1017](https://staticcheck.dev/docs/checks/#S1017)

<a id='S1018'></a>
## `S1018`: Use 'copy' for sliding elements

copy() permits using the same source and destination slice, even with overlapping ranges. This makes it ideal for sliding elements in a slice.

Before:

	for i := 0; i < n; i++ {
	    bs[i] = bs[offset+i]
	}

After:

	copy(bs[:n], bs[offset:])

Available since

	2017.1


Default: on.

Package documentation: [S1018](https://staticcheck.dev/docs/checks/#S1018)

<a id='S1019'></a>
## `S1019`: Simplify 'make' call by omitting redundant arguments

The 'make' function has default values for the length and capacity arguments. For channels, the length defaults to zero, and for slices, the capacity defaults to the length.

Available since

	2017.1


Default: on.

Package documentation: [S1019](https://staticcheck.dev/docs/checks/#S1019)

<a id='S1020'></a>
## `S1020`: Omit redundant nil check in type assertion

Before:

	if _, ok := i.(T); ok && i != nil {}

After:

	if _, ok := i.(T); ok {}

Available since

	2017.1


Default: on.

Package documentation: [S1020](https://staticcheck.dev/docs/checks/#S1020)

<a id='S1021'></a>
## `S1021`: Merge variable declaration and assignment

Before:

	var x uint
	x = 1

After:

	var x uint = 1

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"S1021": true}`.

Package documentation: [S1021](https://staticcheck.dev/docs/checks/#S1021)

<a id='S1023'></a>
## `S1023`: Omit redundant control flow

Functions that have no return value do not need a return statement as the final statement of the function.

Switches in Go do not have automatic fallthrough, unlike languages like C. It is not necessary to have a break statement as the final statement in a case block.

Available since

	2017.1


Default: on.

Package documentation: [S1023](https://staticcheck.dev/docs/checks/#S1023)

<a id='S1024'></a>
## `S1024`: Replace x.Sub(time.Now()) with time.Until(x)

The time.Until helper has the same effect as using x.Sub(time.Now()) but is easier to read.

Before:

	x.Sub(time.Now())

After:

	time.Until(x)

Available since

	2017.1


Default: on.

Package documentation: [S1024](https://staticcheck.dev/docs/checks/#S1024)

<a id='S1025'></a>
## `S1025`: Don't use fmt.Sprintf("%s", x) unnecessarily

In many instances, there are easier and more efficient ways of getting a value's string representation. Whenever a value's underlying type is a string already, or the type has a String method, they should be used directly.

Given the following shared definitions

	type T1 string
	type T2 int

	func (T2) String() string { return "Hello, world" }

	var x string
	var y T1
	var z T2

we can simplify

	fmt.Sprintf("%s", x)
	fmt.Sprintf("%s", y)
	fmt.Sprintf("%s", z)

to

	x
	string(y)
	z.String()

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"S1025": true}`.

Package documentation: [S1025](https://staticcheck.dev/docs/checks/#S1025)

<a id='S1028'></a>
## `S1028`: Simplify error construction with fmt.Errorf

Before:

	errors.New(fmt.Sprintf(...))

After:

	fmt.Errorf(...)

Available since

	2017.1


Default: on.

Package documentation: [S1028](https://staticcheck.dev/docs/checks/#S1028)

<a id='S1029'></a>
## `S1029`: Range over the string directly

Ranging over a string will yield byte offsets and runes. If the offset isn't used, this is functionally equivalent to converting the string to a slice of runes and ranging over that. Ranging directly over the string will be more performant, however, as it avoids allocating a new slice, the size of which depends on the length of the string.

Before:

	for _, r := range []rune(s) {}

After:

	for _, r := range s {}

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"S1029": true}`.

Package documentation: [S1029](https://staticcheck.dev/docs/checks/#S1029)

<a id='S1030'></a>
## `S1030`: Use bytes.Buffer.String or bytes.Buffer.Bytes

bytes.Buffer has both a String and a Bytes method. It is almost never necessary to use string(buf.Bytes()) or \[]byte(buf.String()) – simply use the other method.

The only exception to this are map lookups. Due to a compiler optimization, m\[string(buf.Bytes())] is more efficient than m\[buf.String()].

Available since

	2017.1


Default: on.

Package documentation: [S1030](https://staticcheck.dev/docs/checks/#S1030)

<a id='S1031'></a>
## `S1031`: Omit redundant nil check around loop

You can use range on nil slices and maps, the loop will simply never execute. This makes an additional nil check around the loop unnecessary.

Before:

	if s != nil {
	    for _, x := range s {
	        ...
	    }
	}

After:

	for _, x := range s {
	    ...
	}

Available since

	2017.1


Default: on.

Package documentation: [S1031](https://staticcheck.dev/docs/checks/#S1031)

<a id='S1032'></a>
## `S1032`: Use sort.Ints(x), sort.Float64s(x), and sort.Strings(x)

The sort.Ints, sort.Float64s and sort.Strings functions are easier to read than sort.Sort(sort.IntSlice(x)), sort.Sort(sort.Float64Slice(x)) and sort.Sort(sort.StringSlice(x)).

Before:

	sort.Sort(sort.StringSlice(x))

After:

	sort.Strings(x)

Available since

	2019.1


Default: on.

Package documentation: [S1032](https://staticcheck.dev/docs/checks/#S1032)

<a id='S1033'></a>
## `S1033`: Unnecessary guard around call to 'delete'

Calling delete on a nil map is a no-op.

Available since

	2019.2


Default: on.

Package documentation: [S1033](https://staticcheck.dev/docs/checks/#S1033)

<a id='S1034'></a>
## `S1034`: Use result of type assertion to simplify cases

Available since

	2019.2


Default: on.

Package documentation: [S1034](https://staticcheck.dev/docs/checks/#S1034)

<a id='S1035'></a>
## `S1035`: Redundant call to net/http.CanonicalHeaderKey in method call on net/http.Header

The methods on net/http.Header, namely Add, Del, Get and Set, already canonicalize the given header name.

Available since

	2020.1


Default: on.

Package documentation: [S1035](https://staticcheck.dev/docs/checks/#S1035)

<a id='S1036'></a>
## `S1036`: Unnecessary guard around map access

When accessing a map key that doesn't exist yet, one receives a zero value. Often, the zero value is a suitable value, for example when using append or doing integer math.

The following

	if _, ok := m["foo"]; ok {
	    m["foo"] = append(m["foo"], "bar")
	} else {
	    m["foo"] = []string{"bar"}
	}

can be simplified to

	m["foo"] = append(m["foo"], "bar")

and

	if _, ok := m2["k"]; ok {
	    m2["k"] += 4
	} else {
	    m2["k"] = 4
	}

can be simplified to

	m["k"] += 4

Available since

	2020.1


Default: on.

Package documentation: [S1036](https://staticcheck.dev/docs/checks/#S1036)

<a id='S1037'></a>
## `S1037`: Elaborate way of sleeping

Using a select statement with a single case receiving from the result of time.After is a very elaborate way of sleeping that can much simpler be expressed with a simple call to time.Sleep.

Available since

	2020.1


Default: on.

Package documentation: [S1037](https://staticcheck.dev/docs/checks/#S1037)

<a id='S1038'></a>
## `S1038`: Unnecessarily complex way of printing formatted string

Instead of using fmt.Print(fmt.Sprintf(...)), one can use fmt.Printf(...).

Available since

	2020.1


Default: on.

Package documentation: [S1038](https://staticcheck.dev/docs/checks/#S1038)

<a id='S1039'></a>
## `S1039`: Unnecessary use of fmt.Sprint

Calling fmt.Sprint with a single string argument is unnecessary and identical to using the string directly.

Available since

	2020.1


Default: on.

Package documentation: [S1039](https://staticcheck.dev/docs/checks/#S1039)

<a id='S1040'></a>
## `S1040`: Type assertion to current type

The type assertion x.(SomeInterface), when x already has type SomeInterface, can only fail if x is nil. Usually, this is left-over code from when x had a different type and you can safely delete the type assertion. If you want to check that x is not nil, consider being explicit and using an actual if x == nil comparison instead of relying on the type assertion panicking.

Available since

	2021.1


Default: on.

Package documentation: [S1040](https://staticcheck.dev/docs/checks/#S1040)

<a id='SA1000'></a>
## `SA1000`: Invalid regular expression

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA1000": true}`.

Package documentation: [SA1000](https://staticcheck.dev/docs/checks/#SA1000)

<a id='SA1001'></a>
## `SA1001`: Invalid template

Available since

	2017.1


Default: on.

Package documentation: [SA1001](https://staticcheck.dev/docs/checks/#SA1001)

<a id='SA1002'></a>
## `SA1002`: Invalid format in time.Parse

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA1002": true}`.

Package documentation: [SA1002](https://staticcheck.dev/docs/checks/#SA1002)

<a id='SA1003'></a>
## `SA1003`: Unsupported argument to functions in encoding/binary

The encoding/binary package can only serialize types with known sizes. This precludes the use of the int and uint types, as their sizes differ on different architectures. Furthermore, it doesn't support serializing maps, channels, strings, or functions.

Before Go 1.8, bool wasn't supported, either.

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA1003": true}`.

Package documentation: [SA1003](https://staticcheck.dev/docs/checks/#SA1003)

<a id='SA1004'></a>
## `SA1004`: Suspiciously small untyped constant in time.Sleep

The time.Sleep function takes a time.Duration as its only argument. Durations are expressed in nanoseconds. Thus, calling time.Sleep(1) will sleep for 1 nanosecond. This is a common source of bugs, as sleep functions in other languages often accept seconds or milliseconds.

The time package provides constants such as time.Second to express large durations. These can be combined with arithmetic to express arbitrary durations, for example 5 \* time.Second for 5 seconds.

If you truly meant to sleep for a tiny amount of time, use n \* time.Nanosecond to signal to Staticcheck that you did mean to sleep for some amount of nanoseconds.

Available since

	2017.1


Default: on.

Package documentation: [SA1004](https://staticcheck.dev/docs/checks/#SA1004)

<a id='SA1005'></a>
## `SA1005`: Invalid first argument to exec.Command

os/exec runs programs directly (using variants of the fork and exec system calls on Unix systems). This shouldn't be confused with running a command in a shell. The shell will allow for features such as input redirection, pipes, and general scripting. The shell is also responsible for splitting the user's input into a program name and its arguments. For example, the equivalent to

	ls / /tmp

would be

	exec.Command("ls", "/", "/tmp")

If you want to run a command in a shell, consider using something like the following – but be aware that not all systems, particularly Windows, will have a /bin/sh program:

	exec.Command("/bin/sh", "-c", "ls | grep Awesome")

Available since

	2017.1


Default: on.

Package documentation: [SA1005](https://staticcheck.dev/docs/checks/#SA1005)

<a id='SA1007'></a>
## `SA1007`: Invalid URL in net/url.Parse

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA1007": true}`.

Package documentation: [SA1007](https://staticcheck.dev/docs/checks/#SA1007)

<a id='SA1008'></a>
## `SA1008`: Non-canonical key in http.Header map

Keys in http.Header maps are canonical, meaning they follow a specific combination of uppercase and lowercase letters. Methods such as http.Header.Add and http.Header.Del convert inputs into this canonical form before manipulating the map.

When manipulating http.Header maps directly, as opposed to using the provided methods, care should be taken to stick to canonical form in order to avoid inconsistencies. The following piece of code demonstrates one such inconsistency:

	h := http.Header{}
	h["etag"] = []string{"1234"}
	h.Add("etag", "5678")
	fmt.Println(h)

	// Output:
	// map[Etag:[5678] etag:[1234]]

The easiest way of obtaining the canonical form of a key is to use http.CanonicalHeaderKey.

Available since

	2017.1


Default: on.

Package documentation: [SA1008](https://staticcheck.dev/docs/checks/#SA1008)

<a id='SA1010'></a>
## `SA1010`: (*regexp.Regexp).FindAll called with n == 0, which will always return zero results

If n >= 0, the function returns at most n matches/submatches. To return all results, specify a negative number.

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA1010": true}`.

Package documentation: [SA1010](https://staticcheck.dev/docs/checks/#SA1010)

<a id='SA1011'></a>
## `SA1011`: Various methods in the 'strings' package expect valid UTF-8, but invalid input is provided

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA1011": true}`.

Package documentation: [SA1011](https://staticcheck.dev/docs/checks/#SA1011)

<a id='SA1012'></a>
## `SA1012`: A nil context.Context is being passed to a function, consider using context.TODO instead

Available since

	2017.1


Default: on.

Package documentation: [SA1012](https://staticcheck.dev/docs/checks/#SA1012)

<a id='SA1013'></a>
## `SA1013`: io.Seeker.Seek is being called with the whence constant as the first argument, but it should be the second

Available since

	2017.1


Default: on.

Package documentation: [SA1013](https://staticcheck.dev/docs/checks/#SA1013)

<a id='SA1014'></a>
## `SA1014`: Non-pointer value passed to Unmarshal or Decode

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA1014": true}`.

Package documentation: [SA1014](https://staticcheck.dev/docs/checks/#SA1014)

<a id='SA1015'></a>
## `SA1015`: Using time.Tick in a way that will leak. Consider using time.NewTicker, and only use time.Tick in tests, commands and endless functions

Before Go 1.23, time.Tickers had to be closed to be able to be garbage collected. Since time.Tick doesn't make it possible to close the underlying ticker, using it repeatedly would leak memory.

Go 1.23 fixes this by allowing tickers to be collected even if they weren't closed.

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA1015": true}`.

Package documentation: [SA1015](https://staticcheck.dev/docs/checks/#SA1015)

<a id='SA1016'></a>
## `SA1016`: Trapping a signal that cannot be trapped

Not all signals can be intercepted by a process. Specifically, on UNIX-like systems, the syscall.SIGKILL and syscall.SIGSTOP signals are never passed to the process, but instead handled directly by the kernel. It is therefore pointless to try and handle these signals.

Available since

	2017.1


Default: on.

Package documentation: [SA1016](https://staticcheck.dev/docs/checks/#SA1016)

<a id='SA1017'></a>
## `SA1017`: Channels used with os/signal.Notify should be buffered

The os/signal package uses non-blocking channel sends when delivering signals. If the receiving end of the channel isn't ready and the channel is either unbuffered or full, the signal will be dropped. To avoid missing signals, the channel should be buffered and of the appropriate size. For a channel used for notification of just one signal value, a buffer of size 1 is sufficient.

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA1017": true}`.

Package documentation: [SA1017](https://staticcheck.dev/docs/checks/#SA1017)

<a id='SA1018'></a>
## `SA1018`: strings.Replace called with n == 0, which does nothing

With n == 0, zero instances will be replaced. To replace all instances, use a negative number, or use strings.ReplaceAll.

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA1018": true}`.

Package documentation: [SA1018](https://staticcheck.dev/docs/checks/#SA1018)

<a id='SA1020'></a>
## `SA1020`: Using an invalid host:port pair with a net.Listen-related function

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA1020": true}`.

Package documentation: [SA1020](https://staticcheck.dev/docs/checks/#SA1020)

<a id='SA1021'></a>
## `SA1021`: Using bytes.Equal to compare two net.IP

A net.IP stores an IPv4 or IPv6 address as a slice of bytes. The length of the slice for an IPv4 address, however, can be either 4 or 16 bytes long, using different ways of representing IPv4 addresses. In order to correctly compare two net.IPs, the net.IP.Equal method should be used, as it takes both representations into account.

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA1021": true}`.

Package documentation: [SA1021](https://staticcheck.dev/docs/checks/#SA1021)

<a id='SA1023'></a>
## `SA1023`: Modifying the buffer in an io.Writer implementation

Write must not modify the slice data, even temporarily.

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA1023": true}`.

Package documentation: [SA1023](https://staticcheck.dev/docs/checks/#SA1023)

<a id='SA1024'></a>
## `SA1024`: A string cutset contains duplicate characters

The strings.TrimLeft and strings.TrimRight functions take cutsets, not prefixes. A cutset is treated as a set of characters to remove from a string. For example,

	strings.TrimLeft("42133word", "1234")

will result in the string "word" – any characters that are 1, 2, 3 or 4 are cut from the left of the string.

In order to remove one string from another, use strings.TrimPrefix instead.

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA1024": true}`.

Package documentation: [SA1024](https://staticcheck.dev/docs/checks/#SA1024)

<a id='SA1025'></a>
## `SA1025`: It is not possible to use (*time.Timer).Reset's return value correctly

Available since

	2019.1


Default: off. Enable by setting `"analyses": {"SA1025": true}`.

Package documentation: [SA1025](https://staticcheck.dev/docs/checks/#SA1025)

<a id='SA1026'></a>
## `SA1026`: Cannot marshal channels or functions

Available since

	2019.2


Default: off. Enable by setting `"analyses": {"SA1026": true}`.

Package documentation: [SA1026](https://staticcheck.dev/docs/checks/#SA1026)

<a id='SA1027'></a>
## `SA1027`: Atomic access to 64-bit variable must be 64-bit aligned

On ARM, x86-32, and 32-bit MIPS, it is the caller's responsibility to arrange for 64-bit alignment of 64-bit words accessed atomically. The first word in a variable or in an allocated struct, array, or slice can be relied upon to be 64-bit aligned.

You can use the structlayout tool to inspect the alignment of fields in a struct.

Available since

	2019.2


Default: off. Enable by setting `"analyses": {"SA1027": true}`.

Package documentation: [SA1027](https://staticcheck.dev/docs/checks/#SA1027)

<a id='SA1028'></a>
## `SA1028`: sort.Slice can only be used on slices

The first argument of sort.Slice must be a slice.

Available since

	2020.1


Default: off. Enable by setting `"analyses": {"SA1028": true}`.

Package documentation: [SA1028](https://staticcheck.dev/docs/checks/#SA1028)

<a id='SA1029'></a>
## `SA1029`: Inappropriate key in call to context.WithValue

The provided key must be comparable and should not be of type string or any other built-in type to avoid collisions between packages using context. Users of WithValue should define their own types for keys.

To avoid allocating when assigning to an interface{}, context keys often have concrete type struct{}. Alternatively, exported context key variables' static type should be a pointer or interface.

Available since

	2020.1


Default: off. Enable by setting `"analyses": {"SA1029": true}`.

Package documentation: [SA1029](https://staticcheck.dev/docs/checks/#SA1029)

<a id='SA1030'></a>
## `SA1030`: Invalid argument in call to a strconv function

This check validates the format, number base and bit size arguments of the various parsing and formatting functions in strconv.

Available since

	2021.1


Default: off. Enable by setting `"analyses": {"SA1030": true}`.

Package documentation: [SA1030](https://staticcheck.dev/docs/checks/#SA1030)

<a id='SA1031'></a>
## `SA1031`: Overlapping byte slices passed to an encoder

In an encoding function of the form Encode(dst, src), dst and src were found to reference the same memory. This can result in src bytes being overwritten before they are read, when the encoder writes more than one byte per src byte.

Available since

	2024.1


Default: off. Enable by setting `"analyses": {"SA1031": true}`.

Package documentation: [SA1031](https://staticcheck.dev/docs/checks/#SA1031)

<a id='SA1032'></a>
## `SA1032`: Wrong order of arguments to errors.Is

The first argument of the function errors.Is is the error that we have and the second argument is the error we're trying to match against. For example:

	if errors.Is(err, io.EOF) { ... }

This check detects some cases where the two arguments have been swapped. It flags any calls where the first argument is referring to a package-level error variable, such as

	if errors.Is(io.EOF, err) { /* this is wrong */ }

Available since

	2024.1


Default: off. Enable by setting `"analyses": {"SA1032": true}`.

Package documentation: [SA1032](https://staticcheck.dev/docs/checks/#SA1032)

<a id='SA2001'></a>
## `SA2001`: Empty critical section, did you mean to defer the unlock?

Empty critical sections of the kind

	mu.Lock()
	mu.Unlock()

are very often a typo, and the following was intended instead:

	mu.Lock()
	defer mu.Unlock()

Do note that sometimes empty critical sections can be useful, as a form of signaling to wait on another goroutine. Many times, there are simpler ways of achieving the same effect. When that isn't the case, the code should be amply commented to avoid confusion. Combining such comments with a //lint:ignore directive can be used to suppress this rare false positive.

Available since

	2017.1


Default: on.

Package documentation: [SA2001](https://staticcheck.dev/docs/checks/#SA2001)

<a id='SA2002'></a>
## `SA2002`: Called testing.T.FailNow or SkipNow in a goroutine, which isn't allowed

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA2002": true}`.

Package documentation: [SA2002](https://staticcheck.dev/docs/checks/#SA2002)

<a id='SA2003'></a>
## `SA2003`: Deferred Lock right after locking, likely meant to defer Unlock instead

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA2003": true}`.

Package documentation: [SA2003](https://staticcheck.dev/docs/checks/#SA2003)

<a id='SA3000'></a>
## `SA3000`: TestMain doesn't call os.Exit, hiding test failures

Test executables (and in turn 'go test') exit with a non-zero status code if any tests failed. When specifying your own TestMain function, it is your responsibility to arrange for this, by calling os.Exit with the correct code. The correct code is returned by (\*testing.M).Run, so the usual way of implementing TestMain is to end it with os.Exit(m.Run()).

Available since

	2017.1


Default: on.

Package documentation: [SA3000](https://staticcheck.dev/docs/checks/#SA3000)

<a id='SA3001'></a>
## `SA3001`: Assigning to b.N in benchmarks distorts the results

The testing package dynamically sets b.N to improve the reliability of benchmarks and uses it in computations to determine the duration of a single operation. Benchmark code must not alter b.N as this would falsify results.

Available since

	2017.1


Default: on.

Package documentation: [SA3001](https://staticcheck.dev/docs/checks/#SA3001)

<a id='SA4000'></a>
## `SA4000`: Binary operator has identical expressions on both sides

Available since

	2017.1


Default: on.

Package documentation: [SA4000](https://staticcheck.dev/docs/checks/#SA4000)

<a id='SA4001'></a>
## `SA4001`: &*x gets simplified to x, it does not copy x

Available since

	2017.1


Default: on.

Package documentation: [SA4001](https://staticcheck.dev/docs/checks/#SA4001)

<a id='SA4003'></a>
## `SA4003`: Comparing unsigned values against negative values is pointless

Available since

	2017.1


Default: on.

Package documentation: [SA4003](https://staticcheck.dev/docs/checks/#SA4003)

<a id='SA4004'></a>
## `SA4004`: The loop exits unconditionally after one iteration

Available since

	2017.1


Default: on.

Package documentation: [SA4004](https://staticcheck.dev/docs/checks/#SA4004)

<a id='SA4005'></a>
## `SA4005`: Field assignment that will never be observed. Did you mean to use a pointer receiver?

Available since

	2021.1


Default: off. Enable by setting `"analyses": {"SA4005": true}`.

Package documentation: [SA4005](https://staticcheck.dev/docs/checks/#SA4005)

<a id='SA4006'></a>
## `SA4006`: A value assigned to a variable is never read before being overwritten. Forgotten error check or dead code?

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA4006": true}`.

Package documentation: [SA4006](https://staticcheck.dev/docs/checks/#SA4006)

<a id='SA4008'></a>
## `SA4008`: The variable in the loop condition never changes, are you incrementing the wrong variable?

For example:

	for i := 0; i < 10; j++ { ... }

This may also occur when a loop can only execute once because of unconditional control flow that terminates the loop. For example, when a loop body contains an unconditional break, return, or panic:

	func f() {
		panic("oops")
	}
	func g() {
		for i := 0; i < 10; i++ {
			// f unconditionally calls panic, which means "i" is
			// never incremented.
			f()
		}
	}

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA4008": true}`.

Package documentation: [SA4008](https://staticcheck.dev/docs/checks/#SA4008)

<a id='SA4009'></a>
## `SA4009`: A function argument is overwritten before its first use

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA4009": true}`.

Package documentation: [SA4009](https://staticcheck.dev/docs/checks/#SA4009)

<a id='SA4010'></a>
## `SA4010`: The result of append will never be observed anywhere

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA4010": true}`.

Package documentation: [SA4010](https://staticcheck.dev/docs/checks/#SA4010)

<a id='SA4011'></a>
## `SA4011`: Break statement with no effect. Did you mean to break out of an outer loop?

Available since

	2017.1


Default: on.

Package documentation: [SA4011](https://staticcheck.dev/docs/checks/#SA4011)

<a id='SA4012'></a>
## `SA4012`: Comparing a value against NaN even though no value is equal to NaN

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA4012": true}`.

Package documentation: [SA4012](https://staticcheck.dev/docs/checks/#SA4012)

<a id='SA4013'></a>
## `SA4013`: Negating a boolean twice (!!b) is the same as writing b. This is either redundant, or a typo.

Available since

	2017.1


Default: on.

Package documentation: [SA4013](https://staticcheck.dev/docs/checks/#SA4013)

<a id='SA4014'></a>
## `SA4014`: An if/else if chain has repeated conditions and no side-effects; if the condition didn't match the first time, it won't match the second time, either

Available since

	2017.1


Default: on.

Package documentation: [SA4014](https://staticcheck.dev/docs/checks/#SA4014)

<a id='SA4015'></a>
## `SA4015`: Calling functions like math.Ceil on floats converted from integers doesn't do anything useful

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA4015": true}`.

Package documentation: [SA4015](https://staticcheck.dev/docs/checks/#SA4015)

<a id='SA4016'></a>
## `SA4016`: Certain bitwise operations, such as x ^ 0, do not do anything useful

Available since

	2017.1


Default: on.

Package documentation: [SA4016](https://staticcheck.dev/docs/checks/#SA4016)

<a id='SA4017'></a>
## `SA4017`: Discarding the return values of a function without side effects, making the call pointless

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA4017": true}`.

Package documentation: [SA4017](https://staticcheck.dev/docs/checks/#SA4017)

<a id='SA4018'></a>
## `SA4018`: Self-assignment of variables

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA4018": true}`.

Package documentation: [SA4018](https://staticcheck.dev/docs/checks/#SA4018)

<a id='SA4019'></a>
## `SA4019`: Multiple, identical build constraints in the same file

Available since

	2017.1


Default: on.

Package documentation: [SA4019](https://staticcheck.dev/docs/checks/#SA4019)

<a id='SA4020'></a>
## `SA4020`: Unreachable case clause in a type switch

In a type switch like the following

	type T struct{}
	func (T) Read(b []byte) (int, error) { return 0, nil }

	var v any = T{}

	switch v.(type) {
	case io.Reader:
	    // ...
	case T:
	    // unreachable
	}

the second case clause can never be reached because T implements io.Reader and case clauses are evaluated in source order.

Another example:

	type T struct{}
	func (T) Read(b []byte) (int, error) { return 0, nil }
	func (T) Close() error { return nil }

	var v any = T{}

	switch v.(type) {
	case io.Reader:
	    // ...
	case io.ReadCloser:
	    // unreachable
	}

Even though T has a Close method and thus implements io.ReadCloser, io.Reader will always match first. The method set of io.Reader is a subset of io.ReadCloser. Thus it is impossible to match the second case without matching the first case.

### Structurally equivalent interfaces {#hdr-Structurally_equivalent_interfaces}

A special case of the previous example are structurally identical interfaces. Given these declarations

	type T error
	type V error

	func doSomething() error {
	    err, ok := doAnotherThing()
	    if ok {
	        return T(err)
	    }

	    return U(err)
	}

the following type switch will have an unreachable case clause:

	switch doSomething().(type) {
	case T:
	    // ...
	case V:
	    // unreachable
	}

T will always match before V because they are structurally equivalent and therefore doSomething()'s return value implements both.

Available since

	2019.2


Default: on.

Package documentation: [SA4020](https://staticcheck.dev/docs/checks/#SA4020)

<a id='SA4022'></a>
## `SA4022`: Comparing the address of a variable against nil

Code such as 'if &x == nil' is meaningless, because taking the address of a variable always yields a non-nil pointer.

Available since

	2020.1


Default: on.

Package documentation: [SA4022](https://staticcheck.dev/docs/checks/#SA4022)

<a id='SA4023'></a>
## `SA4023`: Impossible comparison of interface value with untyped nil

Under the covers, interfaces are implemented as two elements, a type T and a value V. V is a concrete value such as an int, struct or pointer, never an interface itself, and has type T. For instance, if we store the int value 3 in an interface, the resulting interface value has, schematically, (T=int, V=3). The value V is also known as the interface's dynamic value, since a given interface variable might hold different values V (and corresponding types T) during the execution of the program.

An interface value is nil only if the V and T are both unset, (T=nil, V is not set), In particular, a nil interface will always hold a nil type. If we store a nil pointer of type \*int inside an interface value, the inner type will be \*int regardless of the value of the pointer: (T=\*int, V=nil). Such an interface value will therefore be non-nil even when the pointer value V inside is nil.

This situation can be confusing, and arises when a nil value is stored inside an interface value such as an error return:

	func returnsError() error {
	    var p *MyError = nil
	    if bad() {
	        p = ErrBad
	    }
	    return p // Will always return a non-nil error.
	}

If all goes well, the function returns a nil p, so the return value is an error interface value holding (T=\*MyError, V=nil). This means that if the caller compares the returned error to nil, it will always look as if there was an error even if nothing bad happened. To return a proper nil error to the caller, the function must return an explicit nil:

	func returnsError() error {
	    if bad() {
	        return ErrBad
	    }
	    return nil
	}

It's a good idea for functions that return errors always to use the error type in their signature (as we did above) rather than a concrete type such as \*MyError, to help guarantee the error is created correctly. As an example, os.Open returns an error even though, if not nil, it's always of concrete type \*os.PathError.

Similar situations to those described here can arise whenever interfaces are used. Just keep in mind that if any concrete value has been stored in the interface, the interface will not be nil. For more information, see The Laws of Reflection at [https://golang.org/doc/articles/laws\_of\_reflection.html](https://golang.org/doc/articles/laws_of_reflection.html).

This text has been copied from [https://golang.org/doc/faq#nil\_error](https://golang.org/doc/faq#nil_error), licensed under the Creative Commons Attribution 3.0 License.

Available since

	2020.2


Default: off. Enable by setting `"analyses": {"SA4023": true}`.

Package documentation: [SA4023](https://staticcheck.dev/docs/checks/#SA4023)

<a id='SA4024'></a>
## `SA4024`: Checking for impossible return value from a builtin function

Return values of the len and cap builtins cannot be negative.

See [https://golang.org/pkg/builtin/#len](https://golang.org/pkg/builtin/#len) and [https://golang.org/pkg/builtin/#cap](https://golang.org/pkg/builtin/#cap).

Example:

	if len(slice) < 0 {
	    fmt.Println("unreachable code")
	}

Available since

	2021.1


Default: on.

Package documentation: [SA4024](https://staticcheck.dev/docs/checks/#SA4024)

<a id='SA4025'></a>
## `SA4025`: Integer division of literals that results in zero

When dividing two integer constants, the result will also be an integer. Thus, a division such as 2 / 3 results in 0. This is true for all of the following examples:

	_ = 2 / 3
	const _ = 2 / 3
	const _ float64 = 2 / 3
	_ = float64(2 / 3)

Staticcheck will flag such divisions if both sides of the division are integer literals, as it is highly unlikely that the division was intended to truncate to zero. Staticcheck will not flag integer division involving named constants, to avoid noisy positives.

Available since

	2021.1


Default: on.

Package documentation: [SA4025](https://staticcheck.dev/docs/checks/#SA4025)

<a id='SA4026'></a>
## `SA4026`: Go constants cannot express negative zero

In IEEE 754 floating point math, zero has a sign and can be positive or negative. This can be useful in certain numerical code.

Go constants, however, cannot express negative zero. This means that the literals -0.0 and 0.0 have the same ideal value (zero) and will both represent positive zero at runtime.

To explicitly and reliably create a negative zero, you can use the math.Copysign function: math.Copysign(0, -1).

Available since

	2021.1


Default: on.

Package documentation: [SA4026](https://staticcheck.dev/docs/checks/#SA4026)

<a id='SA4027'></a>
## `SA4027`: (*net/url.URL).Query returns a copy, modifying it doesn't change the URL

(\*net/url.URL).Query parses the current value of net/url.URL.RawQuery and returns it as a map of type net/url.Values. Subsequent changes to this map will not affect the URL unless the map gets encoded and assigned to the URL's RawQuery.

As a consequence, the following code pattern is an expensive no-op: u.Query().Add(key, value).

Available since

	2021.1


Default: on.

Package documentation: [SA4027](https://staticcheck.dev/docs/checks/#SA4027)

<a id='SA4028'></a>
## `SA4028`: x % 1 is always zero

Available since

	2022.1


Default: on.

Package documentation: [SA4028](https://staticcheck.dev/docs/checks/#SA4028)

<a id='SA4029'></a>
## `SA4029`: Ineffective attempt at sorting slice

sort.Float64Slice, sort.IntSlice, and sort.StringSlice are types, not functions. Doing x = sort.StringSlice(x) does nothing, especially not sort any values. The correct usage is sort.Sort(sort.StringSlice(x)) or sort.StringSlice(x).Sort(), but there are more convenient helpers, namely sort.Float64s, sort.Ints, and sort.Strings.

Available since

	2022.1


Default: on.

Package documentation: [SA4029](https://staticcheck.dev/docs/checks/#SA4029)

<a id='SA4030'></a>
## `SA4030`: Ineffective attempt at generating random number

Functions in the math/rand package that accept upper limits, such as Intn, generate random numbers in the half-open interval \[0,n). In other words, the generated numbers will be >= 0 and \< n – they don't include n. rand.Intn(1) therefore doesn't generate 0 or 1, it always generates 0.

Available since

	2022.1


Default: on.

Package documentation: [SA4030](https://staticcheck.dev/docs/checks/#SA4030)

<a id='SA4031'></a>
## `SA4031`: Checking never-nil value against nil

Available since

	2022.1


Default: off. Enable by setting `"analyses": {"SA4031": true}`.

Package documentation: [SA4031](https://staticcheck.dev/docs/checks/#SA4031)

<a id='SA4032'></a>
## `SA4032`: Comparing runtime.GOOS or runtime.GOARCH against impossible value

Available since

	2024.1


Default: on.

Package documentation: [SA4032](https://staticcheck.dev/docs/checks/#SA4032)

<a id='SA5000'></a>
## `SA5000`: Assignment to nil map

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA5000": true}`.

Package documentation: [SA5000](https://staticcheck.dev/docs/checks/#SA5000)

<a id='SA5001'></a>
## `SA5001`: Deferring Close before checking for a possible error

Available since

	2017.1


Default: on.

Package documentation: [SA5001](https://staticcheck.dev/docs/checks/#SA5001)

<a id='SA5002'></a>
## `SA5002`: The empty for loop ('for {}') spins and can block the scheduler

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA5002": true}`.

Package documentation: [SA5002](https://staticcheck.dev/docs/checks/#SA5002)

<a id='SA5003'></a>
## `SA5003`: Defers in infinite loops will never execute

Defers are scoped to the surrounding function, not the surrounding block. In a function that never returns, i.e. one containing an infinite loop, defers will never execute.

Available since

	2017.1


Default: on.

Package documentation: [SA5003](https://staticcheck.dev/docs/checks/#SA5003)

<a id='SA5004'></a>
## `SA5004`: 'for { select { ...' with an empty default branch spins

Available since

	2017.1


Default: on.

Package documentation: [SA5004](https://staticcheck.dev/docs/checks/#SA5004)

<a id='SA5005'></a>
## `SA5005`: The finalizer references the finalized object, preventing garbage collection

A finalizer is a function associated with an object that runs when the garbage collector is ready to collect said object, that is when the object is no longer referenced by anything.

If the finalizer references the object, however, it will always remain as the final reference to that object, preventing the garbage collector from collecting the object. The finalizer will never run, and the object will never be collected, leading to a memory leak. That is why the finalizer should instead use its first argument to operate on the object. That way, the number of references can temporarily go to zero before the object is being passed to the finalizer.

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA5005": true}`.

Package documentation: [SA5005](https://staticcheck.dev/docs/checks/#SA5005)

<a id='SA5007'></a>
## `SA5007`: Infinite recursive call

A function that calls itself recursively needs to have an exit condition. Otherwise it will recurse forever, until the system runs out of memory.

This issue can be caused by simple bugs such as forgetting to add an exit condition. It can also happen "on purpose". Some languages have tail call optimization which makes certain infinite recursive calls safe to use. Go, however, does not implement TCO, and as such a loop should be used instead.

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA5007": true}`.

Package documentation: [SA5007](https://staticcheck.dev/docs/checks/#SA5007)

<a id='SA5008'></a>
## `SA5008`: Invalid struct tag

Available since

	2019.2


Default: on.

Package documentation: [SA5008](https://staticcheck.dev/docs/checks/#SA5008)

<a id='SA5010'></a>
## `SA5010`: Impossible type assertion

Some type assertions can be statically proven to be impossible. This is the case when the method sets of both arguments of the type assertion conflict with each other, for example by containing the same method with different signatures.

The Go compiler already applies this check when asserting from an interface value to a concrete type. If the concrete type misses methods from the interface, or if function signatures don't match, then the type assertion can never succeed.

This check applies the same logic when asserting from one interface to another. If both interface types contain the same method but with different signatures, then the type assertion can never succeed, either.

Available since

	2020.1


Default: off. Enable by setting `"analyses": {"SA5010": true}`.

Package documentation: [SA5010](https://staticcheck.dev/docs/checks/#SA5010)

<a id='SA5011'></a>
## `SA5011`: Possible nil pointer dereference

A pointer is being dereferenced unconditionally, while also being checked against nil in another place. This suggests that the pointer may be nil and dereferencing it may panic. This is commonly a result of improperly ordered code or missing return statements. Consider the following examples:

	func fn(x *int) {
	    fmt.Println(*x)

	    // This nil check is equally important for the previous dereference
	    if x != nil {
	        foo(*x)
	    }
	}

	func TestFoo(t *testing.T) {
	    x := compute()
	    if x == nil {
	        t.Errorf("nil pointer received")
	    }

	    // t.Errorf does not abort the test, so if x is nil, the next line will panic.
	    foo(*x)
	}

Staticcheck tries to deduce which functions abort control flow. For example, it is aware that a function will not continue execution after a call to panic or log.Fatal. However, sometimes this detection fails, in particular in the presence of conditionals. Consider the following example:

	func Log(msg string, level int) {
	    fmt.Println(msg)
	    if level == levelFatal {
	        os.Exit(1)
	    }
	}

	func Fatal(msg string) {
	    Log(msg, levelFatal)
	}

	func fn(x *int) {
	    if x == nil {
	        Fatal("unexpected nil pointer")
	    }
	    fmt.Println(*x)
	}

Staticcheck will flag the dereference of x, even though it is perfectly safe. Staticcheck is not able to deduce that a call to Fatal will exit the program. For the time being, the easiest workaround is to modify the definition of Fatal like so:

	func Fatal(msg string) {
	    Log(msg, levelFatal)
	    panic("unreachable")
	}

We also hard-code functions from common logging packages such as logrus. Please file an issue if we're missing support for a popular package.

Available since

	2020.1


Default: off. Enable by setting `"analyses": {"SA5011": true}`.

Package documentation: [SA5011](https://staticcheck.dev/docs/checks/#SA5011)

<a id='SA5012'></a>
## `SA5012`: Passing odd-sized slice to function expecting even size

Some functions that take slices as parameters expect the slices to have an even number of elements.  Often, these functions treat elements in a slice as pairs.  For example, strings.NewReplacer takes pairs of old and new strings,  and calling it with an odd number of elements would be an error.

Available since

	2020.2


Default: off. Enable by setting `"analyses": {"SA5012": true}`.

Package documentation: [SA5012](https://staticcheck.dev/docs/checks/#SA5012)

<a id='SA6000'></a>
## `SA6000`: Using regexp.Match or related in a loop, should use regexp.Compile

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA6000": true}`.

Package documentation: [SA6000](https://staticcheck.dev/docs/checks/#SA6000)

<a id='SA6001'></a>
## `SA6001`: Missing an optimization opportunity when indexing maps by byte slices

Map keys must be comparable, which precludes the use of byte slices. This usually leads to using string keys and converting byte slices to strings.

Normally, a conversion of a byte slice to a string needs to copy the data and causes allocations. The compiler, however, recognizes m\[string(b)] and uses the data of b directly, without copying it, because it knows that the data can't change during the map lookup. This leads to the counter-intuitive situation that

	k := string(b)
	println(m[k])
	println(m[k])

will be less efficient than

	println(m[string(b)])
	println(m[string(b)])

because the first version needs to copy and allocate, while the second one does not.

For some history on this optimization, check out commit f5f5a8b6209f84961687d993b93ea0d397f5d5bf in the Go repository.

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA6001": true}`.

Package documentation: [SA6001](https://staticcheck.dev/docs/checks/#SA6001)

<a id='SA6002'></a>
## `SA6002`: Storing non-pointer values in sync.Pool allocates memory

A sync.Pool is used to avoid unnecessary allocations and reduce the amount of work the garbage collector has to do.

When passing a value that is not a pointer to a function that accepts an interface, the value needs to be placed on the heap, which means an additional allocation. Slices are a common thing to put in sync.Pools, and they're structs with 3 fields (length, capacity, and a pointer to an array). In order to avoid the extra allocation, one should store a pointer to the slice instead.

See the comments on [https://go-review.googlesource.com/c/go/+/24371](https://go-review.googlesource.com/c/go/+/24371) that discuss this problem.

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA6002": true}`.

Package documentation: [SA6002](https://staticcheck.dev/docs/checks/#SA6002)

<a id='SA6003'></a>
## `SA6003`: Converting a string to a slice of runes before ranging over it

You may want to loop over the runes in a string. Instead of converting the string to a slice of runes and looping over that, you can loop over the string itself. That is,

	for _, r := range s {}

and

	for _, r := range []rune(s) {}

will yield the same values. The first version, however, will be faster and avoid unnecessary memory allocations.

Do note that if you are interested in the indices, ranging over a string and over a slice of runes will yield different indices. The first one yields byte offsets, while the second one yields indices in the slice of runes.

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA6003": true}`.

Package documentation: [SA6003](https://staticcheck.dev/docs/checks/#SA6003)

<a id='SA6005'></a>
## `SA6005`: Inefficient string comparison with strings.ToLower or strings.ToUpper

Converting two strings to the same case and comparing them like so

	if strings.ToLower(s1) == strings.ToLower(s2) {
	    ...
	}

is significantly more expensive than comparing them with strings.EqualFold(s1, s2). This is due to memory usage as well as computational complexity.

strings.ToLower will have to allocate memory for the new strings, as well as convert both strings fully, even if they differ on the very first byte. strings.EqualFold, on the other hand, compares the strings one character at a time. It doesn't need to create two intermediate strings and can return as soon as the first non-matching character has been found.

For a more in-depth explanation of this issue, see [https://blog.digitalocean.com/how-to-efficiently-compare-strings-in-go/](https://blog.digitalocean.com/how-to-efficiently-compare-strings-in-go/)

Available since

	2019.2


Default: on.

Package documentation: [SA6005](https://staticcheck.dev/docs/checks/#SA6005)

<a id='SA6006'></a>
## `SA6006`: Using io.WriteString to write []byte

Using io.WriteString to write a slice of bytes, as in

	io.WriteString(w, string(b))

is both unnecessary and inefficient. Converting from \[]byte to string has to allocate and copy the data, and we could simply use w.Write(b) instead.

Available since

	2024.1


Default: on.

Package documentation: [SA6006](https://staticcheck.dev/docs/checks/#SA6006)

<a id='SA9001'></a>
## `SA9001`: Defers in range loops may not run when you expect them to

Available since

	2017.1


Default: off. Enable by setting `"analyses": {"SA9001": true}`.

Package documentation: [SA9001](https://staticcheck.dev/docs/checks/#SA9001)

<a id='SA9002'></a>
## `SA9002`: Using a non-octal os.FileMode that looks like it was meant to be in octal.

Available since

	2017.1


Default: on.

Package documentation: [SA9002](https://staticcheck.dev/docs/checks/#SA9002)

<a id='SA9003'></a>
## `SA9003`: Empty body in an if or else branch

Available since

	2017.1, non-default


Default: off. Enable by setting `"analyses": {"SA9003": true}`.

Package documentation: [SA9003](https://staticcheck.dev/docs/checks/#SA9003)

<a id='SA9004'></a>
## `SA9004`: Only the first constant has an explicit type

In a constant declaration such as the following:

	const (
	    First byte = 1
	    Second     = 2
	)

the constant Second does not have the same type as the constant First. This construct shouldn't be confused with

	const (
	    First byte = iota
	    Second
	)

where First and Second do indeed have the same type. The type is only passed on when no explicit value is assigned to the constant.

When declaring enumerations with explicit values it is therefore important not to write

	const (
	      EnumFirst EnumType = 1
	      EnumSecond         = 2
	      EnumThird          = 3
	)

This discrepancy in types can cause various confusing behaviors and bugs.

### Wrong type in variable declarations {#hdr-Wrong_type_in_variable_declarations}

The most obvious issue with such incorrect enumerations expresses itself as a compile error:

	package pkg

	const (
	    EnumFirst  uint8 = 1
	    EnumSecond       = 2
	)

	func fn(useFirst bool) {
	    x := EnumSecond
	    if useFirst {
	        x = EnumFirst
	    }
	}

fails to compile with

	./const.go:11:5: cannot use EnumFirst (type uint8) as type int in assignment

### Losing method sets {#hdr-Losing_method_sets}

A more subtle issue occurs with types that have methods and optional interfaces. Consider the following:

	package main

	import "fmt"

	type Enum int

	func (e Enum) String() string {
	    return "an enum"
	}

	const (
	    EnumFirst  Enum = 1
	    EnumSecond      = 2
	)

	func main() {
	    fmt.Println(EnumFirst)
	    fmt.Println(EnumSecond)
	}

This code will output

	an enum
	2

as EnumSecond has no explicit type, and thus defaults to int.

Available since

	2019.1


Default: on.

Package documentation: [SA9004](https://staticcheck.dev/docs/checks/#SA9004)

<a id='SA9005'></a>
## `SA9005`: Trying to marshal a struct with no public fields nor custom marshaling

The encoding/json and encoding/xml packages only operate on exported fields in structs, not unexported ones. It is usually an error to try to (un)marshal structs that only consist of unexported fields.

This check will not flag calls involving types that define custom marshaling behavior, e.g. via MarshalJSON methods. It will also not flag empty structs.

Available since

	2019.2


Default: off. Enable by setting `"analyses": {"SA9005": true}`.

Package documentation: [SA9005](https://staticcheck.dev/docs/checks/#SA9005)

<a id='SA9006'></a>
## `SA9006`: Dubious bit shifting of a fixed size integer value

Bit shifting a value past its size will always clear the value.

For instance:

	v := int8(42)
	v >>= 8

will always result in 0.

This check flags bit shifting operations on fixed size integer values only. That is, int, uint and uintptr are never flagged to avoid potential false positives in somewhat exotic but valid bit twiddling tricks:

	// Clear any value above 32 bits if integers are more than 32 bits.
	func f(i int) int {
	    v := i >> 32
	    v = v << 32
	    return i-v
	}

Available since

	2020.2


Default: on.

Package documentation: [SA9006](https://staticcheck.dev/docs/checks/#SA9006)

<a id='SA9007'></a>
## `SA9007`: Deleting a directory that shouldn't be deleted

It is virtually never correct to delete system directories such as /tmp or the user's home directory. However, it can be fairly easy to do by mistake, for example by mistakenly using os.TempDir instead of ioutil.TempDir, or by forgetting to add a suffix to the result of os.UserHomeDir.

Writing

	d := os.TempDir()
	defer os.RemoveAll(d)

in your unit tests will have a devastating effect on the stability of your system.

This check flags attempts at deleting the following directories:

\- os.TempDir - os.UserCacheDir - os.UserConfigDir - os.UserHomeDir

Available since

	2022.1


Default: off. Enable by setting `"analyses": {"SA9007": true}`.

Package documentation: [SA9007](https://staticcheck.dev/docs/checks/#SA9007)

<a id='SA9008'></a>
## `SA9008`: else branch of a type assertion is probably not reading the right value

When declaring variables as part of an if statement (like in 'if foo := ...; foo {'), the same variables will also be in the scope of the else branch. This means that in the following example

	if x, ok := x.(int); ok {
	    // ...
	} else {
	    fmt.Printf("unexpected type %T", x)
	}

x in the else branch will refer to the x from x, ok :=; it will not refer to the x that is being type-asserted. The result of a failed type assertion is the zero value of the type that is being asserted to, so x in the else branch will always have the value 0 and the type int.

Available since

	2022.1


Default: off. Enable by setting `"analyses": {"SA9008": true}`.

Package documentation: [SA9008](https://staticcheck.dev/docs/checks/#SA9008)

<a id='SA9009'></a>
## `SA9009`: Ineffectual Go compiler directive

A potential Go compiler directive was found, but is ineffectual as it begins with whitespace.

Available since

	2024.1


Default: on.

Package documentation: [SA9009](https://staticcheck.dev/docs/checks/#SA9009)

<a id='ST1000'></a>
## `ST1000`: Incorrect or missing package comment

Packages must have a package comment that is formatted according to the guidelines laid out in [https://go.dev/wiki/CodeReviewComments#package-comments](https://go.dev/wiki/CodeReviewComments#package-comments).

Available since

	2019.1, non-default


Default: off. Enable by setting `"analyses": {"ST1000": true}`.

Package documentation: [ST1000](https://staticcheck.dev/docs/checks/#ST1000)

<a id='ST1001'></a>
## `ST1001`: Dot imports are discouraged

Dot imports that aren't in external test packages are discouraged.

The dot\_import\_whitelist option can be used to whitelist certain imports.

Quoting Go Code Review Comments:

> The import . form can be useful in tests that, due to circular > dependencies, cannot be made part of the package being tested: >  >     package foo\_test >  >     import ( >         "bar/testutil" // also imports "foo" >         . "foo" >     ) >  > In this case, the test file cannot be in package foo because it > uses bar/testutil, which imports foo. So we use the import . > form to let the file pretend to be part of package foo even though > it is not. Except for this one case, do not use import . in your > programs. It makes the programs much harder to read because it is > unclear whether a name like Quux is a top-level identifier in the > current package or in an imported package.

Available since

	2019.1

Options

	dot_import_whitelist


Default: off. Enable by setting `"analyses": {"ST1001": true}`.

Package documentation: [ST1001](https://staticcheck.dev/docs/checks/#ST1001)

<a id='ST1003'></a>
## `ST1003`: Poorly chosen identifier

Identifiers, such as variable and package names, follow certain rules.

See the following links for details:

\- [https://go.dev/doc/effective\_go#package-names](https://go.dev/doc/effective_go#package-names) - [https://go.dev/doc/effective\_go#mixed-caps](https://go.dev/doc/effective_go#mixed-caps) - [https://go.dev/wiki/CodeReviewComments#initialisms](https://go.dev/wiki/CodeReviewComments#initialisms) - [https://go.dev/wiki/CodeReviewComments#variable-names](https://go.dev/wiki/CodeReviewComments#variable-names)

Available since

	2019.1, non-default

Options

	initialisms


Default: off. Enable by setting `"analyses": {"ST1003": true}`.

Package documentation: [ST1003](https://staticcheck.dev/docs/checks/#ST1003)

<a id='ST1005'></a>
## `ST1005`: Incorrectly formatted error string

Error strings follow a set of guidelines to ensure uniformity and good composability.

Quoting Go Code Review Comments:

> Error strings should not be capitalized (unless beginning with > proper nouns or acronyms) or end with punctuation, since they are > usually printed following other context. That is, use > fmt.Errorf("something bad") not fmt.Errorf("Something bad"), so > that log.Printf("Reading %s: %v", filename, err) formats without a > spurious capital letter mid-message.

Available since

	2019.1


Default: off. Enable by setting `"analyses": {"ST1005": true}`.

Package documentation: [ST1005](https://staticcheck.dev/docs/checks/#ST1005)

<a id='ST1006'></a>
## `ST1006`: Poorly chosen receiver name

Quoting Go Code Review Comments:

> The name of a method's receiver should be a reflection of its > identity; often a one or two letter abbreviation of its type > suffices (such as "c" or "cl" for "Client"). Don't use generic > names such as "me", "this" or "self", identifiers typical of > object-oriented languages that place more emphasis on methods as > opposed to functions. The name need not be as descriptive as that > of a method argument, as its role is obvious and serves no > documentary purpose. It can be very short as it will appear on > almost every line of every method of the type; familiarity admits > brevity. Be consistent, too: if you call the receiver "c" in one > method, don't call it "cl" in another.

Available since

	2019.1


Default: off. Enable by setting `"analyses": {"ST1006": true}`.

Package documentation: [ST1006](https://staticcheck.dev/docs/checks/#ST1006)

<a id='ST1008'></a>
## `ST1008`: A function's error value should be its last return value

A function's error value should be its last return value.

Available since

	2019.1


Default: off. Enable by setting `"analyses": {"ST1008": true}`.

Package documentation: [ST1008](https://staticcheck.dev/docs/checks/#ST1008)

<a id='ST1011'></a>
## `ST1011`: Poorly chosen name for variable of type time.Duration

time.Duration values represent an amount of time, which is represented as a count of nanoseconds. An expression like 5 \* time.Microsecond yields the value 5000. It is therefore not appropriate to suffix a variable of type time.Duration with any time unit, such as Msec or Milli.

Available since

	2019.1


Default: off. Enable by setting `"analyses": {"ST1011": true}`.

Package documentation: [ST1011](https://staticcheck.dev/docs/checks/#ST1011)

<a id='ST1012'></a>
## `ST1012`: Poorly chosen name for error variable

Error variables that are part of an API should be called errFoo or ErrFoo.

Available since

	2019.1


Default: off. Enable by setting `"analyses": {"ST1012": true}`.

Package documentation: [ST1012](https://staticcheck.dev/docs/checks/#ST1012)

<a id='ST1013'></a>
## `ST1013`: Should use constants for HTTP error codes, not magic numbers

HTTP has a tremendous number of status codes. While some of those are well known (200, 400, 404, 500), most of them are not. The net/http package provides constants for all status codes that are part of the various specifications. It is recommended to use these constants instead of hard-coding magic numbers, to vastly improve the readability of your code.

Available since

	2019.1

Options

	http_status_code_whitelist


Default: off. Enable by setting `"analyses": {"ST1013": true}`.

Package documentation: [ST1013](https://staticcheck.dev/docs/checks/#ST1013)

<a id='ST1015'></a>
## `ST1015`: A switch's default case should be the first or last case

Available since

	2019.1


Default: off. Enable by setting `"analyses": {"ST1015": true}`.

Package documentation: [ST1015](https://staticcheck.dev/docs/checks/#ST1015)

<a id='ST1016'></a>
## `ST1016`: Use consistent method receiver names

Available since

	2019.1, non-default


Default: off. Enable by setting `"analyses": {"ST1016": true}`.

Package documentation: [ST1016](https://staticcheck.dev/docs/checks/#ST1016)

<a id='ST1017'></a>
## `ST1017`: Don't use Yoda conditions

Yoda conditions are conditions of the kind 'if 42 == x', where the literal is on the left side of the comparison. These are a common idiom in languages in which assignment is an expression, to avoid bugs of the kind 'if (x = 42)'. In Go, which doesn't allow for this kind of bug, we prefer the more idiomatic 'if x == 42'.

Available since

	2019.2


Default: off. Enable by setting `"analyses": {"ST1017": true}`.

Package documentation: [ST1017](https://staticcheck.dev/docs/checks/#ST1017)

<a id='ST1018'></a>
## `ST1018`: Avoid zero-width and control characters in string literals

Available since

	2019.2


Default: off. Enable by setting `"analyses": {"ST1018": true}`.

Package documentation: [ST1018](https://staticcheck.dev/docs/checks/#ST1018)

<a id='ST1019'></a>
## `ST1019`: Importing the same package multiple times

Go allows importing the same package multiple times, as long as different import aliases are being used. That is, the following bit of code is valid:

	import (
	    "fmt"
	    fumpt "fmt"
	    format "fmt"
	    _ "fmt"
	)

However, this is very rarely done on purpose. Usually, it is a sign of code that got refactored, accidentally adding duplicate import statements. It is also a rarely known feature, which may contribute to confusion.

Do note that sometimes, this feature may be used intentionally (see for example [https://github.com/golang/go/commit/3409ce39bfd7584523b7a8c150a310cea92d879d](https://github.com/golang/go/commit/3409ce39bfd7584523b7a8c150a310cea92d879d)) – if you want to allow this pattern in your code base, you're advised to disable this check.

Available since

	2020.1


Default: off. Enable by setting `"analyses": {"ST1019": true}`.

Package documentation: [ST1019](https://staticcheck.dev/docs/checks/#ST1019)

<a id='ST1020'></a>
## `ST1020`: The documentation of an exported function should start with the function's name

Doc comments work best as complete sentences, which allow a wide variety of automated presentations. The first sentence should be a one-sentence summary that starts with the name being declared.

If every doc comment begins with the name of the item it describes, you can use the doc subcommand of the go tool and run the output through grep.

See [https://go.dev/doc/effective\_go#commentary](https://go.dev/doc/effective_go#commentary) for more information on how to write good documentation.

Available since

	2020.1, non-default


Default: off. Enable by setting `"analyses": {"ST1020": true}`.

Package documentation: [ST1020](https://staticcheck.dev/docs/checks/#ST1020)

<a id='ST1021'></a>
## `ST1021`: The documentation of an exported type should start with type's name

Doc comments work best as complete sentences, which allow a wide variety of automated presentations. The first sentence should be a one-sentence summary that starts with the name being declared.

If every doc comment begins with the name of the item it describes, you can use the doc subcommand of the go tool and run the output through grep.

See [https://go.dev/doc/effective\_go#commentary](https://go.dev/doc/effective_go#commentary) for more information on how to write good documentation.

Available since

	2020.1, non-default


Default: off. Enable by setting `"analyses": {"ST1021": true}`.

Package documentation: [ST1021](https://staticcheck.dev/docs/checks/#ST1021)

<a id='ST1022'></a>
## `ST1022`: The documentation of an exported variable or constant should start with variable's name

Doc comments work best as complete sentences, which allow a wide variety of automated presentations. The first sentence should be a one-sentence summary that starts with the name being declared.

If every doc comment begins with the name of the item it describes, you can use the doc subcommand of the go tool and run the output through grep.

See [https://go.dev/doc/effective\_go#commentary](https://go.dev/doc/effective_go#commentary) for more information on how to write good documentation.

Available since

	2020.1, non-default


Default: off. Enable by setting `"analyses": {"ST1022": true}`.

Package documentation: [ST1022](https://staticcheck.dev/docs/checks/#ST1022)

<a id='ST1023'></a>
## `ST1023`: Redundant type in variable declaration

Available since

	2021.1, non-default


Default: off. Enable by setting `"analyses": {"ST1023": true}`.

Package documentation: [ST1023](https://staticcheck.dev/docs/checks/#)

<a id='any'></a>
## `any`: replace interface{} with any

The any analyzer suggests replacing uses of the empty interface type, \`interface{}\`, with the \`any\` alias, which was introduced in Go 1.18. This is a purely stylistic change that makes code more readable.


Default: on.

Package documentation: [any](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#any)

<a id='appendclipped'></a>
## `appendclipped`: simplify append chains using slices.Concat

The appendclipped analyzer suggests replacing chains of append calls with a single call to slices.Concat, which was added in Go 1.21. For example, append(append(s, s1...), s2...) would be simplified to slices.Concat(s, s1, s2).

In the simple case of appending to a newly allocated slice, such as append(\[]T(nil), s...), the analyzer suggests the more concise slices.Clone(s). For byte slices, it will prefer bytes.Clone if the "bytes" package is already imported.

This fix is only applied when the base of the append tower is a "clipped" slice, meaning its length and capacity are equal (e.g. x\[:0:0] or \[]T{}). This is to avoid changing program behavior by eliminating intended side effects on the base slice's underlying array.

This analyzer is currently disabled by default as the transformation does not preserve the nilness of the base slice in all cases; see [https://go.dev/issue/73557](https://go.dev/issue/73557).


Default: off. Enable by setting `"analyses": {"appendclipped": true}`.

Package documentation: [appendclipped](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#appendclipped)

<a id='appends'></a>
## `appends`: check for missing values after append

This checker reports calls to append that pass no values to be appended to the slice.

	s := []string{"a", "b", "c"}
	_ = append(s)

Such calls are always no-ops and often indicate an underlying mistake.


Default: on.

Package documentation: [appends](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/appends)

<a id='asmdecl'></a>
## `asmdecl`: report mismatches between assembly files and Go declarations



Default: on.

Package documentation: [asmdecl](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/asmdecl)

<a id='assign'></a>
## `assign`: check for useless assignments

This checker reports assignments of the form x = x or a\[i] = a\[i]. These are almost always useless, and even when they aren't they are usually a mistake.


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

<a id='bloop'></a>
## `bloop`: replace for-range over b.N with b.Loop

The bloop analyzer suggests replacing benchmark loops of the form \`for i := 0; i \< b.N; i++\` or \`for range b.N\` with the more modern \`for b.Loop()\`, which was added in Go 1.24.

This change makes benchmark code more readable and also removes the need for manual timer control, so any preceding calls to b.StartTimer, b.StopTimer, or b.ResetTimer within the same function will also be removed.

Caveats: The b.Loop() method is designed to prevent the compiler from optimizing away the benchmark loop, which can occasionally result in slower execution due to increased allocations in some specific cases.


Default: on.

Package documentation: [bloop](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#bloop)

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

Check for invalid cgo pointer passing. This looks for code that uses cgo to call C code passing values whose types are almost always invalid according to the cgo pointer sharing rules. Specifically, it warns about attempts to pass a Go chan, map, func, or slice to C, either directly, or via a pointer, array, or struct.


Default: on.

Package documentation: [cgocall](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/cgocall)

<a id='composites'></a>
## `composites`: check for unkeyed composite literals

This analyzer reports a diagnostic for composite literals of struct types imported from another package that do not use the field-keyed syntax. Such literals are fragile because the addition of a new field (even if unexported) to the struct will cause compilation to fail.

As an example,

	err = &net.DNSConfigError{err}

should be replaced by:

	err = &net.DNSConfigError{Err: err}


Default: on.

Package documentation: [composites](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/composite)

<a id='copylocks'></a>
## `copylocks`: check for locks erroneously passed by value

Inadvertently copying a value containing a lock, such as sync.Mutex or sync.WaitGroup, may cause both copies to malfunction. Generally such values should be referred to through a pointer.


Default: on.

Package documentation: [copylocks](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/copylock)

<a id='deepequalerrors'></a>
## `deepequalerrors`: check for calls of reflect.DeepEqual on error values

The deepequalerrors checker looks for calls of the form:

	reflect.DeepEqual(err1, err2)

where err1 and err2 are errors. Using reflect.DeepEqual to compare errors is discouraged.


Default: on.

Package documentation: [deepequalerrors](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/deepequalerrors)

<a id='defers'></a>
## `defers`: report common mistakes in defer statements

The defers analyzer reports a diagnostic when a defer statement would result in a non-deferred call to time.Since, as experience has shown that this is nearly always a mistake.

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

The deprecated analyzer looks for deprecated symbols and package imports.

See [https://go.dev/wiki/Deprecated](https://go.dev/wiki/Deprecated) to learn about Go's convention for documenting and signaling deprecated identifiers.


Default: on.

Package documentation: [deprecated](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/deprecated)

<a id='directive'></a>
## `directive`: check Go toolchain directives such as //go:debug

This analyzer checks for problems with known Go toolchain directives in all Go source files in a package directory, even those excluded by //go:build constraints, and all non-Go source files too.

For //go:debug (see [https://go.dev/doc/godebug](https://go.dev/doc/godebug)), the analyzer checks that the directives are placed only in Go source files, only above the package comment, and only in package main or \*\_test.go files.

Support for other known directives may be added in the future.

This analyzer does not check //go:build, which is handled by the buildtag analyzer.


Default: on.

Package documentation: [directive](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/directive)

<a id='embed'></a>
## `embed`: check //go:embed directive usage

This analyzer checks that the embed package is imported if //go:embed directives are present, providing a suggested fix to add the import if it is missing.

This analyzer also checks that //go:embed directives precede the declaration of a single variable.


Default: on.

Package documentation: [embed](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/embeddirective)

<a id='errorsas'></a>
## `errorsas`: report passing non-pointer or non-error values to errors.As

The errorsas analyzer reports calls to errors.As where the type of the second argument is not a pointer to a type implementing error.


Default: on.

Package documentation: [errorsas](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/errorsas)

<a id='errorsastype'></a>
## `errorsastype`: replace errors.As with errors.AsType[T]

This analyzer suggests fixes to simplify uses of [errors.As](/errors#As) of this form:

	var myerr *MyErr
	if errors.As(err, &myerr) {
		handle(myerr)
	}

by using the less error-prone generic [errors.AsType](/errors#AsType) function, introduced in Go 1.26:

	if myerr, ok := errors.AsType[*MyErr](err); ok {
		handle(myerr)
	}

The fix is only offered if the var declaration has the form shown and there are no uses of myerr outside the if statement.


Default: on.

Package documentation: [errorsastype](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#errorsastype)

<a id='fillreturns'></a>
## `fillreturns`: suggest fixes for errors due to an incorrect number of return values

This checker provides suggested fixes for type errors of the type "wrong number of return values (want %d, got %d)". For example:

	func m() (int, string, *bool, error) {
		return
	}

will turn into

	func m() (int, string, *bool, error) {
		return 0, "", nil, nil
	}

This functionality is similar to [https://github.com/sqs/goreturns](https://github.com/sqs/goreturns).


Default: on.

Package documentation: [fillreturns](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/fillreturns)

<a id='fmtappendf'></a>
## `fmtappendf`: replace []byte(fmt.Sprintf) with fmt.Appendf

The fmtappendf analyzer suggests replacing \`\[]byte(fmt.Sprintf(...))\` with \`fmt.Appendf(nil, ...)\`. This avoids the intermediate allocation of a string by Sprintf, making the code more efficient. The suggestion also applies to fmt.Sprint and fmt.Sprintln.


Default: on.

Package documentation: [fmtappendf](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#fmtappendf)

<a id='forvar'></a>
## `forvar`: remove redundant re-declaration of loop variables

The forvar analyzer removes unnecessary shadowing of loop variables. Before Go 1.22, it was common to write \`for \_, x := range s { x := x ... }\` to create a fresh variable for each iteration. Go 1.22 changed the semantics of \`for\` loops, making this pattern redundant. This analyzer removes the unnecessary \`x := x\` statement.

This fix only applies to \`range\` loops.


Default: on.

Package documentation: [forvar](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#forvar)

<a id='framepointer'></a>
## `framepointer`: report assembly that clobbers the frame pointer before saving it



Default: on.

Package documentation: [framepointer](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/framepointer)

<a id='hostport'></a>
## `hostport`: check format of addresses passed to net.Dial

This analyzer flags code that produce network address strings using fmt.Sprintf, as in this example:

	addr := fmt.Sprintf("%s:%d", host, 12345) // "will not work with IPv6"
	...
	conn, err := net.Dial("tcp", addr)       // "when passed to dial here"

The analyzer suggests a fix to use the correct approach, a call to net.JoinHostPort:

	addr := net.JoinHostPort(host, "12345")
	...
	conn, err := net.Dial("tcp", addr)

A similar diagnostic and fix are produced for a format string of "%s:%s".


Default: on.

Package documentation: [hostport](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/hostport)

<a id='httpresponse'></a>
## `httpresponse`: check for mistakes using HTTP responses

A common mistake when using the net/http package is to defer a function call to close the http.Response Body before checking the error that determines whether the response is valid:

	resp, err := http.Head(url)
	defer resp.Body.Close()
	if err != nil {
		log.Fatal(err)
	}
	// (defer statement belongs here)

This checker helps uncover latent nil dereference bugs by reporting a diagnostic for such mistakes.


Default: on.

Package documentation: [httpresponse](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/httpresponse)

<a id='ifaceassert'></a>
## `ifaceassert`: detect impossible interface-to-interface type assertions

This checker flags type assertions v.(T) and corresponding type-switch cases in which the static type V of v is an interface that cannot possibly implement the target interface T. This occurs when V and T contain methods with the same name but different signatures. Example:

	var v interface {
		Read()
	}
	_ = v.(io.Reader)

The Read method in v has a different signature than the Read method in io.Reader, so this assertion cannot succeed.


Default: on.

Package documentation: [ifaceassert](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/ifaceassert)

<a id='infertypeargs'></a>
## `infertypeargs`: check for unnecessary type arguments in call expressions

Explicit type arguments may be omitted from call expressions if they can be inferred from function arguments, or from other type arguments:

	func f[T any](T) {}

	func _() {
		f[string]("foo") // string could be inferred
	}


Default: on.

Package documentation: [infertypeargs](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/infertypeargs)

<a id='inline'></a>
## `inline`: apply fixes based on 'go:fix inline' comment directives

The inline analyzer inlines functions and constants that are marked for inlining.

\## Functions

Given a function that is marked for inlining, like this one:

	//go:fix inline
	func Square(x int) int { return Pow(x, 2) }

this analyzer will recommend that calls to the function elsewhere, in the same or other packages, should be inlined.

Inlining can be used to move off of a deprecated function:

	// Deprecated: prefer Pow(x, 2).
	//go:fix inline
	func Square(x int) int { return Pow(x, 2) }

It can also be used to move off of an obsolete package, as when the import path has changed or a higher major version is available:

	package pkg

	import pkg2 "pkg/v2"

	//go:fix inline
	func F() { pkg2.F(nil) }

Replacing a call pkg.F() by pkg2.F(nil) can have no effect on the program, so this mechanism provides a low-risk way to update large numbers of calls. We recommend, where possible, expressing the old API in terms of the new one to enable automatic migration.

The inliner takes care to avoid behavior changes, even subtle ones, such as changes to the order in which argument expressions are evaluated. When it cannot safely eliminate all parameter variables, it may introduce a "binding declaration" of the form

	var params = args

to evaluate argument expressions in the correct order and bind them to parameter variables. Since the resulting code transformation may be stylistically suboptimal, such inlinings may be disabled by specifying the -inline.allow\_binding\_decl=false flag to the analyzer driver.

(In cases where it is not safe to "reduce" a call—that is, to replace a call f(x) by the body of function f, suitably substituted—the inliner machinery is capable of replacing f by a function literal, func(){...}(). However, the inline analyzer discards all such "literalizations" unconditionally, again on grounds of style.)

\## Constants

Given a constant that is marked for inlining, like this one:

	//go:fix inline
	const Ptr = Pointer

this analyzer will recommend that uses of Ptr should be replaced with Pointer.

As with functions, inlining can be used to replace deprecated constants and constants in obsolete packages.

A constant definition can be marked for inlining only if it refers to another named constant.

The "//go:fix inline" comment must appear before a single const declaration on its own, as above; before a const declaration that is part of a group, as in this case:

	const (
	   C = 1
	   //go:fix inline
	   Ptr = Pointer
	)

or before a group, applying to every constant in the group:

	//go:fix inline
	const (
		Ptr = Pointer
	    Val = Value
	)

The proposal [https://go.dev/issue/32816](https://go.dev/issue/32816) introduces the "//go:fix inline" directives.

You can use this command to apply inline fixes en masse:

	$ go run golang.org/x/tools/go/analysis/passes/inline/cmd/inline@latest -fix ./...


Default: on.

Package documentation: [inline](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/inline)

<a id='loopclosure'></a>
## `loopclosure`: check references to loop variables from within nested functions

This analyzer reports places where a function literal references the iteration variable of an enclosing loop, and the loop calls the function in such a way (e.g. with go or defer) that it may outlive the loop iteration and possibly observe the wrong value of the variable.

Note: An iteration variable can only outlive a loop iteration in Go versions \<=1.21. In Go 1.22 and later, the loop variable lifetimes changed to create a new iteration variable per loop iteration. (See go.dev/issue/60078.)

In this example, all the deferred functions run after the loop has completed, so all observe the final value of v \[\<go1.22].

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

After Go version 1.22, the previous two for loops are equivalent and both are correct.

The next example uses a go statement and has a similar problem \[\<go1.22]. In addition, it has a data race because the loop updates v concurrent with the goroutines accessing it.

	for _, v := range elem {
	    go func() {
	        use(v)  // incorrect, and a data race
	    }()
	}

A fix is the same as before. The checker also reports problems in goroutines started by golang.org/x/sync/errgroup.Group. A hard-to-spot variant of this form is common in parallel tests:

	func Test(t *testing.T) {
	    for _, test := range tests {
	        t.Run(test.name, func(t *testing.T) {
	            t.Parallel()
	            use(test) // incorrect, and a data race
	        })
	    }
	}

The t.Parallel() call causes the rest of the function to execute concurrent with the loop \[\<go1.22].

The analyzer reports references only in the last statement, as it is not deep enough to understand the effects of subsequent statements that might render the reference benign. ("Last statement" is defined recursively in compound statements such as if, switch, and select.)

See: [https://golang.org/doc/go\_faq.html#closures\_and\_goroutines](https://golang.org/doc/go_faq.html#closures_and_goroutines)


Default: on.

Package documentation: [loopclosure](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/loopclosure)

<a id='lostcancel'></a>
## `lostcancel`: check cancel func returned by context.WithCancel is called

The cancellation function returned by context.WithCancel, WithTimeout, WithDeadline and variants such as WithCancelCause must be called, or the new context will remain live until its parent context is cancelled. (The background context is never cancelled.)


Default: on.

Package documentation: [lostcancel](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/lostcancel)

<a id='maprange'></a>
## `maprange`: checks for unnecessary calls to maps.Keys and maps.Values in range statements

Consider a loop written like this:

	for val := range maps.Values(m) {
		fmt.Println(val)
	}

This should instead be written without the call to maps.Values:

	for _, val := range m {
		fmt.Println(val)
	}

golang.org/x/exp/maps returns slices for Keys/Values instead of iterators, but unnecessary calls should similarly be removed:

	for _, key := range maps.Keys(m) {
		fmt.Println(key)
	}

should be rewritten as:

	for key := range m {
		fmt.Println(key)
	}


Default: on.

Package documentation: [maprange](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/maprange)

<a id='mapsloop'></a>
## `mapsloop`: replace explicit loops over maps with calls to maps package

The mapsloop analyzer replaces loops of the form

	for k, v := range x { m[k] = v }

with a single call to a function from the \`maps\` package, added in Go 1.23. Depending on the context, this could be \`maps.Copy\`, \`maps.Insert\`, \`maps.Clone\`, or \`maps.Collect\`.

The transformation to \`maps.Clone\` is applied conservatively, as it preserves the nilness of the source map, which may be a subtle change in behavior if the original code did not handle a nil map in the same way.


Default: on.

Package documentation: [mapsloop](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#mapsloop)

<a id='minmax'></a>
## `minmax`: replace if/else statements with calls to min or max

The minmax analyzer simplifies conditional assignments by suggesting the use of the built-in \`min\` and \`max\` functions, introduced in Go 1.21. For example,

	if a < b { x = a } else { x = b }

is replaced by

	x = min(a, b).

This analyzer avoids making suggestions for floating-point types, as the behavior of \`min\` and \`max\` with NaN values can differ from the original if/else statement.


Default: on.

Package documentation: [minmax](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#minmax)

<a id='newexpr'></a>
## `newexpr`: simplify code by using go1.26's new(expr)

This analyzer finds declarations of functions of this form:

	func varOf(x int) *int { return &x }

and suggests a fix to turn them into inlinable wrappers around go1.26's built-in new(expr) function:

	//go:fix inline
	func varOf(x int) *int { return new(x) }

(The directive comment causes the 'inline' analyzer to suggest that calls to such functions are inlined.)

In addition, this analyzer suggests a fix for each call to one of the functions before it is transformed, so that

	use(varOf(123))

is replaced by:

	use(new(123))

Wrapper functions such as varOf are common when working with Go serialization packages such as for JSON or protobuf, where pointers are often used to express optionality.


Default: on.

Package documentation: [newexpr](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/modernize#newexpr)

<a id='nilfunc'></a>
## `nilfunc`: check for useless comparisons between functions and nil

A useless comparison is one like f == nil as opposed to f() == nil.


Default: on.

Package documentation: [nilfunc](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/nilfunc)

<a id='nilness'></a>
## `nilness`: check for redundant or impossible nil comparisons

The nilness checker inspects the control-flow graph of each function in a package and reports nil pointer dereferences, degenerate nil pointers, and panics with nil values. A degenerate comparison is of the form x==nil or x!=nil where x is statically known to be nil or non-nil. These are often a mistake, especially in control flow related to errors. Panics with nil values are checked because they are not detectable by

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

Sometimes the control flow may be quite complex, making bugs hard to spot. In the example below, the err.Error expression is guaranteed to panic because, after the first return, err must be nil. The intervening loop is just a distraction.

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

This checker provides suggested fixes for type errors of the type "no new vars on left side of :=". For example:

	z := 1
	z := 2

will turn into

	z := 1
	z = 2


Default: on.

Package documentation: [nonewvars](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/nonewvars)

<a id='noresultvalues'></a>
## `noresultvalues`: suggested fixes for unexpected return values

This checker provides suggested fixes for type errors of the type "no result values expected" or "too many return values". For example:

	func z() { return nil }

will turn into

	func z() { return }


Default: on.

Package documentation: [noresultvalues](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/noresultvalues)

<a id='omitzero'></a>
## `omitzero`: suggest replacing omitempty with omitzero for struct fields

The omitzero analyzer identifies uses of the \`omitempty\` JSON struct tag on fields that are themselves structs. For struct-typed fields, the \`omitempty\` tag has no effect on the behavior of json.Marshal and json.Unmarshal. The analyzer offers two suggestions: either remove the tag, or replace it with \`omitzero\` (added in Go 1.24), which correctly omits the field if the struct value is zero.

However, some other serialization packages (notably kubebuilder, see [https://book.kubebuilder.io/reference/markers.html](https://book.kubebuilder.io/reference/markers.html)) may have their own interpretation of the \`json:",omitzero"\` tag, so removing it may affect program behavior. For this reason, the omitzero modernizer will not make changes in any package that contains +kubebuilder annotations.

Replacing \`omitempty\` with \`omitzero\` is a change in behavior. The original code would always encode the struct field, whereas the modified code will omit it if it is a zero-value.


Default: on.

Package documentation: [omitzero](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#omitzero)

<a id='plusbuild'></a>
## `plusbuild`: remove obsolete //+build comments

The plusbuild analyzer suggests a fix to remove obsolete build tags of the form:

	//+build linux,amd64

in files that also contain a Go 1.18-style tag such as:

	//go:build linux && amd64

(It does not check that the old and new tags are consistent; that is the job of the 'buildtag' analyzer in the vet suite.)


Default: on.

Package documentation: [plusbuild](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#plusbuild)

<a id='printf'></a>
## `printf`: check consistency of Printf format strings and arguments

The check applies to calls of the formatting functions such as [fmt.Printf](/fmt#Printf) and [fmt.Sprintf](/fmt#Sprintf), as well as any detected wrappers of those functions such as [log.Printf](/log#Printf). It reports a variety of mistakes such as syntax errors in the format string and mismatches (of number and type) between the verbs and their arguments.

See the documentation of the fmt package for the complete set of format operators and their operand types.


Default: on.

Package documentation: [printf](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/printf)

<a id='rangeint'></a>
## `rangeint`: replace 3-clause for loops with for-range over integers

The rangeint analyzer suggests replacing traditional for loops such as

	for i := 0; i < n; i++ { ... }

with the more idiomatic Go 1.22 style:

	for i := range n { ... }

This transformation is applied only if (a) the loop variable is not modified within the loop body and (b) the loop's limit expression is not modified within the loop, as \`for range\` evaluates its operand only once.


Default: on.

Package documentation: [rangeint](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#rangeint)

<a id='recursiveiter'></a>
## `recursiveiter`: check for inefficient recursive iterators

This analyzer reports when a function that returns an iterator (iter.Seq or iter.Seq2) calls itself as the operand of a range statement, as this is inefficient.

When implementing an iterator (e.g. iter.Seq\[T]) for a recursive data type such as a tree or linked list, it is tempting to recursively range over the iterator for each child element.

Here's an example of a naive iterator over a binary tree:

	type tree struct {
		value       int
		left, right *tree
	}

	func (t *tree) All() iter.Seq[int] {
		return func(yield func(int) bool) {
			if t != nil {
				for elem := range t.left.All() { // "inefficient recursive iterator"
					if !yield(elem) {
						return
					}
				}
				if !yield(t.value) {
					return
				}
				for elem := range t.right.All() { // "inefficient recursive iterator"
					if !yield(elem) {
						return
					}
				}
			}
		}
	}

Though it correctly enumerates the elements of the tree, it hides a significant performance problem--two, in fact. Consider a balanced tree of N nodes. Iterating the root node will cause All to be called once on every node of the tree. This results in a chain of nested active range-over-func statements when yield(t.value) is called on a leaf node.

The first performance problem is that each range-over-func statement must typically heap-allocate a variable, so iteration of the tree allocates as many variables as there are elements in the tree, for a total of O(N) allocations, all unnecessary.

The second problem is that each call to yield for a leaf of the tree causes each of the enclosing range loops to receive a value, which they then immediately pass on to their respective yield function. This results in a chain of log(N) dynamic yield calls per element, a total of O(N\*log N) dynamic calls overall, when only O(N) are necessary.

A better implementation strategy for recursive iterators is to first define the "every" operator for your recursive data type, where every(f) reports whether an arbitrary predicate f(x) is true for every element x in the data type. For our tree, the every function would be:

	func (t *tree) every(f func(int) bool) bool {
		return t == nil ||
			t.left.every(f) && f(t.value) && t.right.every(f)
	}

For example, this use of the every operator prints whether every element in the tree is an even number:

	even := func(x int) bool { return x&1 == 0 }
	println(t.every(even))

Then the iterator can be simply expressed as a trivial wrapper around the every operator:

	func (t *tree) All() iter.Seq[int] {
		return func(yield func(int) bool) {
			_ = t.every(yield)
		}
	}

In effect, tree.All computes whether yield returns true for each element, short-circuiting if it ever returns false, then discards the final boolean result.

This has much better performance characteristics: it makes one dynamic call per element of the tree, and it doesn't heap-allocate anything. It is also clearer.


Default: on.

Package documentation: [recursiveiter](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/recursiveiter)

<a id='reflecttypefor'></a>
## `reflecttypefor`: replace reflect.TypeOf(x) with TypeFor[T]()

This analyzer suggests fixes to replace uses of reflect.TypeOf(x) with reflect.TypeFor, introduced in go1.22, when the desired runtime type is known at compile time, for example:

	reflect.TypeOf(uint32(0))        -> reflect.TypeFor[uint32]()
	reflect.TypeOf((*ast.File)(nil)) -> reflect.TypeFor[*ast.File]()

It also offers a fix to simplify the construction below, which uses reflect.TypeOf to return the runtime type for an interface type,

	reflect.TypeOf((*io.Reader)(nil)).Elem()

to:

	reflect.TypeFor[io.Reader]()

No fix is offered in cases when the runtime type is dynamic, such as:

	var r io.Reader = ...
	reflect.TypeOf(r)

or when the operand has potential side effects.


Default: on.

Package documentation: [reflecttypefor](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#reflecttypefor)

<a id='shadow'></a>
## `shadow`: check for possible unintended shadowing of variables

This analyzer check for shadowed variables. A shadowed variable is a variable declared in an inner scope with the same name and type as a variable in an outer scope, and where the outer variable is mentioned after the inner one is declared.

(This definition can be refined; the module generates too many false positives and is not yet enabled by default.)

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

<a id='slicescontains'></a>
## `slicescontains`: replace loops with slices.Contains or slices.ContainsFunc

The slicescontains analyzer simplifies loops that check for the existence of an element in a slice. It replaces them with calls to \`slices.Contains\` or \`slices.ContainsFunc\`, which were added in Go 1.21.

If the expression for the target element has side effects, this transformation will cause those effects to occur only once, not once per tested slice element.


Default: on.

Package documentation: [slicescontains](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#slicescontains)

<a id='slicesdelete'></a>
## `slicesdelete`: replace append-based slice deletion with slices.Delete

The slicesdelete analyzer suggests replacing the idiom

	s = append(s[:i], s[j:]...)

with the more explicit

	s = slices.Delete(s, i, j)

introduced in Go 1.21.

This analyzer is disabled by default. The \`slices.Delete\` function zeros the elements between the new length and the old length of the slice to prevent memory leaks, which is a subtle difference in behavior compared to the append-based idiom; see [https://go.dev/issue/73686](https://go.dev/issue/73686).


Default: off. Enable by setting `"analyses": {"slicesdelete": true}`.

Package documentation: [slicesdelete](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#slicesdelete)

<a id='slicessort'></a>
## `slicessort`: replace sort.Slice with slices.Sort for basic types

The slicessort analyzer simplifies sorting slices of basic ordered types. It replaces

	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })

with the simpler \`slices.Sort(s)\`, which was added in Go 1.21.


Default: on.

Package documentation: [slicessort](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#slicessort)

<a id='slog'></a>
## `slog`: check for invalid structured logging calls

The slog checker looks for calls to functions from the log/slog package that take alternating key-value pairs. It reports calls where an argument in a key position is neither a string nor a slog.Attr, and where a final key is missing its value. For example,it would report

	slog.Warn("message", 11, "k") // slog.Warn arg "11" should be a string or a slog.Attr

and

	slog.Info("message", "k1", v1, "k2") // call to slog.Info missing a final value


Default: on.

Package documentation: [slog](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/slog)

<a id='sortslice'></a>
## `sortslice`: check the argument type of sort.Slice

sort.Slice requires an argument of a slice type. Check that the interface{} value passed to sort.Slice is actually a slice.


Default: on.

Package documentation: [sortslice](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/sortslice)

<a id='stditerators'></a>
## `stditerators`: use iterators instead of Len/At-style APIs

This analyzer suggests a fix to replace each loop of the form:

	for i := 0; i < x.Len(); i++ {
		use(x.At(i))
	}

or its "for elem := range x.Len()" equivalent by a range loop over an iterator offered by the same data type:

	for elem := range x.All() {
		use(x.At(i)
	}

where x is one of various well-known types in the standard library.


Default: on.

Package documentation: [stditerators](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#stditerators)

<a id='stdmethods'></a>
## `stdmethods`: check signature of methods of well-known interfaces

Sometimes a type may be intended to satisfy an interface but may fail to do so because of a mistake in its method signature. For example, the result of this WriteTo method should be (int64, error), not error, to satisfy io.WriterTo:

	type myWriterTo struct{...}
	func (myWriterTo) WriteTo(w io.Writer) error { ... }

This check ensures that each method whose name matches one of several well-known interface methods from the standard library has the correct signature for that interface.

Checked method names include:

	Format GobEncode GobDecode MarshalJSON MarshalXML
	Peek ReadByte ReadFrom ReadRune Scan Seek
	UnmarshalJSON UnreadByte UnreadRune WriteByte
	WriteTo


Default: on.

Package documentation: [stdmethods](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/stdmethods)

<a id='stdversion'></a>
## `stdversion`: report uses of too-new standard library symbols

The stdversion analyzer reports references to symbols in the standard library that were introduced by a Go release higher than the one in force in the referring file. (Recall that the file's Go version is defined by the 'go' directive its module's go.mod file, or by a "//go:build go1.X" build tag at the top of the file.)

The analyzer does not report a diagnostic for a reference to a "too new" field or method of a type that is itself "too new", as this may have false positives, for example if fields or methods are accessed through a type alias that is guarded by a Go version constraint.


Default: on.

Package documentation: [stdversion](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/stdversion)

<a id='stringintconv'></a>
## `stringintconv`: check for string(int) conversions

This checker flags conversions of the form string(x) where x is an integer (but not byte or rune) type. Such conversions are discouraged because they return the UTF-8 representation of the Unicode code point x, and not a decimal string representation of x as one might expect. Furthermore, if x denotes an invalid code point, the conversion cannot be statically rejected.

For conversions that intend on using the code point, consider replacing them with string(rune(x)). Otherwise, strconv.Itoa and its equivalents return the string representation of the value in the desired base.


Default: on.

Package documentation: [stringintconv](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/stringintconv)

<a id='stringsbuilder'></a>
## `stringsbuilder`: replace += with strings.Builder

This analyzer replaces repeated string += string concatenation operations with calls to Go 1.10's strings.Builder.

For example:

	var s = "["
	for x := range seq {
		s += x
		s += "."
	}
	s += "]"
	use(s)

is replaced by:

	var s strings.Builder
	s.WriteString("[")
	for x := range seq {
		s.WriteString(x)
		s.WriteString(".")
	}
	s.WriteString("]")
	use(s.String())

This avoids quadratic memory allocation and improves performance.

The analyzer requires that all references to s except the final one are += operations. To avoid warning about trivial cases, at least one must appear within a loop. The variable s must be a local variable, not a global or parameter.

The sole use of the finished string must be the last reference to the variable s. (It may appear within an intervening loop or function literal, since even s.String() is called repeatedly, it does not allocate memory.)


Default: on.

Package documentation: [stringsbuilder](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#stringbuilder)

<a id='stringscut'></a>
## `stringscut`: replace strings.Index etc. with strings.Cut

This analyzer replaces certain patterns of use of [strings.Index](/strings#Index) and string slicing by [strings.Cut](/strings#Cut), added in go1.18.

For example:

	idx := strings.Index(s, substr)
	if idx >= 0 {
	    return s[:idx]
	}

is replaced by:

	before, _, ok := strings.Cut(s, substr)
	if ok {
	    return before
	}

And:

	idx := strings.Index(s, substr)
	if idx >= 0 {
	    return
	}

is replaced by:

	found := strings.Contains(s, substr)
	if found {
	    return
	}

It also handles variants using [strings.IndexByte](/strings#IndexByte) instead of Index, or the bytes package instead of strings.

Fixes are offered only in cases in which there are no potential modifications of the idx, s, or substr expressions between their definition and use.


Default: on.

Package documentation: [stringscut](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/modernize#stringscut)

<a id='stringscutprefix'></a>
## `stringscutprefix`: replace HasPrefix/TrimPrefix with CutPrefix

The stringscutprefix analyzer simplifies a common pattern where code first checks for a prefix with \`strings.HasPrefix\` and then removes it with \`strings.TrimPrefix\`. It replaces this two-step process with a single call to \`strings.CutPrefix\`, introduced in Go 1.20. The analyzer also handles the equivalent functions in the \`bytes\` package.

For example, this input:

	if strings.HasPrefix(s, prefix) {
	    use(strings.TrimPrefix(s, prefix))
	}

is fixed to:

	if after, ok := strings.CutPrefix(s, prefix); ok {
	    use(after)
	}

The analyzer also offers fixes to use CutSuffix in a similar way. This input:

	if strings.HasSuffix(s, suffix) {
	    use(strings.TrimSuffix(s, suffix))
	}

is fixed to:

	if before, ok := strings.CutSuffix(s, suffix); ok {
	    use(before)
	}


Default: on.

Package documentation: [stringscutprefix](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#stringscutprefix)

<a id='stringsseq'></a>
## `stringsseq`: replace ranging over Split/Fields with SplitSeq/FieldsSeq

The stringsseq analyzer improves the efficiency of iterating over substrings. It replaces

	for range strings.Split(...)

with the more efficient

	for range strings.SplitSeq(...)

which was added in Go 1.24 and avoids allocating a slice for the substrings. The analyzer also handles strings.Fields and the equivalent functions in the bytes package.


Default: on.

Package documentation: [stringsseq](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#stringsseq)

<a id='structtag'></a>
## `structtag`: check that struct field tags conform to reflect.StructTag.Get

Also report certain struct tags (json, xml) used with unexported fields.


Default: on.

Package documentation: [structtag](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/structtag)

<a id='testingcontext'></a>
## `testingcontext`: replace context.WithCancel with t.Context in tests

The testingcontext analyzer simplifies context management in tests. It replaces the manual creation of a cancellable context,

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

with a single call to t.Context(), which was added in Go 1.24.

This change is only suggested if the \`cancel\` function is not used for any other purpose.


Default: on.

Package documentation: [testingcontext](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#testingcontext)

<a id='testinggoroutine'></a>
## `testinggoroutine`: report calls to (*testing.T).Fatal from goroutines started by a test

Functions that abruptly terminate a test, such as the Fatal, Fatalf, FailNow, and Skip{,f,Now} methods of \*testing.T, must be called from the test goroutine itself. This checker detects calls to these functions that occur within a goroutine started by the test. For example:

	func TestFoo(t *testing.T) {
	    go func() {
	        t.Fatal("oops") // error: (*T).Fatal called from non-test goroutine
	    }()
	}


Default: on.

Package documentation: [testinggoroutine](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/testinggoroutine)

<a id='tests'></a>
## `tests`: check for common mistaken usages of tests and examples

The tests checker walks Test, Benchmark, Fuzzing and Example functions checking malformed names, wrong signatures and examples documenting non-existent identifiers.

Please see the documentation for package testing in golang.org/pkg/testing for the conventions that are enforced for Tests, Benchmarks, and Examples.


Default: on.

Package documentation: [tests](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/tests)

<a id='timeformat'></a>
## `timeformat`: check for calls of (time.Time).Format or time.Parse with 2006-02-01

The timeformat checker looks for time formats with the 2006-02-01 (yyyy-dd-mm) format. Internationally, "yyyy-dd-mm" does not occur in common calendar date standards, and so it is more likely that 2006-01-02 (yyyy-mm-dd) was intended.


Default: on.

Package documentation: [timeformat](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/timeformat)

<a id='unmarshal'></a>
## `unmarshal`: report passing non-pointer or non-interface values to unmarshal

The unmarshal analysis reports calls to functions such as json.Unmarshal in which the argument type is not a pointer or an interface.


Default: on.

Package documentation: [unmarshal](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/unmarshal)

<a id='unreachable'></a>
## `unreachable`: check for unreachable code

The unreachable analyzer finds statements that execution can never reach because they are preceded by a return statement, a call to panic, an infinite loop, or similar constructs.


Default: on.

Package documentation: [unreachable](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/unreachable)

<a id='unsafefuncs'></a>
## `unsafefuncs`: replace unsafe pointer arithmetic with function calls

The unsafefuncs analyzer simplifies pointer arithmetic expressions by replacing them with calls to helper functions such as unsafe.Add, added in Go 1.17.

Example:

	unsafe.Pointer(uintptr(ptr) + uintptr(n))

where ptr is an unsafe.Pointer, is replaced by:

	unsafe.Add(ptr, n)


Default: on.

Package documentation: [unsafefuncs](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#unsafefuncs)

<a id='unsafeptr'></a>
## `unsafeptr`: check for invalid conversions of uintptr to unsafe.Pointer

The unsafeptr analyzer reports likely incorrect uses of unsafe.Pointer to convert integers to pointers. A conversion from uintptr to unsafe.Pointer is invalid if it implies that there is a uintptr-typed word in memory that holds a pointer value, because that word will be invisible to stack copying and to the garbage collector.


Default: on.

Package documentation: [unsafeptr](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/unsafeptr)

<a id='unusedfunc'></a>
## `unusedfunc`: check for unused functions, methods, etc

The unusedfunc analyzer reports functions and methods that are never referenced outside of their own declaration.

A function is considered unused if it is unexported and not referenced (except within its own declaration).

A method is considered unused if it is unexported, not referenced (except within its own declaration), and its name does not match that of any method of an interface type declared within the same package.

The tool may report false positives in some situations, for example:

  - for a declaration of an unexported function that is referenced from another package using the go:linkname mechanism, if the declaration's doc comment does not also have a go:linkname comment.

    (Such code is in any case strongly discouraged: linkname annotations, if they must be used at all, should be used on both the declaration and the alias.)

  - for compiler intrinsics in the "runtime" package that, though never referenced, are known to the compiler and are called indirectly by compiled object code.

  - for functions called only from assembly.

  - for functions called only from files whose build tags are not selected in the current build configuration.

Since these situations are relatively common in the low-level parts of the runtime, this analyzer ignores the standard library. See [https://go.dev/issue/71686](https://go.dev/issue/71686) and [https://go.dev/issue/74130](https://go.dev/issue/74130) for further discussion of these limitations.

The unusedfunc algorithm is not as precise as the golang.org/x/tools/cmd/deadcode tool, but it has the advantage that it runs within the modular analysis framework, enabling near real-time feedback within gopls.

The unusedfunc analyzer also reports unused types, vars, and constants. Enums--constants defined with iota--are ignored since even the unused values must remain present to preserve the logical ordering.


Default: on.

Package documentation: [unusedfunc](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/unusedfunc)

<a id='unusedparams'></a>
## `unusedparams`: check for unused parameters of functions

The unusedparams analyzer checks functions to see if there are any parameters that are not being used.

To ensure soundness, it ignores:

  - "address-taken" functions, that is, functions that are used as a value rather than being called directly; their signatures may be required to conform to a func type.
  - exported functions or methods, since they may be address-taken in another package.
  - unexported methods whose name matches an interface method declared in the same package, since the method's signature may be required to conform to the interface type.
  - functions with empty bodies, or containing just a call to panic.
  - parameters that are unnamed, or named "\_", the blank identifier.

The analyzer suggests a fix of replacing the parameter name by "\_", but in such cases a deeper fix can be obtained by invoking the "Refactor: remove unused parameter" code action, which will eliminate the parameter entirely, along with all corresponding arguments at call sites, while taking care to preserve any side effects in the argument expressions; see [https://github.com/golang/tools/releases/tag/gopls%2Fv0.14](https://github.com/golang/tools/releases/tag/gopls%2Fv0.14).

This analyzer ignores generated code.


Default: on.

Package documentation: [unusedparams](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/unusedparams)

<a id='unusedresult'></a>
## `unusedresult`: check for unused results of calls to some functions

Some functions like fmt.Errorf return a result and have no side effects, so it is always a mistake to discard the result. Other functions may return an error that must not be ignored, or a cleanup operation that must be called. This analyzer reports calls to functions like these when the result of the call is ignored.

The set of functions may be controlled using flags.


Default: on.

Package documentation: [unusedresult](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/unusedresult)

<a id='unusedvariable'></a>
## `unusedvariable`: check for unused variables and suggest fixes



Default: on.

Package documentation: [unusedvariable](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/unusedvariable)

<a id='unusedwrite'></a>
## `unusedwrite`: checks for unused writes

The analyzer reports instances of writes to struct fields and arrays that are never read. Specifically, when a struct object or an array is copied, its elements are copied implicitly by the compiler, and any element write to this copy does nothing with the original object.

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

<a id='waitgroup'></a>
## `waitgroup`: check for misuses of sync.WaitGroup

This analyzer detects mistaken calls to the (\*sync.WaitGroup).Add method from inside a new goroutine, causing Add to race with Wait:

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

<a id='waitgroup'></a>
## `waitgroup`: replace wg.Add(1)/go/wg.Done() with wg.Go

The waitgroup analyzer simplifies goroutine management with \`sync.WaitGroup\`. It replaces the common pattern

	wg.Add(1)
	go func() {
		defer wg.Done()
		...
	}()

with a single call to

	wg.Go(func(){ ... })

which was added in Go 1.25.


Default: on.

Package documentation: [waitgroup](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#waitgroup)

<a id='yield'></a>
## `yield`: report calls to yield where the result is ignored

After a yield function returns false, the caller should not call the yield function again; generally the iterator should return promptly.

This example fails to check the result of the call to yield, causing this analyzer to report a diagnostic:

	yield(1) // yield may be called again (on L2) after returning false
	yield(2)

The corrected code is either this:

	if yield(1) { yield(2) }

or simply:

	_ = yield(1) && yield(2)

It is not always a mistake to ignore the result of yield. For example, this is a valid single-element iterator:

	yield(1) // ok to ignore result
	return

It is only a mistake when the yield call that returned false may be followed by another call.


Default: on.

Package documentation: [yield](https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/yield)

<!-- END Analyzers: DO NOT MANUALLY EDIT THIS SECTION -->
