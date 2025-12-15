// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Check that int8 bounds checking doesn't cause panics or compilation errors.

package main

import (
	"fmt"
	"strings"
)

type Int8overflow int8

const (
	I_128 Int8overflow = -128
	I_127 Int8overflow = -127
	I_126 Int8overflow = -126
	I_125 Int8overflow = -125
	I_124 Int8overflow = -124
	I_123 Int8overflow = -123
	I_122 Int8overflow = -122
	I_121 Int8overflow = -121
	I_120 Int8overflow = -120
	I_119 Int8overflow = -119
	I_118 Int8overflow = -118
	I_117 Int8overflow = -117
	I_116 Int8overflow = -116
	I_115 Int8overflow = -115
	I_114 Int8overflow = -114
	I_113 Int8overflow = -113
	I_112 Int8overflow = -112
	I_111 Int8overflow = -111
	I_110 Int8overflow = -110
	I_109 Int8overflow = -109
	I_108 Int8overflow = -108
	I_107 Int8overflow = -107
	I_106 Int8overflow = -106
	I_105 Int8overflow = -105
	I_104 Int8overflow = -104
	I_103 Int8overflow = -103
	I_102 Int8overflow = -102
	I_101 Int8overflow = -101
	I_100 Int8overflow = -100
	I_99  Int8overflow = -99
	I_98  Int8overflow = -98
	I_97  Int8overflow = -97
	I_96  Int8overflow = -96
	I_95  Int8overflow = -95
	I_94  Int8overflow = -94
	I_93  Int8overflow = -93
	I_92  Int8overflow = -92
	I_91  Int8overflow = -91
	I_90  Int8overflow = -90
	I_89  Int8overflow = -89
	I_88  Int8overflow = -88
	I_87  Int8overflow = -87
	I_86  Int8overflow = -86
	I_85  Int8overflow = -85
	I_84  Int8overflow = -84
	I_83  Int8overflow = -83
	I_82  Int8overflow = -82
	I_81  Int8overflow = -81
	I_80  Int8overflow = -80
	I_79  Int8overflow = -79
	I_78  Int8overflow = -78
	I_77  Int8overflow = -77
	I_76  Int8overflow = -76
	I_75  Int8overflow = -75
	I_74  Int8overflow = -74
	I_73  Int8overflow = -73
	I_72  Int8overflow = -72
	I_71  Int8overflow = -71
	I_70  Int8overflow = -70
	I_69  Int8overflow = -69
	I_68  Int8overflow = -68
	I_67  Int8overflow = -67
	I_66  Int8overflow = -66
	I_65  Int8overflow = -65
	I_64  Int8overflow = -64
	I_63  Int8overflow = -63
	I_62  Int8overflow = -62
	I_61  Int8overflow = -61
	I_60  Int8overflow = -60
	I_59  Int8overflow = -59
	I_58  Int8overflow = -58
	I_57  Int8overflow = -57
	I_56  Int8overflow = -56
	I_55  Int8overflow = -55
	I_54  Int8overflow = -54
	I_53  Int8overflow = -53
	I_52  Int8overflow = -52
	I_51  Int8overflow = -51
	I_50  Int8overflow = -50
	I_49  Int8overflow = -49
	I_48  Int8overflow = -48
	I_47  Int8overflow = -47
	I_46  Int8overflow = -46
	I_45  Int8overflow = -45
	I_44  Int8overflow = -44
	I_43  Int8overflow = -43
	I_42  Int8overflow = -42
	I_41  Int8overflow = -41
	I_40  Int8overflow = -40
	I_39  Int8overflow = -39
	I_38  Int8overflow = -38
	I_37  Int8overflow = -37
	I_36  Int8overflow = -36
	I_35  Int8overflow = -35
	I_34  Int8overflow = -34
	I_33  Int8overflow = -33
	I_32  Int8overflow = -32
	I_31  Int8overflow = -31
	I_30  Int8overflow = -30
	I_29  Int8overflow = -29
	I_28  Int8overflow = -28
	I_27  Int8overflow = -27
	I_26  Int8overflow = -26
	I_25  Int8overflow = -25
	I_24  Int8overflow = -24
	I_23  Int8overflow = -23
	I_22  Int8overflow = -22
	I_21  Int8overflow = -21
	I_20  Int8overflow = -20
	I_19  Int8overflow = -19
	I_18  Int8overflow = -18
	I_17  Int8overflow = -17
	I_16  Int8overflow = -16
	I_15  Int8overflow = -15
	I_14  Int8overflow = -14
	I_13  Int8overflow = -13
	I_12  Int8overflow = -12
	I_11  Int8overflow = -11
	I_10  Int8overflow = -10
	I_9   Int8overflow = -9
	I_8   Int8overflow = -8
	I_7   Int8overflow = -7
	I_6   Int8overflow = -6
	I_5   Int8overflow = -5
	I_4   Int8overflow = -4
	I_3   Int8overflow = -3
	I_2   Int8overflow = -2
	I_1   Int8overflow = -1
	I0    Int8overflow = 0
	I1    Int8overflow = 1
	I2    Int8overflow = 2
	I3    Int8overflow = 3
	I4    Int8overflow = 4
	I5    Int8overflow = 5
	I6    Int8overflow = 6
	I7    Int8overflow = 7
	I8    Int8overflow = 8
	I9    Int8overflow = 9
	I10   Int8overflow = 10
	I11   Int8overflow = 11
	I12   Int8overflow = 12
	I13   Int8overflow = 13
	I14   Int8overflow = 14
	I15   Int8overflow = 15
	I16   Int8overflow = 16
	I17   Int8overflow = 17
	I18   Int8overflow = 18
	I19   Int8overflow = 19
	I20   Int8overflow = 20
	I21   Int8overflow = 21
	I22   Int8overflow = 22
	I23   Int8overflow = 23
	I24   Int8overflow = 24
	I25   Int8overflow = 25
	I26   Int8overflow = 26
	I27   Int8overflow = 27
	I28   Int8overflow = 28
	I29   Int8overflow = 29
	I30   Int8overflow = 30
	I31   Int8overflow = 31
	I32   Int8overflow = 32
	I33   Int8overflow = 33
	I34   Int8overflow = 34
	I35   Int8overflow = 35
	I36   Int8overflow = 36
	I37   Int8overflow = 37
	I38   Int8overflow = 38
	I39   Int8overflow = 39
	I40   Int8overflow = 40
	I41   Int8overflow = 41
	I42   Int8overflow = 42
	I43   Int8overflow = 43
	I44   Int8overflow = 44
	I45   Int8overflow = 45
	I46   Int8overflow = 46
	I47   Int8overflow = 47
	I48   Int8overflow = 48
	I49   Int8overflow = 49
	I50   Int8overflow = 50
	I51   Int8overflow = 51
	I52   Int8overflow = 52
	I53   Int8overflow = 53
	I54   Int8overflow = 54
	I55   Int8overflow = 55
	I56   Int8overflow = 56
	I57   Int8overflow = 57
	I58   Int8overflow = 58
	I59   Int8overflow = 59
	I60   Int8overflow = 60
	I61   Int8overflow = 61
	I62   Int8overflow = 62
	I63   Int8overflow = 63
	I64   Int8overflow = 64
	I65   Int8overflow = 65
	I66   Int8overflow = 66
	I67   Int8overflow = 67
	I68   Int8overflow = 68
	I69   Int8overflow = 69
	I70   Int8overflow = 70
	I71   Int8overflow = 71
	I72   Int8overflow = 72
	I73   Int8overflow = 73
	I74   Int8overflow = 74
	I75   Int8overflow = 75
	I76   Int8overflow = 76
	I77   Int8overflow = 77
	I78   Int8overflow = 78
	I79   Int8overflow = 79
	I80   Int8overflow = 80
	I81   Int8overflow = 81
	I82   Int8overflow = 82
	I83   Int8overflow = 83
	I84   Int8overflow = 84
	I85   Int8overflow = 85
	I86   Int8overflow = 86
	I87   Int8overflow = 87
	I88   Int8overflow = 88
	I89   Int8overflow = 89
	I90   Int8overflow = 90
	I91   Int8overflow = 91
	I92   Int8overflow = 92
	I93   Int8overflow = 93
	I94   Int8overflow = 94
	I95   Int8overflow = 95
	I96   Int8overflow = 96
	I97   Int8overflow = 97
	I98   Int8overflow = 98
	I99   Int8overflow = 99
	I100  Int8overflow = 100
	I101  Int8overflow = 101
	I102  Int8overflow = 102
	I103  Int8overflow = 103
	I104  Int8overflow = 104
	I105  Int8overflow = 105
	I106  Int8overflow = 106
	I107  Int8overflow = 107
	I108  Int8overflow = 108
	I109  Int8overflow = 109
	I110  Int8overflow = 110
	I111  Int8overflow = 111
	I112  Int8overflow = 112
	I113  Int8overflow = 113
	I114  Int8overflow = 114
	I115  Int8overflow = 115
	I116  Int8overflow = 116
	I117  Int8overflow = 117
	I118  Int8overflow = 118
	I119  Int8overflow = 119
	I120  Int8overflow = 120
	I121  Int8overflow = 121
	I122  Int8overflow = 122
	I123  Int8overflow = 123
	I124  Int8overflow = 124
	I125  Int8overflow = 125
	I126  Int8overflow = 126
	I127  Int8overflow = 127
)

func main() {
	testValues := []Int8overflow{
		I_128,
		I_127,
		I_1,
		I0,
		I1,
		I126,
		I127,
	}

	for _, val := range testValues {
		want := strings.ReplaceAll(fmt.Sprintf("I%d", int(val)), "-", "_")
		result := fmt.Sprint(val)
		if result != want {
			panic(fmt.Sprintf("int8overflow.go: got %s, want %s for value %d", result, want, int8(val)))
		}
	}
}
