// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"reflect"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"slices"
	"strings"
	"time"

	"golang.org/x/tools/gopls/internal/debug"
	"golang.org/x/tools/gopls/internal/filecache"
)

// This file defines common flags and helper functions
// that coordinate flag registration via reflection.

// ProfileFlags can be embedded in your application struct to automatically
// add command line arguments and handling for common profiling methods.
type ProfileFlags struct {
	CPU    string `flag:"profile.cpu" help:"write CPU profile to this file"`
	Memory string `flag:"profile.mem" help:"write memory profile to this file"`
	Alloc  string `flag:"profile.alloc" help:"write alloc profile to this file"`
	Trace  string `flag:"profile.trace" help:"write trace log to this file"`
	Block  string `flag:"profile.block" help:"write block profile to this file"`
}

// command represents an executable CLI command or subcommand within gopls.
type command interface {
	// Name returns the command's name. It is used in help and error messages.
	Name() string
	// Most of the help usage is automatically generated, this string should only
	// describe the contents of non flag arguments.
	Usage() string
	// ShortHelp returns the one line overview of the command.
	ShortHelp() string
	// DetailedHelp should print a detailed help message. It will only ever be shown
	// when the ShortHelp is also printed, so there is no need to duplicate
	// anything from there.
	// It is passed the flag set so it can print the default values of the flags.
	// It should use the flag sets configured Output to write the help to.
	DetailedHelp(*flag.FlagSet)
	// Run is invoked after all flag processing, and inside the profiling and
	// error handling harness.
	Run(ctx context.Context, args ...string) error
}

type subcommand interface {
	// TODO(hyangah): merge with command. It is unclear why we need
	// to keep command and subcommand separate.

	command
	Parent() string
}

// This is the type returned by commandLineErrorf, which causes the outer main
// to trigger printing of the command line help.
type commandLineError string

func (e commandLineError) Error() string { return string(e) }

// commandLineErrorf is like fmt.Errorf except that it returns a value that
// triggers printing of the command line help.
// In general you should use this when generating command line validation errors.
func commandLineErrorf(message string, args ...any) error {
	return commandLineError(fmt.Sprintf(message, args...))
}

// Main is the main entry point for the gopls application, called by gopls main.
// It never returns.
func Main() {
	ctx := context.Background()
	args := os.Args[1:]
	app := newApplication()
	cmd, globalArgs, cmdArgs, err := normalize(app, args)
	if err != nil {
		if cmd == nil {
			cmd = app
		}
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmdPath(cmd), err)
		if isCommandLineError(err) {
			printCommandHelp(os.Stderr, cmd)
		}
		os.Exit(2)
	}

	parseFlags(app, globalArgs)
	cmdFlags := parseFlags(cmd, cmdArgs)

	err = runWithProfile(&app.ProfileFlags, func() error {
		// In the category of "things we can do while waiting for the
		// Go command":

		// TODO(hyangah): check if it's desirable to run filecache.Start unconditionally
		// on every gopls subcommand, including CLI running with -remote or -help message.
		// Pre-initialize the filecache, which takes ~50ms to hash the gopls
		// executable, and immediately runs a gc.
		filecache.Start()

		ctx = debug.WithInstance(ctx, app.OTel)

		return cmd.Run(ctx, cmdFlags.Args()...)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gopls: %v\n", err)
		if isCommandLineError(err) {
			printCommandHelp(os.Stderr, cmd)
		}
		os.Exit(2)
	}
	os.Exit(0)
}

// isCommandLineError reports whether the error was created by [commandLineErrorf].
func isCommandLineError(err error) bool {
	_, ok := err.(commandLineError)
	return ok
}

// cmdPath returns the full command path (e.g. "gopls remote debug") for target.
func cmdPath(target command) string {
	if sub, ok := target.(subcommand); ok && sub.Parent() != "" {
		return sub.Parent() + " " + target.Name()
	}
	return target.Name()
}

// printHelp prints the usage and detailed help for any command to s.Output().
func printHelp(s *flag.FlagSet, cmd command) {
	if _, ok := cmd.(*application); !ok {
		printCommandHelp(s.Output(), cmd)
	}
	cmd.DetailedHelp(s)
}

// printCommandHelp prints a concise usage summary for cmd to w.
func printCommandHelp(w io.Writer, cmd command) {
	if _, ok := cmd.(*application); ok {
		fmt.Fprintln(w, "Usage:\n  gopls help [<subject>]")
		return
	}
	if short := cmd.ShortHelp(); short != "" {
		fmt.Fprintf(w, "%s\n\n", short)
	}
	fmt.Fprintf(w, "Usage:\n  gopls [flags] %s", strings.TrimPrefix(cmdPath(cmd), "gopls "))
	if usage := cmd.Usage(); usage != "" {
		fmt.Fprintf(w, " %s", usage)
	}
	fmt.Fprintln(w)
}

// runWithProfile executes fn with active CPU, trace, memory, alloc, or block profiling
// if requested in p.
func runWithProfile(p *ProfileFlags, fn func() error) (resultErr error) {
	if p.CPU != "" {
		f, err := os.Create(p.CPU)
		if err != nil {
			return err
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			f.Close() // ignore error
			return err
		}
		defer func() {
			pprof.StopCPUProfile()
			if closeErr := f.Close(); resultErr == nil {
				resultErr = closeErr
			}
		}()
	}

	if p.Trace != "" {
		f, err := os.Create(p.Trace)
		if err != nil {
			return err
		}
		if err := trace.Start(f); err != nil {
			f.Close() // ignore error
			return err
		}
		defer func() {
			trace.Stop()
			if closeErr := f.Close(); resultErr == nil {
				resultErr = closeErr
			}
			log.Printf("To view the trace, run:\n$ go tool trace view %s", p.Trace)
		}()
	}

	if p.Memory != "" {
		f, err := os.Create(p.Memory)
		if err != nil {
			return err
		}
		defer func() {
			runtime.GC() // get up-to-date statistics
			if err := pprof.WriteHeapProfile(f); err != nil {
				log.Printf("Writing memory profile: %v", err)
			}
			if err := f.Close(); err != nil {
				log.Printf("Closing memory profile: %v", err)
			}
		}()
	}

	if p.Alloc != "" {
		f, err := os.Create(p.Alloc)
		if err != nil {
			return err
		}
		defer func() {
			if err := pprof.Lookup("allocs").WriteTo(f, 0); err != nil {
				log.Printf("Writing alloc profile: %v", err)
			}
			if err := f.Close(); err != nil {
				log.Printf("Closing alloc profile: %v", err)
			}
		}()
	}

	if p.Block != "" {
		f, err := os.Create(p.Block)
		if err != nil {
			return err
		}
		runtime.SetBlockProfileRate(1) // record all blocking events
		defer func() {
			if err := pprof.Lookup("block").WriteTo(f, 0); err != nil {
				log.Printf("Writing block profile: %v", err)
			}
			if err := f.Close(); err != nil {
				log.Printf("Closing block profile: %v", err)
			}
		}()
	}
	return fn()
}

// addFlags scans fields of structs recursively to find things with flag tags
// and add them to the flag set.
func addFlags(f *flag.FlagSet, field reflect.StructField, value reflect.Value) *ProfileFlags {
	// is it a field we are allowed to reflect on?
	if field.PkgPath != "" {
		return nil
	}
	// now see if is actually a flag
	flagNames, isFlag := field.Tag.Lookup("flag")
	help := field.Tag.Get("help")
	if isFlag {
		nameList := strings.Split(flagNames, ",")
		// add the main flag
		addFlag(f, value, nameList[0], help)
		if len(nameList) > 1 {
			// and now add any aliases using the same flag value
			fv := f.Lookup(nameList[0]).Value
			for _, flagName := range nameList[1:] {
				f.Var(fv, flagName, help)
			}
		}
		return nil
	}
	// not a flag, but it might be a struct with flags in it
	value = resolve(value.Elem())
	if value.Kind() != reflect.Struct {
		return nil
	}

	// TODO(adonovan): there's no need for this special treatment of Profile:
	// The caller can use f.Lookup("profile.cpu") etc instead.
	p, _ := value.Addr().Interface().(*ProfileFlags)
	// go through all the fields of the struct
	for i := 0; i < value.Type().NumField(); i++ {
		child := value.Type().Field(i)
		v := value.Field(i)
		// make sure we have a pointer
		if v.Kind() != reflect.Pointer {
			v = v.Addr()
		}
		// check if that field is a flag or contains flags
		if fp := addFlags(f, child, v); fp != nil {
			p = fp
		}
	}
	return p
}

func addFlag(f *flag.FlagSet, value reflect.Value, flagName string, help string) {
	switch v := value.Interface().(type) {
	case flag.Value:
		f.Var(v, flagName, help)
	case *bool:
		f.BoolVar(v, flagName, *v, help)
	case *time.Duration:
		f.DurationVar(v, flagName, *v, help)
	case *float64:
		f.Float64Var(v, flagName, *v, help)
	case *int64:
		f.Int64Var(v, flagName, *v, help)
	case *int:
		f.IntVar(v, flagName, *v, help)
	case *string:
		f.StringVar(v, flagName, *v, help)
	case *uint:
		f.UintVar(v, flagName, *v, help)
	case *uint64:
		f.Uint64Var(v, flagName, *v, help)
	default:
		log.Fatalf("field %q of type %T is not assignable to flag.Value", flagName, v)
	}
}

func resolve(v reflect.Value) reflect.Value {
	for {
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			v = v.Elem()
		default:
			return v
		}
	}
}

// parseFlags creates, configures, and parses a FlagSet for cmd using args.
// If parsing fails or help is requested, it prints contextual help and exits.
func parseFlags(cmd command, args []string) *flag.FlagSet {
	// We use ContinueOnError and discard initial error output so we can intercept flag errors
	// and produce contextual, user-friendly diagnostic messages rather than standard Go flag usage.
	fs := flag.NewFlagSet(cmd.Name(), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addCommandFlags(fs, cmd)
	err := fs.Parse(args)
	if err == nil {
		return fs
	}

	if err == flag.ErrHelp {
		// POSIX convention requires writing explicit help requests
		// (-h/-help) to stdout on exit 0.
		fs.SetOutput(os.Stdout)
		printHelp(fs, cmd)
		os.Exit(0)
	}

	fs.SetOutput(os.Stderr)
	// When standard flag parsing fails due to an undefined flag,
	// inspect command hierarchy so we can guide the user
	// if they misplaced a flag before or after a subcommand.
	if prefix := "flag provided but not defined: -"; strings.HasPrefix(err.Error(), prefix) {
		checkMisplacedFlag(fs, cmd, strings.TrimPrefix(err.Error(), prefix))
	}

	// Fallback diagnostic for general flag syntax errors
	// or truly unknown flags.
	fmt.Fprintf(os.Stderr, "%s: %v\n", cmdPath(cmd), err)
	printCommandHelp(os.Stderr, cmd)
	os.Exit(2)
	return nil
}

// findCommandByName searches the command tree starting from root for a command named name.
func findCommandByName(root command, name string) command {
	if root.Name() == name {
		return root
	}
	for _, sub := range getSubcommands(root) {
		if sub.Name() == name {
			return sub
		}
		if found := findCommandByName(sub, name); found != nil {
			return found
		}
	}
	return nil
}

// checkMisplacedFlag inspects ancestors and descendants to diagnose undefined flag errors.
// If a misplaced flag is found, it prints where the flag belongs and exits with code 2.
func checkMisplacedFlag(fs *flag.FlagSet, cmd command, name string) {
	// Check descendants: e.g. placing a subcommand flag before specifying the subcommand.
	if sub := findSubcommandWithFlag(cmd, name); sub != nil {
		fmt.Fprintf(os.Stderr, "%s: flag -%s belongs to subcommand %s\n", cmdPath(cmd), name, sub.Name())
		printCommandHelp(fs.Output(), cmd)
		os.Exit(2)
	}

	// Walk up ancestors via lineage string: e.g. placing a global application flag after the subcommand name.
	// Strict flag ordering requires parent/global flags to precede subcommands.
	if sub, ok := cmd.(subcommand); ok && sub.Parent() != "" {
		root := newApplication()
		for _, currName := range slices.Backward(strings.Fields(sub.Parent())) {
			curr := findCommandByName(root, currName)
			if curr != nil && hasFlag(curr, name) {
				fmt.Fprintf(os.Stderr, "%s: flag -%s must be placed before subcommand %s (after %s)\n", cmdPath(cmd), name, cmd.Name(), currName)
				printCommandHelp(fs.Output(), cmd)
				os.Exit(2)
			}
		}
	}
}

// findSubcommandWithFlag recursively searches getSubcommands(target) to check
// if flagName is registered on any child or descendant subcommand.
func findSubcommandWithFlag(target command, flagName string) command {
	for _, sub := range getSubcommands(target) {
		if hasFlag(sub, flagName) {
			return sub
		}
		if found := findSubcommandWithFlag(sub, flagName); found != nil {
			return found
		}
	}
	return nil
}

// hasFlag reports whether flagName is registered on cmd.
func hasFlag(cmd command, flagName string) bool {
	fs := flag.NewFlagSet(cmd.Name(), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addCommandFlags(fs, cmd)
	return fs.Lookup(flagName) != nil
}

// addCommandFlags registers the flags defined in the app struct onto the FlagSet.
func addCommandFlags(f *flag.FlagSet, app command) *ProfileFlags {
	return addFlags(f, reflect.StructField{}, reflect.ValueOf(app))
}
