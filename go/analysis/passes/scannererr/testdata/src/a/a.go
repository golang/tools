package a

import (
	"bufio"
	"bytes"
	"strings"
)

func missingErr() {
	sc := bufio.NewScanner(nil) // want `bufio.Scanner "sc" is used in Scan loop at line 11 without final check of sc.Err\(\)`
	for sc.Scan() {             // L11
		println(sc.Text())
	}
}

func missingErr2() {
	sc := bufio.NewScanner(nil) // want `bufio.Scanner "sc" is used in Scan loop at line 19 without final check of sc.Err\(\)`
	for {
		if !sc.Scan() { // L19
			break
		}
		println(sc.Text())
	}
}

func nopeErrIsChecked() {
	sc := bufio.NewScanner(nil)
	for sc.Scan() {
	}
	if err := sc.Err(); err != nil {
		panic(err)
	}
}

func nopeErrIsCalled() {
	sc := bufio.NewScanner(nil)
	for sc.Scan() {
		println(sc.Text())
	}
	_ = sc.Err() // ignore error
}

func nopeScannerIsParam(sc *bufio.Scanner) {
	for sc.Scan() {
	}
}

func nopeScannerEscapes(sc *bufio.Scanner) {
	for sc.Scan() {
	}
	arbitraryEffects(sc)
}

func nopeInfallibleReader() {
	{
		sc := bufio.NewScanner(strings.NewReader("")) // nope
		for sc.Scan() {
			println(sc.Text())
		}
	}
	{
		sc := bufio.NewScanner(bytes.NewReader(nil)) // nope
		for sc.Scan() {
			println(sc.Text())
		}
	}
	{
		sc := bufio.NewScanner(bytes.NewBufferString("")) // nope
		for sc.Scan() {
			println(sc.Text())
		}
	}
}

func arbitraryEffects(any)
