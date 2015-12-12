package util

import (
	"fmt"
	"os"
)

var Exiter func(code int)

func init() {
	Exiter = os.Exit
}

func Check(err error) {
	if err != nil {
		Error(err)
	}
}

func Error(err error) {
	fmt.Fprintf(os.Stderr, "ERROR: %s\n", err)
	Exiter(1)
}
