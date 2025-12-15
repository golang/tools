// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package settings

import (
	"fmt"
	"log"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/gopls/internal/protocol"
	"honnef.co/go/tools/analysis/lint"
	"honnef.co/go/tools/quickfix"
	"honnef.co/go/tools/quickfix/qf1001"
	"honnef.co/go/tools/quickfix/qf1002"
	"honnef.co/go/tools/quickfix/qf1003"
	"honnef.co/go/tools/quickfix/qf1004"
	"honnef.co/go/tools/quickfix/qf1005"
	"honnef.co/go/tools/quickfix/qf1006"
	"honnef.co/go/tools/quickfix/qf1007"
	"honnef.co/go/tools/quickfix/qf1008"
	"honnef.co/go/tools/quickfix/qf1009"
	"honnef.co/go/tools/quickfix/qf1010"
	"honnef.co/go/tools/quickfix/qf1011"
	"honnef.co/go/tools/quickfix/qf1012"
	"honnef.co/go/tools/simple"
	"honnef.co/go/tools/simple/s1000"
	"honnef.co/go/tools/simple/s1001"
	"honnef.co/go/tools/simple/s1002"
	"honnef.co/go/tools/simple/s1003"
	"honnef.co/go/tools/simple/s1004"
	"honnef.co/go/tools/simple/s1005"
	"honnef.co/go/tools/simple/s1006"
	"honnef.co/go/tools/simple/s1007"
	"honnef.co/go/tools/simple/s1008"
	"honnef.co/go/tools/simple/s1009"
	"honnef.co/go/tools/simple/s1010"
	"honnef.co/go/tools/simple/s1011"
	"honnef.co/go/tools/simple/s1012"
	"honnef.co/go/tools/simple/s1016"
	"honnef.co/go/tools/simple/s1017"
	"honnef.co/go/tools/simple/s1018"
	"honnef.co/go/tools/simple/s1019"
	"honnef.co/go/tools/simple/s1020"
	"honnef.co/go/tools/simple/s1021"
	"honnef.co/go/tools/simple/s1023"
	"honnef.co/go/tools/simple/s1024"
	"honnef.co/go/tools/simple/s1025"
	"honnef.co/go/tools/simple/s1028"
	"honnef.co/go/tools/simple/s1029"
	"honnef.co/go/tools/simple/s1030"
	"honnef.co/go/tools/simple/s1031"
	"honnef.co/go/tools/simple/s1032"
	"honnef.co/go/tools/simple/s1033"
	"honnef.co/go/tools/simple/s1034"
	"honnef.co/go/tools/simple/s1035"
	"honnef.co/go/tools/simple/s1036"
	"honnef.co/go/tools/simple/s1037"
	"honnef.co/go/tools/simple/s1038"
	"honnef.co/go/tools/simple/s1039"
	"honnef.co/go/tools/simple/s1040"
	"honnef.co/go/tools/staticcheck"
	"honnef.co/go/tools/staticcheck/sa1000"
	"honnef.co/go/tools/staticcheck/sa1001"
	"honnef.co/go/tools/staticcheck/sa1002"
	"honnef.co/go/tools/staticcheck/sa1003"
	"honnef.co/go/tools/staticcheck/sa1004"
	"honnef.co/go/tools/staticcheck/sa1005"
	"honnef.co/go/tools/staticcheck/sa1006"
	"honnef.co/go/tools/staticcheck/sa1007"
	"honnef.co/go/tools/staticcheck/sa1008"
	"honnef.co/go/tools/staticcheck/sa1010"
	"honnef.co/go/tools/staticcheck/sa1011"
	"honnef.co/go/tools/staticcheck/sa1012"
	"honnef.co/go/tools/staticcheck/sa1013"
	"honnef.co/go/tools/staticcheck/sa1014"
	"honnef.co/go/tools/staticcheck/sa1015"
	"honnef.co/go/tools/staticcheck/sa1016"
	"honnef.co/go/tools/staticcheck/sa1017"
	"honnef.co/go/tools/staticcheck/sa1018"
	"honnef.co/go/tools/staticcheck/sa1019"
	"honnef.co/go/tools/staticcheck/sa1020"
	"honnef.co/go/tools/staticcheck/sa1021"
	"honnef.co/go/tools/staticcheck/sa1023"
	"honnef.co/go/tools/staticcheck/sa1024"
	"honnef.co/go/tools/staticcheck/sa1025"
	"honnef.co/go/tools/staticcheck/sa1026"
	"honnef.co/go/tools/staticcheck/sa1027"
	"honnef.co/go/tools/staticcheck/sa1028"
	"honnef.co/go/tools/staticcheck/sa1029"
	"honnef.co/go/tools/staticcheck/sa1030"
	"honnef.co/go/tools/staticcheck/sa1031"
	"honnef.co/go/tools/staticcheck/sa1032"
	"honnef.co/go/tools/staticcheck/sa2000"
	"honnef.co/go/tools/staticcheck/sa2001"
	"honnef.co/go/tools/staticcheck/sa2002"
	"honnef.co/go/tools/staticcheck/sa2003"
	"honnef.co/go/tools/staticcheck/sa3000"
	"honnef.co/go/tools/staticcheck/sa3001"
	"honnef.co/go/tools/staticcheck/sa4000"
	"honnef.co/go/tools/staticcheck/sa4001"
	"honnef.co/go/tools/staticcheck/sa4003"
	"honnef.co/go/tools/staticcheck/sa4004"
	"honnef.co/go/tools/staticcheck/sa4005"
	"honnef.co/go/tools/staticcheck/sa4006"
	"honnef.co/go/tools/staticcheck/sa4008"
	"honnef.co/go/tools/staticcheck/sa4009"
	"honnef.co/go/tools/staticcheck/sa4010"
	"honnef.co/go/tools/staticcheck/sa4011"
	"honnef.co/go/tools/staticcheck/sa4012"
	"honnef.co/go/tools/staticcheck/sa4013"
	"honnef.co/go/tools/staticcheck/sa4014"
	"honnef.co/go/tools/staticcheck/sa4015"
	"honnef.co/go/tools/staticcheck/sa4016"
	"honnef.co/go/tools/staticcheck/sa4017"
	"honnef.co/go/tools/staticcheck/sa4018"
	"honnef.co/go/tools/staticcheck/sa4019"
	"honnef.co/go/tools/staticcheck/sa4020"
	"honnef.co/go/tools/staticcheck/sa4021"
	"honnef.co/go/tools/staticcheck/sa4022"
	"honnef.co/go/tools/staticcheck/sa4023"
	"honnef.co/go/tools/staticcheck/sa4024"
	"honnef.co/go/tools/staticcheck/sa4025"
	"honnef.co/go/tools/staticcheck/sa4026"
	"honnef.co/go/tools/staticcheck/sa4027"
	"honnef.co/go/tools/staticcheck/sa4028"
	"honnef.co/go/tools/staticcheck/sa4029"
	"honnef.co/go/tools/staticcheck/sa4030"
	"honnef.co/go/tools/staticcheck/sa4031"
	"honnef.co/go/tools/staticcheck/sa4032"
	"honnef.co/go/tools/staticcheck/sa5000"
	"honnef.co/go/tools/staticcheck/sa5001"
	"honnef.co/go/tools/staticcheck/sa5002"
	"honnef.co/go/tools/staticcheck/sa5003"
	"honnef.co/go/tools/staticcheck/sa5004"
	"honnef.co/go/tools/staticcheck/sa5005"
	"honnef.co/go/tools/staticcheck/sa5007"
	"honnef.co/go/tools/staticcheck/sa5008"
	"honnef.co/go/tools/staticcheck/sa5009"
	"honnef.co/go/tools/staticcheck/sa5010"
	"honnef.co/go/tools/staticcheck/sa5011"
	"honnef.co/go/tools/staticcheck/sa5012"
	"honnef.co/go/tools/staticcheck/sa6000"
	"honnef.co/go/tools/staticcheck/sa6001"
	"honnef.co/go/tools/staticcheck/sa6002"
	"honnef.co/go/tools/staticcheck/sa6003"
	"honnef.co/go/tools/staticcheck/sa6005"
	"honnef.co/go/tools/staticcheck/sa6006"
	"honnef.co/go/tools/staticcheck/sa9001"
	"honnef.co/go/tools/staticcheck/sa9002"
	"honnef.co/go/tools/staticcheck/sa9003"
	"honnef.co/go/tools/staticcheck/sa9004"
	"honnef.co/go/tools/staticcheck/sa9005"
	"honnef.co/go/tools/staticcheck/sa9006"
	"honnef.co/go/tools/staticcheck/sa9007"
	"honnef.co/go/tools/staticcheck/sa9008"
	"honnef.co/go/tools/staticcheck/sa9009"
	"honnef.co/go/tools/stylecheck"
	"honnef.co/go/tools/stylecheck/st1000"
	"honnef.co/go/tools/stylecheck/st1001"
	"honnef.co/go/tools/stylecheck/st1003"
	"honnef.co/go/tools/stylecheck/st1005"
	"honnef.co/go/tools/stylecheck/st1006"
	"honnef.co/go/tools/stylecheck/st1008"
	"honnef.co/go/tools/stylecheck/st1011"
	"honnef.co/go/tools/stylecheck/st1012"
	"honnef.co/go/tools/stylecheck/st1013"
	"honnef.co/go/tools/stylecheck/st1015"
	"honnef.co/go/tools/stylecheck/st1016"
	"honnef.co/go/tools/stylecheck/st1017"
	"honnef.co/go/tools/stylecheck/st1018"
	"honnef.co/go/tools/stylecheck/st1019"
	"honnef.co/go/tools/stylecheck/st1020"
	"honnef.co/go/tools/stylecheck/st1021"
	"honnef.co/go/tools/stylecheck/st1022"
	"honnef.co/go/tools/stylecheck/st1023"
)

// StaticcheckAnalyzers lists available Staticcheck analyzers.
var StaticcheckAnalyzers = initStaticcheckAnalyzers()

func initStaticcheckAnalyzers() (res []*Analyzer) {

	mapSeverity := func(severity lint.Severity) protocol.DiagnosticSeverity {
		switch severity {
		case lint.SeverityError:
			return protocol.SeverityError
		case lint.SeverityDeprecated:
			// TODO(dh): in LSP, deprecated is a tag, not a severity.
			//   We'll want to support this once we enable SA5011.
			return protocol.SeverityWarning
		case lint.SeverityWarning:
			return protocol.SeverityWarning
		case lint.SeverityInfo:
			return protocol.SeverityInformation
		case lint.SeverityHint:
			return protocol.SeverityHint
		default:
			return protocol.SeverityWarning
		}
	}

	// We can't import buildir.Analyzer directly, so grab it from another analyzer.
	buildir := sa1000.SCAnalyzer.Analyzer.Requires[0]
	if buildir.Name != "buildir" {
		panic("sa1000.Requires[0] is not buildir")
	}

	add := func(a *lint.Analyzer, dflt bool) {
		// Assert that no analyzer that requires "buildir",
		// even indirectly, is enabled by default.
		if dflt {
			var visit func(aa *analysis.Analyzer)
			visit = func(aa *analysis.Analyzer) {
				if aa == buildir {
					log.Fatalf("%s requires buildir (perhaps indirectly) yet is enabled by default", a.Analyzer.Name)
				}
				for _, req := range aa.Requires {
					visit(req)
				}
			}
			visit(a.Analyzer)
		}
		res = append(res, &Analyzer{
			analyzer:    a.Analyzer,
			staticcheck: a.Doc,
			nonDefault:  !dflt,
			severity:    mapSeverity(a.Doc.Severity),
		})
	}

	type M = map[*lint.Analyzer]any // value = true|false|nil

	addAll := func(suite string, upstream []*lint.Analyzer, config M) {
		for _, a := range upstream {
			v, ok := config[a]
			if !ok {
				panic(fmt.Sprintf("%s.Analyzers includes %s but config mapping does not; settings audit required", suite, a.Analyzer.Name))
			}
			if v != nil {
				add(a, v.(bool))
			}
		}
	}

	// For each analyzer in the four suites provided by
	// staticcheck, we provide a complete configuration, mapping
	// it to a boolean, indicating whether it should be on by
	// default in gopls, or nil to indicate explicitly that it has
	// been excluded (e.g. because it is redundant with an
	// existing vet analyzer such as printf, waitgroup, appends).
	//
	// This approach ensures that as suites grow, we make an
	// affirmative decision, positive or negative, about adding
	// new items.
	//
	// An analyzer may be off by default if:
	// - it requires, even indirectly, "buildir", which is like
	//   buildssa but uses facts, making it expensive;
	// - it has significant false positives;
	// - it reports on non-problematic style issues;
	// - its fixes are lossy (e.g. of comments) or not always sound;
	// - it reports "maybes", not "definites" (e.g. sa9001).
	// - it reports on harmless stylistic choices that may have
	//   been chosen deliberately for clarity or emphasis (e.g. s1005).
	// - it makes deductions from build tags that are not true
	//   for all configurations.

	addAll("simple", simple.Analyzers, M{
		s1000.SCAnalyzer: true,
		s1001.SCAnalyzer: true,
		s1002.SCAnalyzer: false, // makes unsound deductions from build tags
		s1003.SCAnalyzer: true,
		s1004.SCAnalyzer: true,
		s1005.SCAnalyzer: false, // not a correctness/style issue
		s1006.SCAnalyzer: false, // makes unsound deductions from build tags
		s1007.SCAnalyzer: true,
		s1008.SCAnalyzer: false, // may lose important comments
		s1009.SCAnalyzer: true,
		s1010.SCAnalyzer: true,
		s1011.SCAnalyzer: false, // requires buildir
		s1012.SCAnalyzer: true,
		s1016.SCAnalyzer: false, // may rely on coincidental structural subtyping
		s1017.SCAnalyzer: true,
		s1018.SCAnalyzer: true,
		s1019.SCAnalyzer: true,
		s1020.SCAnalyzer: true,
		s1021.SCAnalyzer: false, // may lose important comments
		s1023.SCAnalyzer: true,
		s1024.SCAnalyzer: true,
		s1025.SCAnalyzer: false, // requires buildir
		s1028.SCAnalyzer: true,
		s1029.SCAnalyzer: false, // requires buildir
		s1030.SCAnalyzer: true,  // (tentative: see docs,
		s1031.SCAnalyzer: true,
		s1032.SCAnalyzer: true,
		s1033.SCAnalyzer: true,
		s1034.SCAnalyzer: true,
		s1035.SCAnalyzer: true,
		s1036.SCAnalyzer: true,
		s1037.SCAnalyzer: true,
		s1038.SCAnalyzer: true,
		s1039.SCAnalyzer: true,
		s1040.SCAnalyzer: true,
	})

	addAll("stylecheck", stylecheck.Analyzers, M{
		// These are all slightly too opinionated to be on by default.
		st1000.SCAnalyzer: false,
		st1001.SCAnalyzer: false,
		st1003.SCAnalyzer: false,
		st1005.SCAnalyzer: false,
		st1006.SCAnalyzer: false,
		st1008.SCAnalyzer: false,
		st1011.SCAnalyzer: false,
		st1012.SCAnalyzer: false,
		st1013.SCAnalyzer: false,
		st1015.SCAnalyzer: false,
		st1016.SCAnalyzer: false,
		st1017.SCAnalyzer: false,
		st1018.SCAnalyzer: false,
		st1019.SCAnalyzer: false,
		st1020.SCAnalyzer: false,
		st1021.SCAnalyzer: false,
		st1022.SCAnalyzer: false,
		st1023.SCAnalyzer: false,
	})

	// These are not bug fixes but code transformations: some
	// reversible and value-neutral, of the kind typically listed
	// on the VS Code's Refactor/Source Action/Quick Fix menus.
	//
	// TODO(adonovan): plumb these to the appropriate menu,
	// as we do for code actions such as split/join lines.
	addAll("quickfix", quickfix.Analyzers, M{
		qf1001.SCAnalyzer: false, // not always a style improvement
		qf1002.SCAnalyzer: true,
		qf1003.SCAnalyzer: true,
		qf1004.SCAnalyzer: true,
		qf1005.SCAnalyzer: false, // not always a style improvement
		qf1006.SCAnalyzer: false, // may lose important comments
		qf1007.SCAnalyzer: false, // may lose important comments
		qf1008.SCAnalyzer: false, // not always a style improvement
		qf1009.SCAnalyzer: true,
		qf1010.SCAnalyzer: true,
		qf1011.SCAnalyzer: false, // not always a style improvement
		qf1012.SCAnalyzer: true,
	})

	addAll("staticcheck", staticcheck.Analyzers, M{
		sa1000.SCAnalyzer: false, // requires buildir
		sa1001.SCAnalyzer: true,
		sa1002.SCAnalyzer: false, // requires buildir
		sa1003.SCAnalyzer: false, // requires buildir
		sa1004.SCAnalyzer: true,
		sa1005.SCAnalyzer: true,
		sa1006.SCAnalyzer: nil,   // redundant wrt 'printf'
		sa1007.SCAnalyzer: false, // requires buildir
		sa1008.SCAnalyzer: true,
		sa1010.SCAnalyzer: false, // requires buildir
		sa1011.SCAnalyzer: false, // requires buildir
		sa1012.SCAnalyzer: true,
		sa1013.SCAnalyzer: true,
		sa1014.SCAnalyzer: false, // requires buildir
		sa1015.SCAnalyzer: false, // requires buildir
		sa1016.SCAnalyzer: true,
		sa1017.SCAnalyzer: false, // requires buildir
		sa1018.SCAnalyzer: false, // requires buildir
		sa1019.SCAnalyzer: nil,   // redundant wrt 'deprecated'
		sa1020.SCAnalyzer: false, // requires buildir
		sa1021.SCAnalyzer: false, // requires buildir
		sa1023.SCAnalyzer: false, // requires buildir
		sa1024.SCAnalyzer: false, // requires buildir
		sa1025.SCAnalyzer: false, // requires buildir
		sa1026.SCAnalyzer: false, // requires buildir
		sa1027.SCAnalyzer: false, // requires buildir
		sa1028.SCAnalyzer: false, // requires buildir
		sa1029.SCAnalyzer: false, // requires buildir
		sa1030.SCAnalyzer: false, // requires buildir
		sa1031.SCAnalyzer: false, // requires buildir
		sa1032.SCAnalyzer: false, // requires buildir
		sa2000.SCAnalyzer: nil,   // redundant wrt 'waitgroup'
		sa2001.SCAnalyzer: true,
		sa2002.SCAnalyzer: false, // requires buildir
		sa2003.SCAnalyzer: false, // requires buildir
		sa3000.SCAnalyzer: true,
		sa3001.SCAnalyzer: true,
		sa4000.SCAnalyzer: true,
		sa4001.SCAnalyzer: true,
		sa4003.SCAnalyzer: true,
		sa4004.SCAnalyzer: true,
		sa4005.SCAnalyzer: false, // requires buildir
		sa4006.SCAnalyzer: false, // requires buildir
		sa4008.SCAnalyzer: false, // requires buildir
		sa4009.SCAnalyzer: false, // requires buildir
		sa4010.SCAnalyzer: false, // requires buildir
		sa4011.SCAnalyzer: true,
		sa4012.SCAnalyzer: false, // requires buildir
		sa4013.SCAnalyzer: true,
		sa4014.SCAnalyzer: true,
		sa4015.SCAnalyzer: false, // requires buildir
		sa4016.SCAnalyzer: true,
		sa4017.SCAnalyzer: false, // requires buildir
		sa4018.SCAnalyzer: false, // requires buildir
		sa4019.SCAnalyzer: true,
		sa4020.SCAnalyzer: true,
		sa4021.SCAnalyzer: nil, // redundant wrt 'appends'
		sa4022.SCAnalyzer: true,
		sa4023.SCAnalyzer: false, // requires buildir
		sa4024.SCAnalyzer: true,
		sa4025.SCAnalyzer: true,
		sa4026.SCAnalyzer: true,
		sa4027.SCAnalyzer: true,
		sa4028.SCAnalyzer: true,
		sa4029.SCAnalyzer: true,
		sa4030.SCAnalyzer: true,
		sa4031.SCAnalyzer: false, // requires buildir
		sa4032.SCAnalyzer: true,
		sa5000.SCAnalyzer: false, // requires buildir
		sa5001.SCAnalyzer: true,
		sa5002.SCAnalyzer: false, // makes unsound deductions from build tags
		sa5003.SCAnalyzer: true,
		sa5004.SCAnalyzer: true,
		sa5005.SCAnalyzer: false, // requires buildir
		sa5007.SCAnalyzer: false, // requires buildir
		sa5008.SCAnalyzer: true,
		sa5009.SCAnalyzer: nil,   // requires buildir; redundant wrt 'printf' (#34494)
		sa5010.SCAnalyzer: false, // requires buildir
		sa5011.SCAnalyzer: false, // requires buildir
		sa5012.SCAnalyzer: false, // requires buildir
		sa6000.SCAnalyzer: false, // requires buildir
		sa6001.SCAnalyzer: false, // requires buildir
		sa6002.SCAnalyzer: false, // requires buildir
		sa6003.SCAnalyzer: false, // requires buildir
		sa6005.SCAnalyzer: true,
		sa6006.SCAnalyzer: true,
		sa9001.SCAnalyzer: false, // reports a "maybe" bug (low signal/noise)
		sa9002.SCAnalyzer: true,
		sa9003.SCAnalyzer: false, // requires buildir; NonDefault
		sa9004.SCAnalyzer: true,
		sa9005.SCAnalyzer: false, // requires buildir
		sa9006.SCAnalyzer: true,
		sa9007.SCAnalyzer: false, // requires buildir
		sa9008.SCAnalyzer: false, // requires buildir
		sa9009.SCAnalyzer: true,
	})

	return res
}
