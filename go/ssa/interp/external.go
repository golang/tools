// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package interp

// Emulated functions that we cannot interpret because they are
// external or because they use "unsafe" or "reflect" operations.

import (
	"bytes"
	"maps"
	"math"
	"os"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type externalFn func(fr *frame, args []value) value

// TODO(adonovan): fix: reflect.Value abstracts an lvalue or an
// rvalue; Set() causes mutations that can be observed via aliases.
// We have not captured that correctly here.

// Key strings are from Function.String().
var externals = make(map[string]externalFn)

func init() {
	// That little dot ۰ is an Arabic zero numeral (U+06F0), categories [Nd].
	maps.Copy(externals, map[string]externalFn{
		"(reflect.Value).Bool":            ext۰reflect۰Value۰Bool,
		"(reflect.Value).CanAddr":         ext۰reflect۰Value۰CanAddr,
		"(reflect.Value).CanInterface":    ext۰reflect۰Value۰CanInterface,
		"(reflect.Value).Elem":            ext۰reflect۰Value۰Elem,
		"(reflect.Value).Field":           ext۰reflect۰Value۰Field,
		"(reflect.Value).Float":           ext۰reflect۰Value۰Float,
		"(reflect.Value).Index":           ext۰reflect۰Value۰Index,
		"(reflect.Value).Int":             ext۰reflect۰Value۰Int,
		"(reflect.Value).Interface":       ext۰reflect۰Value۰Interface,
		"(reflect.Value).IsNil":           ext۰reflect۰Value۰IsNil,
		"(reflect.Value).IsValid":         ext۰reflect۰Value۰IsValid,
		"(reflect.Value).Kind":            ext۰reflect۰Value۰Kind,
		"(reflect.Value).Len":             ext۰reflect۰Value۰Len,
		"(reflect.Value).MapIndex":        ext۰reflect۰Value۰MapIndex,
		"(reflect.Value).MapKeys":         ext۰reflect۰Value۰MapKeys,
		"(reflect.Value).NumField":        ext۰reflect۰Value۰NumField,
		"(reflect.Value).NumMethod":       ext۰reflect۰Value۰NumMethod,
		"(reflect.Value).Pointer":         ext۰reflect۰Value۰Pointer,
		"(reflect.Value).Set":             ext۰reflect۰Value۰Set,
		"(reflect.Value).String":          ext۰reflect۰Value۰String,
		"(reflect.Value).Type":            ext۰reflect۰Value۰Type,
		"(reflect.Value).Uint":            ext۰reflect۰Value۰Uint,
		"(reflect.error).Error":           ext۰reflect۰error۰Error,
		"(reflect.rtype).Bits":            ext۰reflect۰rtype۰Bits,
		"(reflect.rtype).Elem":            ext۰reflect۰rtype۰Elem,
		"(reflect.rtype).Field":           ext۰reflect۰rtype۰Field,
		"(reflect.rtype).In":              ext۰reflect۰rtype۰In,
		"(reflect.rtype).Kind":            ext۰reflect۰rtype۰Kind,
		"(reflect.rtype).NumField":        ext۰reflect۰rtype۰NumField,
		"(reflect.rtype).NumIn":           ext۰reflect۰rtype۰NumIn,
		"(reflect.rtype).NumMethod":       ext۰reflect۰rtype۰NumMethod,
		"(reflect.rtype).NumOut":          ext۰reflect۰rtype۰NumOut,
		"(reflect.rtype).Out":             ext۰reflect۰rtype۰Out,
		"(reflect.rtype).Size":            ext۰reflect۰rtype۰Size,
		"(reflect.rtype).String":          ext۰reflect۰rtype۰String,
		"bytes.Equal":                     ext۰bytes۰Equal,
		"bytes.IndexByte":                 ext۰bytes۰IndexByte,
		"fmt.Sprint":                      ext۰fmt۰Sprint,
		"math.Abs":                        ext۰math۰Abs,
		"math.Copysign":                   ext۰math۰Copysign,
		"math.Exp":                        ext۰math۰Exp,
		"math.Float32bits":                ext۰math۰Float32bits,
		"math.Float32frombits":            ext۰math۰Float32frombits,
		"math.Float64bits":                ext۰math۰Float64bits,
		"math.Float64frombits":            ext۰math۰Float64frombits,
		"math.Inf":                        ext۰math۰Inf,
		"math.IsNaN":                      ext۰math۰IsNaN,
		"math.Ldexp":                      ext۰math۰Ldexp,
		"math.Log":                        ext۰math۰Log,
		"math.Min":                        ext۰math۰Min,
		"math.NaN":                        ext۰math۰NaN,
		"math.Sqrt":                       ext۰math۰Sqrt,
		"os.Exit":                         ext۰os۰Exit,
		"os.Getenv":                       ext۰os۰Getenv,
		"reflect.New":                     ext۰reflect۰New,
		"reflect.SliceOf":                 ext۰reflect۰SliceOf,
		"reflect.TypeOf":                  ext۰reflect۰TypeOf,
		"reflect.ValueOf":                 ext۰reflect۰ValueOf,
		"reflect.Zero":                    ext۰reflect۰Zero,
		"runtime.Breakpoint":              ext۰runtime۰Breakpoint,
		"runtime.GC":                      ext۰runtime۰GC,
		"runtime.GOMAXPROCS":              ext۰runtime۰GOMAXPROCS,
		"runtime.GOROOT":                  ext۰runtime۰GOROOT,
		"runtime.Goexit":                  ext۰runtime۰Goexit,
		"runtime.Gosched":                 ext۰runtime۰Gosched,
		"runtime.NumCPU":                  ext۰runtime۰NumCPU,
		"sort.Float64s":                   ext۰sort۰Float64s,
		"sort.Ints":                       ext۰sort۰Ints,
		"sort.Strings":                    ext۰sort۰Strings,
		"strconv.Atoi":                    ext۰strconv۰Atoi,
		"strconv.Itoa":                    ext۰strconv۰Itoa,
		"strconv.FormatFloat":             ext۰strconv۰FormatFloat,
		"strings.Count":                   ext۰strings۰Count,
		"strings.EqualFold":               ext۰strings۰EqualFold,
		"strings.Index":                   ext۰strings۰Index,
		"strings.IndexByte":               ext۰strings۰IndexByte,
		"strings.Replace":                 ext۰strings۰Replace,
		"strings.ToLower":                 ext۰strings۰ToLower,
		"time.Sleep":                      ext۰time۰Sleep,
		"unicode/utf8.DecodeRuneInString": ext۰unicode۰utf8۰DecodeRuneInString,
	})
}

func ext۰bytes۰Equal(fr *frame, args []value) value {
	// func Equal(a, b []byte) bool
	a := args[0].([]value)
	b := args[1].([]value)
	return slices.Equal(a, b)
}

func ext۰bytes۰IndexByte(fr *frame, args []value) value {
	// func IndexByte(s []byte, c byte) int
	s := args[0].([]value)
	c := args[1].(byte)
	for i, b := range s {
		if b.(byte) == c {
			return i
		}
	}
	return -1
}

func ext۰math۰Float64frombits(fr *frame, args []value) value {
	return math.Float64frombits(args[0].(uint64))
}

func ext۰math۰Float64bits(fr *frame, args []value) value {
	return math.Float64bits(args[0].(float64))
}

func ext۰math۰Float32frombits(fr *frame, args []value) value {
	return math.Float32frombits(args[0].(uint32))
}

func ext۰math۰Abs(fr *frame, args []value) value {
	return math.Abs(args[0].(float64))
}

func ext۰math۰Copysign(fr *frame, args []value) value {
	return math.Copysign(args[0].(float64), args[1].(float64))
}

func ext۰math۰Exp(fr *frame, args []value) value {
	return math.Exp(args[0].(float64))
}

func ext۰math۰Float32bits(fr *frame, args []value) value {
	return math.Float32bits(args[0].(float32))
}

func ext۰math۰Min(fr *frame, args []value) value {
	return math.Min(args[0].(float64), args[1].(float64))
}

func ext۰math۰NaN(fr *frame, args []value) value {
	return math.NaN()
}

func ext۰math۰IsNaN(fr *frame, args []value) value {
	return math.IsNaN(args[0].(float64))
}

func ext۰math۰Inf(fr *frame, args []value) value {
	return math.Inf(args[0].(int))
}

func ext۰math۰Ldexp(fr *frame, args []value) value {
	return math.Ldexp(args[0].(float64), args[1].(int))
}

func ext۰math۰Log(fr *frame, args []value) value {
	return math.Log(args[0].(float64))
}

func ext۰math۰Sqrt(fr *frame, args []value) value {
	return math.Sqrt(args[0].(float64))
}

func ext۰runtime۰Breakpoint(fr *frame, args []value) value {
	runtime.Breakpoint()
	return nil
}

func ext۰sort۰Ints(fr *frame, args []value) value {
	x := args[0].([]value)
	sort.Slice(x, func(i, j int) bool {
		return x[i].(int) < x[j].(int)
	})
	return nil
}
func ext۰sort۰Strings(fr *frame, args []value) value {
	x := args[0].([]value)
	sort.Slice(x, func(i, j int) bool {
		return x[i].(string) < x[j].(string)
	})
	return nil
}
func ext۰sort۰Float64s(fr *frame, args []value) value {
	x := args[0].([]value)
	sort.Slice(x, func(i, j int) bool {
		return x[i].(float64) < x[j].(float64)
	})
	return nil
}

func ext۰strconv۰Atoi(fr *frame, args []value) value {
	i, e := strconv.Atoi(args[0].(string))
	if e != nil {
		if fr.i.runtimeErrorString != nil {
			return tuple{i, iface{fr.i.runtimeErrorString, e.Error()}}
		}
		return tuple{i, e.Error()}
	}
	return tuple{i, iface{}}
}
func ext۰strconv۰Itoa(fr *frame, args []value) value {
	return strconv.Itoa(args[0].(int))
}
func ext۰strconv۰FormatFloat(fr *frame, args []value) value {
	return strconv.FormatFloat(args[0].(float64), args[1].(byte), args[2].(int), args[3].(int))
}

func ext۰strings۰Count(fr *frame, args []value) value {
	return strings.Count(args[0].(string), args[1].(string))
}

func ext۰strings۰EqualFold(fr *frame, args []value) value {
	return strings.EqualFold(args[0].(string), args[1].(string))
}
func ext۰strings۰IndexByte(fr *frame, args []value) value {
	return strings.IndexByte(args[0].(string), args[1].(byte))
}

func ext۰strings۰Index(fr *frame, args []value) value {
	return strings.Index(args[0].(string), args[1].(string))
}

func ext۰strings۰Replace(fr *frame, args []value) value {
	// func Replace(s, old, new string, n int) string
	s := args[0].(string)
	new := args[1].(string)
	old := args[2].(string)
	n := args[3].(int)
	return strings.Replace(s, old, new, n)
}

func ext۰strings۰ToLower(fr *frame, args []value) value {
	return strings.ToLower(args[0].(string))
}

func ext۰runtime۰GOMAXPROCS(fr *frame, args []value) value {
	// Ignore args[0]; don't let the interpreted program
	// set the interpreter's GOMAXPROCS!
	return runtime.GOMAXPROCS(0)
}

func ext۰runtime۰Goexit(fr *frame, args []value) value {
	// TODO(adonovan): don't kill the interpreter's main goroutine.
	runtime.Goexit()
	return nil
}

func ext۰runtime۰GOROOT(fr *frame, args []value) value {
	return runtime.GOROOT()
}

func ext۰runtime۰GC(fr *frame, args []value) value {
	runtime.GC()
	return nil
}

func ext۰runtime۰Gosched(fr *frame, args []value) value {
	runtime.Gosched()
	return nil
}

func ext۰runtime۰NumCPU(fr *frame, args []value) value {
	return runtime.NumCPU()
}

func ext۰time۰Sleep(fr *frame, args []value) value {
	time.Sleep(time.Duration(args[0].(int64)))
	return nil
}

func ext۰os۰Getenv(fr *frame, args []value) value {
	name := args[0].(string)
	switch name {
	case "GOSSAINTERP":
		return "1"
	}
	return os.Getenv(name)
}

func ext۰os۰Exit(fr *frame, args []value) value {
	panic(exitPanic(args[0].(int)))
}

func ext۰unicode۰utf8۰DecodeRuneInString(fr *frame, args []value) value {
	r, n := utf8.DecodeRuneInString(args[0].(string))
	return tuple{r, n}
}

// A fake function for turning an arbitrary value into a string.
// Handles only the cases needed by the tests.
// Uses same logic as 'print' built-in.
func ext۰fmt۰Sprint(fr *frame, args []value) value {
	buf := new(bytes.Buffer)
	wasStr := false
	for i, arg := range args[0].([]value) {
		x := arg.(iface).v
		_, isStr := x.(string)
		if i > 0 && !wasStr && !isStr {
			buf.WriteByte(' ')
		}
		wasStr = isStr
		buf.WriteString(toString(x))
	}
	return buf.String()
}
