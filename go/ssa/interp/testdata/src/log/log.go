package log

import (
	"fmt"
	"os"
)

func Println(v ...any) {
	fmt.Println(v...)
}
func Printf(format string, v ...any) {
	fmt.Printf(format, v...)
}

func Fatalln(v ...any) {
	Println(v...)
	os.Exit(1)
}

func Fatalf(format string, v ...any) {
	Printf(format, v...)
	os.Exit(1)
}
