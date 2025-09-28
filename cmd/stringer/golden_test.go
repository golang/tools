// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains simple golden tests for various examples.
// Besides validating the results when the implementation changes,
// it provides a way to look at the generated code without having
// to execute the print statements in one's head.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/internal/testenv"
)

// Golden represents a test case.
type Golden struct {
	name        string
	trimPrefix  string
	lineComment bool
	input       string // input; the package clause is provided when running the test.
	output      string // expected output.
}

var golden = []Golden{
	{"day", "", false, day_in, day_out},
	{"offset", "", false, offset_in, offset_out},
	{"gap", "", false, gap_in, gap_out},
	{"num", "", false, num_in, num_out},
	{"unum", "", false, unum_in, unum_out},
	{"unumpos", "", false, unumpos_in, unumpos_out},
	{"prime", "", false, prime_in, prime_out},
	{"prefix", "Type", false, prefix_in, prefix_out},
	{"tokens", "", true, tokens_in, tokens_out},
	{"overflow8", "", false, overflow8_in, overflow8_out},
}

// Each example starts with "type XXX [u]int", with a single space separating them.

// Simple test: enumeration of type int starting at 0.
const day_in = `type Day int
const (
	Monday Day = iota
	Tuesday
	Wednesday
	Thursday
	Friday
	Saturday
	Sunday
)
`

const day_out = `func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[Monday-0]
	_ = x[Tuesday-1]
	_ = x[Wednesday-2]
	_ = x[Thursday-3]
	_ = x[Friday-4]
	_ = x[Saturday-5]
	_ = x[Sunday-6]
}

const _Day_name = "MondayTuesdayWednesdayThursdayFridaySaturdaySunday"

var _Day_index = [...]uint8{0, 6, 13, 22, 30, 36, 44, 50}

func (i Day) String() string {
	idx := int(i) - 0
	if i < 0 || idx >= len(_Day_index)-1 {
		return "Day(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Day_name[_Day_index[idx]:_Day_index[idx+1]]
}
`

// Enumeration with an offset.
// Also includes a duplicate.
const offset_in = `type Number int
const (
	_ Number = iota
	One
	Two
	Three
	AnotherOne = One  // Duplicate; note that AnotherOne doesn't appear below.
)
`

const offset_out = `func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[One-1]
	_ = x[Two-2]
	_ = x[Three-3]
}

const _Number_name = "OneTwoThree"

var _Number_index = [...]uint8{0, 3, 6, 11}

func (i Number) String() string {
	idx := int(i) - 1
	if i < 1 || idx >= len(_Number_index)-1 {
		return "Number(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Number_name[_Number_index[idx]:_Number_index[idx+1]]
}
`

// Gaps and an offset.
const gap_in = `type Gap int
const (
	Two Gap = 2
	Three Gap = 3
	Five Gap = 5
	Six Gap = 6
	Seven Gap = 7
	Eight Gap = 8
	Nine Gap = 9
	Eleven Gap = 11
)
`

const gap_out = `func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[Two-2]
	_ = x[Three-3]
	_ = x[Five-5]
	_ = x[Six-6]
	_ = x[Seven-7]
	_ = x[Eight-8]
	_ = x[Nine-9]
	_ = x[Eleven-11]
}

const (
	_Gap_name_0 = "TwoThree"
	_Gap_name_1 = "FiveSixSevenEightNine"
	_Gap_name_2 = "Eleven"
)

var (
	_Gap_index_0 = [...]uint8{0, 3, 8}
	_Gap_index_1 = [...]uint8{0, 4, 7, 12, 17, 21}
)

func (i Gap) String() string {
	switch {
	case 2 <= i && i <= 3:
		i -= 2
		return _Gap_name_0[_Gap_index_0[i]:_Gap_index_0[i+1]]
	case 5 <= i && i <= 9:
		i -= 5
		return _Gap_name_1[_Gap_index_1[i]:_Gap_index_1[i+1]]
	case i == 11:
		return _Gap_name_2
	default:
		return "Gap(" + strconv.FormatInt(int64(i), 10) + ")"
	}
}
`

// Signed integers spanning zero.
const num_in = `type Num int
const (
	m_2 Num = -2 + iota
	m_1
	m0
	m1
	m2
)
`

const num_out = `func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[m_2 - -2]
	_ = x[m_1 - -1]
	_ = x[m0-0]
	_ = x[m1-1]
	_ = x[m2-2]
}

const _Num_name = "m_2m_1m0m1m2"

var _Num_index = [...]uint8{0, 3, 6, 8, 10, 12}

func (i Num) String() string {
	idx := int(i) - -2
	if i < -2 || idx >= len(_Num_index)-1 {
		return "Num(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Num_name[_Num_index[idx]:_Num_index[idx+1]]
}
`

// Unsigned integers spanning zero.
const unum_in = `type Unum uint
const (
	m_2 Unum = iota + 253
	m_1
)

const (
	m0 Unum = iota
	m1
	m2
)
`

const unum_out = `func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[m_2-253]
	_ = x[m_1-254]
	_ = x[m0-0]
	_ = x[m1-1]
	_ = x[m2-2]
}

const (
	_Unum_name_0 = "m0m1m2"
	_Unum_name_1 = "m_2m_1"
)

var (
	_Unum_index_0 = [...]uint8{0, 2, 4, 6}
	_Unum_index_1 = [...]uint8{0, 3, 6}
)

func (i Unum) String() string {
	switch {
	case i <= 2:
		return _Unum_name_0[_Unum_index_0[i]:_Unum_index_0[i+1]]
	case 253 <= i && i <= 254:
		i -= 253
		return _Unum_name_1[_Unum_index_1[i]:_Unum_index_1[i+1]]
	default:
		return "Unum(" + strconv.FormatInt(int64(i), 10) + ")"
	}
}
`

// Unsigned positive integers.
const unumpos_in = `type Unumpos uint
const (
	m253 Unumpos = iota + 253
	m254
)

const (
	m1 Unumpos = iota + 1
	m2
	m3
)
`

const unumpos_out = `func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[m253-253]
	_ = x[m254-254]
	_ = x[m1-1]
	_ = x[m2-2]
	_ = x[m3-3]
}

const (
	_Unumpos_name_0 = "m1m2m3"
	_Unumpos_name_1 = "m253m254"
)

var (
	_Unumpos_index_0 = [...]uint8{0, 2, 4, 6}
	_Unumpos_index_1 = [...]uint8{0, 4, 8}
)

func (i Unumpos) String() string {
	switch {
	case 1 <= i && i <= 3:
		i -= 1
		return _Unumpos_name_0[_Unumpos_index_0[i]:_Unumpos_index_0[i+1]]
	case 253 <= i && i <= 254:
		i -= 253
		return _Unumpos_name_1[_Unumpos_index_1[i]:_Unumpos_index_1[i+1]]
	default:
		return "Unumpos(" + strconv.FormatInt(int64(i), 10) + ")"
	}
}
`

// Enough gaps to trigger a map implementation of the method.
// Also includes a duplicate to test that it doesn't cause problems
const prime_in = `type Prime int
const (
	p2 Prime = 2
	p3 Prime = 3
	p5 Prime = 5
	p7 Prime = 7
	p77 Prime = 7 // Duplicate; note that p77 doesn't appear below.
	p11 Prime = 11
	p13 Prime = 13
	p17 Prime = 17
	p19 Prime = 19
	p23 Prime = 23
	p29 Prime = 29
	p37 Prime = 31
	p41 Prime = 41
	p43 Prime = 43
)
`

const prime_out = `func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[p2-2]
	_ = x[p3-3]
	_ = x[p5-5]
	_ = x[p7-7]
	_ = x[p77-7]
	_ = x[p11-11]
	_ = x[p13-13]
	_ = x[p17-17]
	_ = x[p19-19]
	_ = x[p23-23]
	_ = x[p29-29]
	_ = x[p37-31]
	_ = x[p41-41]
	_ = x[p43-43]
}

const _Prime_name = "p2p3p5p7p11p13p17p19p23p29p37p41p43"

var _Prime_map = map[Prime]string{
	2:  _Prime_name[0:2],
	3:  _Prime_name[2:4],
	5:  _Prime_name[4:6],
	7:  _Prime_name[6:8],
	11: _Prime_name[8:11],
	13: _Prime_name[11:14],
	17: _Prime_name[14:17],
	19: _Prime_name[17:20],
	23: _Prime_name[20:23],
	29: _Prime_name[23:26],
	31: _Prime_name[26:29],
	41: _Prime_name[29:32],
	43: _Prime_name[32:35],
}

func (i Prime) String() string {
	if str, ok := _Prime_map[i]; ok {
		return str
	}
	return "Prime(" + strconv.FormatInt(int64(i), 10) + ")"
}
`

const prefix_in = `type Type int
const (
	TypeInt Type = iota
	TypeString
	TypeFloat
	TypeRune
	TypeByte
	TypeStruct
	TypeSlice
)
`

const prefix_out = `func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[TypeInt-0]
	_ = x[TypeString-1]
	_ = x[TypeFloat-2]
	_ = x[TypeRune-3]
	_ = x[TypeByte-4]
	_ = x[TypeStruct-5]
	_ = x[TypeSlice-6]
}

const _Type_name = "IntStringFloatRuneByteStructSlice"

var _Type_index = [...]uint8{0, 3, 9, 14, 18, 22, 28, 33}

func (i Type) String() string {
	idx := int(i) - 0
	if i < 0 || idx >= len(_Type_index)-1 {
		return "Type(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Type_name[_Type_index[idx]:_Type_index[idx+1]]
}
`

const tokens_in = `type Token int
const (
	And Token = iota // &
	Or               // |
	Add              // +
	Sub              // -
	Ident
	Period // .

	// not to be used
	SingleBefore
	// not to be used
	BeforeAndInline // inline
	InlineGeneral /* inline general */
)
`

const tokens_out = `func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[And-0]
	_ = x[Or-1]
	_ = x[Add-2]
	_ = x[Sub-3]
	_ = x[Ident-4]
	_ = x[Period-5]
	_ = x[SingleBefore-6]
	_ = x[BeforeAndInline-7]
	_ = x[InlineGeneral-8]
}

const _Token_name = "&|+-Ident.SingleBeforeinlineinline general"

var _Token_index = [...]uint8{0, 1, 2, 3, 4, 9, 10, 22, 28, 42}

func (i Token) String() string {
	idx := int(i) - 0
	if i < 0 || idx >= len(_Token_index)-1 {
		return "Token(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Token_name[_Token_index[idx]:_Token_index[idx+1]]
}
`

const overflow8_in = `type Overflow8 int8
const (
	O_128 Overflow8 = -128
	O_127 Overflow8 = -127
	O_126 Overflow8 = -126
	O_125 Overflow8 = -125
	O_124 Overflow8 = -124
	O_123 Overflow8 = -123
	O_122 Overflow8 = -122
	O_121 Overflow8 = -121
	O_120 Overflow8 = -120
	O_119 Overflow8 = -119
	O_118 Overflow8 = -118
	O_117 Overflow8 = -117
	O_116 Overflow8 = -116
	O_115 Overflow8 = -115
	O_114 Overflow8 = -114
	O_113 Overflow8 = -113
	O_112 Overflow8 = -112
	O_111 Overflow8 = -111
	O_110 Overflow8 = -110
	O_109 Overflow8 = -109
	O_108 Overflow8 = -108
	O_107 Overflow8 = -107
	O_106 Overflow8 = -106
	O_105 Overflow8 = -105
	O_104 Overflow8 = -104
	O_103 Overflow8 = -103
	O_102 Overflow8 = -102
	O_101 Overflow8 = -101
	O_100 Overflow8 = -100
	O_99 Overflow8 = -99
	O_98 Overflow8 = -98
	O_97 Overflow8 = -97
	O_96 Overflow8 = -96
	O_95 Overflow8 = -95
	O_94 Overflow8 = -94
	O_93 Overflow8 = -93
	O_92 Overflow8 = -92
	O_91 Overflow8 = -91
	O_90 Overflow8 = -90
	O_89 Overflow8 = -89
	O_88 Overflow8 = -88
	O_87 Overflow8 = -87
	O_86 Overflow8 = -86
	O_85 Overflow8 = -85
	O_84 Overflow8 = -84
	O_83 Overflow8 = -83
	O_82 Overflow8 = -82
	O_81 Overflow8 = -81
	O_80 Overflow8 = -80
	O_79 Overflow8 = -79
	O_78 Overflow8 = -78
	O_77 Overflow8 = -77
	O_76 Overflow8 = -76
	O_75 Overflow8 = -75
	O_74 Overflow8 = -74
	O_73 Overflow8 = -73
	O_72 Overflow8 = -72
	O_71 Overflow8 = -71
	O_70 Overflow8 = -70
	O_69 Overflow8 = -69
	O_68 Overflow8 = -68
	O_67 Overflow8 = -67
	O_66 Overflow8 = -66
	O_65 Overflow8 = -65
	O_64 Overflow8 = -64
	O_63 Overflow8 = -63
	O_62 Overflow8 = -62
	O_61 Overflow8 = -61
	O_60 Overflow8 = -60
	O_59 Overflow8 = -59
	O_58 Overflow8 = -58
	O_57 Overflow8 = -57
	O_56 Overflow8 = -56
	O_55 Overflow8 = -55
	O_54 Overflow8 = -54
	O_53 Overflow8 = -53
	O_52 Overflow8 = -52
	O_51 Overflow8 = -51
	O_50 Overflow8 = -50
	O_49 Overflow8 = -49
	O_48 Overflow8 = -48
	O_47 Overflow8 = -47
	O_46 Overflow8 = -46
	O_45 Overflow8 = -45
	O_44 Overflow8 = -44
	O_43 Overflow8 = -43
	O_42 Overflow8 = -42
	O_41 Overflow8 = -41
	O_40 Overflow8 = -40
	O_39 Overflow8 = -39
	O_38 Overflow8 = -38
	O_37 Overflow8 = -37
	O_36 Overflow8 = -36
	O_35 Overflow8 = -35
	O_34 Overflow8 = -34
	O_33 Overflow8 = -33
	O_32 Overflow8 = -32
	O_31 Overflow8 = -31
	O_30 Overflow8 = -30
	O_29 Overflow8 = -29
	O_28 Overflow8 = -28
	O_27 Overflow8 = -27
	O_26 Overflow8 = -26
	O_25 Overflow8 = -25
	O_24 Overflow8 = -24
	O_23 Overflow8 = -23
	O_22 Overflow8 = -22
	O_21 Overflow8 = -21
	O_20 Overflow8 = -20
	O_19 Overflow8 = -19
	O_18 Overflow8 = -18
	O_17 Overflow8 = -17
	O_16 Overflow8 = -16
	O_15 Overflow8 = -15
	O_14 Overflow8 = -14
	O_13 Overflow8 = -13
	O_12 Overflow8 = -12
	O_11 Overflow8 = -11
	O_10 Overflow8 = -10
	O_9 Overflow8 = -9
	O_8 Overflow8 = -8
	O_7 Overflow8 = -7
	O_6 Overflow8 = -6
	O_5 Overflow8 = -5
	O_4 Overflow8 = -4
	O_3 Overflow8 = -3
	O_2 Overflow8 = -2
	O_1 Overflow8 = -1
	O0 Overflow8 = 0
	O1 Overflow8 = 1
	O2 Overflow8 = 2
	O3 Overflow8 = 3
	O4 Overflow8 = 4
	O5 Overflow8 = 5
	O6 Overflow8 = 6
	O7 Overflow8 = 7
	O8 Overflow8 = 8
	O9 Overflow8 = 9
	O10 Overflow8 = 10
	O11 Overflow8 = 11
	O12 Overflow8 = 12
	O13 Overflow8 = 13
	O14 Overflow8 = 14
	O15 Overflow8 = 15
	O16 Overflow8 = 16
	O17 Overflow8 = 17
	O18 Overflow8 = 18
	O19 Overflow8 = 19
	O20 Overflow8 = 20
	O21 Overflow8 = 21
	O22 Overflow8 = 22
	O23 Overflow8 = 23
	O24 Overflow8 = 24
	O25 Overflow8 = 25
	O26 Overflow8 = 26
	O27 Overflow8 = 27
	O28 Overflow8 = 28
	O29 Overflow8 = 29
	O30 Overflow8 = 30
	O31 Overflow8 = 31
	O32 Overflow8 = 32
	O33 Overflow8 = 33
	O34 Overflow8 = 34
	O35 Overflow8 = 35
	O36 Overflow8 = 36
	O37 Overflow8 = 37
	O38 Overflow8 = 38
	O39 Overflow8 = 39
	O40 Overflow8 = 40
	O41 Overflow8 = 41
	O42 Overflow8 = 42
	O43 Overflow8 = 43
	O44 Overflow8 = 44
	O45 Overflow8 = 45
	O46 Overflow8 = 46
	O47 Overflow8 = 47
	O48 Overflow8 = 48
	O49 Overflow8 = 49
	O50 Overflow8 = 50
	O51 Overflow8 = 51
	O52 Overflow8 = 52
	O53 Overflow8 = 53
	O54 Overflow8 = 54
	O55 Overflow8 = 55
	O56 Overflow8 = 56
	O57 Overflow8 = 57
	O58 Overflow8 = 58
	O59 Overflow8 = 59
	O60 Overflow8 = 60
	O61 Overflow8 = 61
	O62 Overflow8 = 62
	O63 Overflow8 = 63
	O64 Overflow8 = 64
	O65 Overflow8 = 65
	O66 Overflow8 = 66
	O67 Overflow8 = 67
	O68 Overflow8 = 68
	O69 Overflow8 = 69
	O70 Overflow8 = 70
	O71 Overflow8 = 71
	O72 Overflow8 = 72
	O73 Overflow8 = 73
	O74 Overflow8 = 74
	O75 Overflow8 = 75
	O76 Overflow8 = 76
	O77 Overflow8 = 77
	O78 Overflow8 = 78
	O79 Overflow8 = 79
	O80 Overflow8 = 80
	O81 Overflow8 = 81
	O82 Overflow8 = 82
	O83 Overflow8 = 83
	O84 Overflow8 = 84
	O85 Overflow8 = 85
	O86 Overflow8 = 86
	O87 Overflow8 = 87
	O88 Overflow8 = 88
	O89 Overflow8 = 89
	O90 Overflow8 = 90
	O91 Overflow8 = 91
	O92 Overflow8 = 92
	O93 Overflow8 = 93
	O94 Overflow8 = 94
	O95 Overflow8 = 95
	O96 Overflow8 = 96
	O97 Overflow8 = 97
	O98 Overflow8 = 98
	O99 Overflow8 = 99
	O100 Overflow8 = 100
	O101 Overflow8 = 101
	O102 Overflow8 = 102
	O103 Overflow8 = 103
	O104 Overflow8 = 104
	O105 Overflow8 = 105
	O106 Overflow8 = 106
	O107 Overflow8 = 107
	O108 Overflow8 = 108
	O109 Overflow8 = 109
	O110 Overflow8 = 110
	O111 Overflow8 = 111
	O112 Overflow8 = 112
	O113 Overflow8 = 113
	O114 Overflow8 = 114
	O115 Overflow8 = 115
	O116 Overflow8 = 116
	O117 Overflow8 = 117
	O118 Overflow8 = 118
	O119 Overflow8 = 119
	O120 Overflow8 = 120
	O121 Overflow8 = 121
	O122 Overflow8 = 122
	O123 Overflow8 = 123
	O124 Overflow8 = 124
	O125 Overflow8 = 125
	O126 Overflow8 = 126
	O127 Overflow8 = 127
)
`

const overflow8_out = `func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[O_128 - -128]
	_ = x[O_127 - -127]
	_ = x[O_126 - -126]
	_ = x[O_125 - -125]
	_ = x[O_124 - -124]
	_ = x[O_123 - -123]
	_ = x[O_122 - -122]
	_ = x[O_121 - -121]
	_ = x[O_120 - -120]
	_ = x[O_119 - -119]
	_ = x[O_118 - -118]
	_ = x[O_117 - -117]
	_ = x[O_116 - -116]
	_ = x[O_115 - -115]
	_ = x[O_114 - -114]
	_ = x[O_113 - -113]
	_ = x[O_112 - -112]
	_ = x[O_111 - -111]
	_ = x[O_110 - -110]
	_ = x[O_109 - -109]
	_ = x[O_108 - -108]
	_ = x[O_107 - -107]
	_ = x[O_106 - -106]
	_ = x[O_105 - -105]
	_ = x[O_104 - -104]
	_ = x[O_103 - -103]
	_ = x[O_102 - -102]
	_ = x[O_101 - -101]
	_ = x[O_100 - -100]
	_ = x[O_99 - -99]
	_ = x[O_98 - -98]
	_ = x[O_97 - -97]
	_ = x[O_96 - -96]
	_ = x[O_95 - -95]
	_ = x[O_94 - -94]
	_ = x[O_93 - -93]
	_ = x[O_92 - -92]
	_ = x[O_91 - -91]
	_ = x[O_90 - -90]
	_ = x[O_89 - -89]
	_ = x[O_88 - -88]
	_ = x[O_87 - -87]
	_ = x[O_86 - -86]
	_ = x[O_85 - -85]
	_ = x[O_84 - -84]
	_ = x[O_83 - -83]
	_ = x[O_82 - -82]
	_ = x[O_81 - -81]
	_ = x[O_80 - -80]
	_ = x[O_79 - -79]
	_ = x[O_78 - -78]
	_ = x[O_77 - -77]
	_ = x[O_76 - -76]
	_ = x[O_75 - -75]
	_ = x[O_74 - -74]
	_ = x[O_73 - -73]
	_ = x[O_72 - -72]
	_ = x[O_71 - -71]
	_ = x[O_70 - -70]
	_ = x[O_69 - -69]
	_ = x[O_68 - -68]
	_ = x[O_67 - -67]
	_ = x[O_66 - -66]
	_ = x[O_65 - -65]
	_ = x[O_64 - -64]
	_ = x[O_63 - -63]
	_ = x[O_62 - -62]
	_ = x[O_61 - -61]
	_ = x[O_60 - -60]
	_ = x[O_59 - -59]
	_ = x[O_58 - -58]
	_ = x[O_57 - -57]
	_ = x[O_56 - -56]
	_ = x[O_55 - -55]
	_ = x[O_54 - -54]
	_ = x[O_53 - -53]
	_ = x[O_52 - -52]
	_ = x[O_51 - -51]
	_ = x[O_50 - -50]
	_ = x[O_49 - -49]
	_ = x[O_48 - -48]
	_ = x[O_47 - -47]
	_ = x[O_46 - -46]
	_ = x[O_45 - -45]
	_ = x[O_44 - -44]
	_ = x[O_43 - -43]
	_ = x[O_42 - -42]
	_ = x[O_41 - -41]
	_ = x[O_40 - -40]
	_ = x[O_39 - -39]
	_ = x[O_38 - -38]
	_ = x[O_37 - -37]
	_ = x[O_36 - -36]
	_ = x[O_35 - -35]
	_ = x[O_34 - -34]
	_ = x[O_33 - -33]
	_ = x[O_32 - -32]
	_ = x[O_31 - -31]
	_ = x[O_30 - -30]
	_ = x[O_29 - -29]
	_ = x[O_28 - -28]
	_ = x[O_27 - -27]
	_ = x[O_26 - -26]
	_ = x[O_25 - -25]
	_ = x[O_24 - -24]
	_ = x[O_23 - -23]
	_ = x[O_22 - -22]
	_ = x[O_21 - -21]
	_ = x[O_20 - -20]
	_ = x[O_19 - -19]
	_ = x[O_18 - -18]
	_ = x[O_17 - -17]
	_ = x[O_16 - -16]
	_ = x[O_15 - -15]
	_ = x[O_14 - -14]
	_ = x[O_13 - -13]
	_ = x[O_12 - -12]
	_ = x[O_11 - -11]
	_ = x[O_10 - -10]
	_ = x[O_9 - -9]
	_ = x[O_8 - -8]
	_ = x[O_7 - -7]
	_ = x[O_6 - -6]
	_ = x[O_5 - -5]
	_ = x[O_4 - -4]
	_ = x[O_3 - -3]
	_ = x[O_2 - -2]
	_ = x[O_1 - -1]
	_ = x[O0-0]
	_ = x[O1-1]
	_ = x[O2-2]
	_ = x[O3-3]
	_ = x[O4-4]
	_ = x[O5-5]
	_ = x[O6-6]
	_ = x[O7-7]
	_ = x[O8-8]
	_ = x[O9-9]
	_ = x[O10-10]
	_ = x[O11-11]
	_ = x[O12-12]
	_ = x[O13-13]
	_ = x[O14-14]
	_ = x[O15-15]
	_ = x[O16-16]
	_ = x[O17-17]
	_ = x[O18-18]
	_ = x[O19-19]
	_ = x[O20-20]
	_ = x[O21-21]
	_ = x[O22-22]
	_ = x[O23-23]
	_ = x[O24-24]
	_ = x[O25-25]
	_ = x[O26-26]
	_ = x[O27-27]
	_ = x[O28-28]
	_ = x[O29-29]
	_ = x[O30-30]
	_ = x[O31-31]
	_ = x[O32-32]
	_ = x[O33-33]
	_ = x[O34-34]
	_ = x[O35-35]
	_ = x[O36-36]
	_ = x[O37-37]
	_ = x[O38-38]
	_ = x[O39-39]
	_ = x[O40-40]
	_ = x[O41-41]
	_ = x[O42-42]
	_ = x[O43-43]
	_ = x[O44-44]
	_ = x[O45-45]
	_ = x[O46-46]
	_ = x[O47-47]
	_ = x[O48-48]
	_ = x[O49-49]
	_ = x[O50-50]
	_ = x[O51-51]
	_ = x[O52-52]
	_ = x[O53-53]
	_ = x[O54-54]
	_ = x[O55-55]
	_ = x[O56-56]
	_ = x[O57-57]
	_ = x[O58-58]
	_ = x[O59-59]
	_ = x[O60-60]
	_ = x[O61-61]
	_ = x[O62-62]
	_ = x[O63-63]
	_ = x[O64-64]
	_ = x[O65-65]
	_ = x[O66-66]
	_ = x[O67-67]
	_ = x[O68-68]
	_ = x[O69-69]
	_ = x[O70-70]
	_ = x[O71-71]
	_ = x[O72-72]
	_ = x[O73-73]
	_ = x[O74-74]
	_ = x[O75-75]
	_ = x[O76-76]
	_ = x[O77-77]
	_ = x[O78-78]
	_ = x[O79-79]
	_ = x[O80-80]
	_ = x[O81-81]
	_ = x[O82-82]
	_ = x[O83-83]
	_ = x[O84-84]
	_ = x[O85-85]
	_ = x[O86-86]
	_ = x[O87-87]
	_ = x[O88-88]
	_ = x[O89-89]
	_ = x[O90-90]
	_ = x[O91-91]
	_ = x[O92-92]
	_ = x[O93-93]
	_ = x[O94-94]
	_ = x[O95-95]
	_ = x[O96-96]
	_ = x[O97-97]
	_ = x[O98-98]
	_ = x[O99-99]
	_ = x[O100-100]
	_ = x[O101-101]
	_ = x[O102-102]
	_ = x[O103-103]
	_ = x[O104-104]
	_ = x[O105-105]
	_ = x[O106-106]
	_ = x[O107-107]
	_ = x[O108-108]
	_ = x[O109-109]
	_ = x[O110-110]
	_ = x[O111-111]
	_ = x[O112-112]
	_ = x[O113-113]
	_ = x[O114-114]
	_ = x[O115-115]
	_ = x[O116-116]
	_ = x[O117-117]
	_ = x[O118-118]
	_ = x[O119-119]
	_ = x[O120-120]
	_ = x[O121-121]
	_ = x[O122-122]
	_ = x[O123-123]
	_ = x[O124-124]
	_ = x[O125-125]
	_ = x[O126-126]
	_ = x[O127-127]
}

const _Overflow8_name = "O_128O_127O_126O_125O_124O_123O_122O_121O_120O_119O_118O_117O_116O_115O_114O_113O_112O_111O_110O_109O_108O_107O_106O_105O_104O_103O_102O_101O_100O_99O_98O_97O_96O_95O_94O_93O_92O_91O_90O_89O_88O_87O_86O_85O_84O_83O_82O_81O_80O_79O_78O_77O_76O_75O_74O_73O_72O_71O_70O_69O_68O_67O_66O_65O_64O_63O_62O_61O_60O_59O_58O_57O_56O_55O_54O_53O_52O_51O_50O_49O_48O_47O_46O_45O_44O_43O_42O_41O_40O_39O_38O_37O_36O_35O_34O_33O_32O_31O_30O_29O_28O_27O_26O_25O_24O_23O_22O_21O_20O_19O_18O_17O_16O_15O_14O_13O_12O_11O_10O_9O_8O_7O_6O_5O_4O_3O_2O_1O0O1O2O3O4O5O6O7O8O9O10O11O12O13O14O15O16O17O18O19O20O21O22O23O24O25O26O27O28O29O30O31O32O33O34O35O36O37O38O39O40O41O42O43O44O45O46O47O48O49O50O51O52O53O54O55O56O57O58O59O60O61O62O63O64O65O66O67O68O69O70O71O72O73O74O75O76O77O78O79O80O81O82O83O84O85O86O87O88O89O90O91O92O93O94O95O96O97O98O99O100O101O102O103O104O105O106O107O108O109O110O111O112O113O114O115O116O117O118O119O120O121O122O123O124O125O126O127"

var _Overflow8_index = [...]uint16{0, 5, 10, 15, 20, 25, 30, 35, 40, 45, 50, 55, 60, 65, 70, 75, 80, 85, 90, 95, 100, 105, 110, 115, 120, 125, 130, 135, 140, 145, 149, 153, 157, 161, 165, 169, 173, 177, 181, 185, 189, 193, 197, 201, 205, 209, 213, 217, 221, 225, 229, 233, 237, 241, 245, 249, 253, 257, 261, 265, 269, 273, 277, 281, 285, 289, 293, 297, 301, 305, 309, 313, 317, 321, 325, 329, 333, 337, 341, 345, 349, 353, 357, 361, 365, 369, 373, 377, 381, 385, 389, 393, 397, 401, 405, 409, 413, 417, 421, 425, 429, 433, 437, 441, 445, 449, 453, 457, 461, 465, 469, 473, 477, 481, 485, 489, 493, 497, 501, 505, 508, 511, 514, 517, 520, 523, 526, 529, 532, 534, 536, 538, 540, 542, 544, 546, 548, 550, 552, 555, 558, 561, 564, 567, 570, 573, 576, 579, 582, 585, 588, 591, 594, 597, 600, 603, 606, 609, 612, 615, 618, 621, 624, 627, 630, 633, 636, 639, 642, 645, 648, 651, 654, 657, 660, 663, 666, 669, 672, 675, 678, 681, 684, 687, 690, 693, 696, 699, 702, 705, 708, 711, 714, 717, 720, 723, 726, 729, 732, 735, 738, 741, 744, 747, 750, 753, 756, 759, 762, 765, 768, 771, 774, 777, 780, 783, 786, 789, 792, 795, 798, 801, 804, 807, 810, 813, 816, 819, 822, 826, 830, 834, 838, 842, 846, 850, 854, 858, 862, 866, 870, 874, 878, 882, 886, 890, 894, 898, 902, 906, 910, 914, 918, 922, 926, 930, 934}

func (i Overflow8) String() string {
	idx := int(i) - -128
	if i < -128 || idx >= len(_Overflow8_index)-1 {
		return "Overflow8(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Overflow8_name[_Overflow8_index[idx]:_Overflow8_index[idx+1]]
}
`

func TestGolden(t *testing.T) {
	testenv.NeedsTool(t, "go")

	dir := t.TempDir()
	for _, test := range golden {
		t.Run(test.name, func(t *testing.T) {
			input := "package test\n" + test.input
			file := test.name + ".go"
			absFile := filepath.Join(dir, file)
			err := os.WriteFile(absFile, []byte(input), 0644)
			if err != nil {
				t.Fatal(err)
			}

			pkgs := loadPackages([]string{absFile}, nil, test.trimPrefix, test.lineComment, t.Logf)
			if len(pkgs) != 1 {
				t.Fatalf("got %d parsed packages but expected 1", len(pkgs))
			}
			// Extract the name and type of the constant from the first line.
			tokens := strings.SplitN(test.input, " ", 3)
			if len(tokens) != 3 {
				t.Fatalf("%s: need type declaration on first line", test.name)
			}

			g := Generator{
				pkg:  pkgs[0],
				logf: t.Logf,
			}
			g.generate(tokens[1], findValues(tokens[1], pkgs[0]))
			got := string(g.format())
			if got != test.output {
				t.Errorf("%s: got(%d)\n====\n%q====\nexpected(%d)\n====\n%q", test.name, len(got), got, len(test.output), test.output)
			}
		})
	}
}
