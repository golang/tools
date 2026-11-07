// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"flag"
	"io"
	"strings"
)

// normalize scans command-line arguments to find the top-level subcommand,
// separating global application flags from subcommand arguments without executing FlagSet.Parse.
//
// Returns:
//   - cmd: the resolved target subcommand (or &app.serve if none is specified).
//     If flag validation errors, cmd is returned populated so callers can display contextual command help.
//   - globalArgs: flags belonging to global application scope.
//   - cmdArgs: arguments and flags belonging to cmd.
func normalize(app *application, args []string) (cmd command, globalArgs, cmdArgs []string, err error) {
	silentFlagSet := func(name string, cmds ...command) *flag.FlagSet {
		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		for _, c := range cmds {
			addCommandFlags(fs, c)
		}
		return fs
	}

	findSubcommand := func(curr command, name string) command {
		for _, c := range getSubcommands(curr) {
			if c.Name() == name {
				return c
			}
		}
		return nil
	}

	// Flag sets to be used for flag name/type lookup.
	appFlagSet := silentFlagSet(app.Name(), app)
	serveFlagSet := silentFlagSet("serve", &app.serve)

	var preServeArgs []string

	i := 0
	for i < len(args) {
		arg := args[i]

		if arg == "--" {
			// e.g. gopls -v -- check file.go
			//      gopls vulncheck -- -mode=...
			if i+1 < len(args) {
				if matched := findSubcommand(app, args[i+1]); matched != nil {
					cmd = matched
					i++ // skip "--"
					cmdArgs = append(cmdArgs, args[i+1:]...)
					break
				}
			}
			i++
			cmd = &app.serve
			cmdArgs = append(cmdArgs, args[i:]...)
			break
		}

		// expect a valid subcommand if not a flag.
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			if matched := findSubcommand(app, arg); matched != nil {
				cmd = matched
				i++
				cmdArgs = append(cmdArgs, args[i:]...)
				break
			}
			break
		}

		// below: arg is a string that has "-" as the prefix.
		cleanArg := strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-")
		name, _, hasValue := strings.Cut(cleanArg, "=")

		if name == "h" || name == "help" { // -h or -help
			globalArgs = append(globalArgs, arg)
			i++
			continue
		}

		if appFlag := appFlagSet.Lookup(name); appFlag != nil {
			consumed, err := consume(args, i, appFlag, name, hasValue)
			if err != nil {
				return nil, nil, nil, err
			}
			globalArgs = append(globalArgs, consumed...)
			i += len(consumed)
			continue
		}
		if serveFlag := serveFlagSet.Lookup(name); serveFlag != nil {
			consumed, err := consume(args, i, serveFlag, name, hasValue)
			if err != nil {
				return nil, nil, nil, err
			}
			preServeArgs = append(preServeArgs, consumed...)
			i += len(consumed)
			continue
		}
		return nil, nil, nil, commandLineErrorf("unknown flag: %s", arg)
	}

	if cmd == nil {
		if i < len(args) {
			return nil, nil, nil, commandLineErrorf("unknown command %q", args[i])
		}
		cmd = &app.serve
		cmdArgs = preServeArgs
	} else if cmd.Name() == "serve" {
		// For backwards compatibility, allow flags to be placed after serve.
		cmdArgs = append(preServeArgs, cmdArgs...)
	} else if len(preServeArgs) > 0 {
		// All explicitly specified subcommands other than serve must
		// follow strict flag ordering.
		arg := preServeArgs[0]
		cleanArg := strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-")
		name, _, _ := strings.Cut(cleanArg, "=")
		if hasFlag(cmd, name) {
			return cmd, nil, nil, commandLineErrorf("flag -%s must be placed after the command %s", name, cmd.Name())
		}
		if sub := findSubcommandWithFlag(app, name); sub != nil {
			return cmd, nil, nil, commandLineErrorf("flag -%s belongs to subcommand %s", name, sub.Name())
		}

		return cmd, nil, nil, commandLineErrorf("flag provided but not defined: -%s", name)
	}

	return cmd, globalArgs, cmdArgs, nil
}

// consume returns the token(s) corresponding to flag f from args starting at index i.
func consume(args []string, i int, f *flag.Flag, name string, hasValue bool) ([]string, error) {
	if hasValue || isBoolFlag(f) {
		return args[i : i+1], nil
	}
	if i+1 >= len(args) {
		return nil, commandLineErrorf("flag needs an argument: -%s", name)
	}
	return args[i : i+2], nil
}

// isBoolFlag reports whether f is a boolean flag.
func isBoolFlag(f *flag.Flag) bool {
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && bf.IsBoolFlag()
}
