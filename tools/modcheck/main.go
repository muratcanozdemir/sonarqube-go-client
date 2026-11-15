// Command modcheck reads file paths (one per line) on stdin and fails if any
// would make the go command refuse to package the module, as happened with
// v1.0.0 and v1.0.1 (a '×' in a docs/ file name).
//
//	git -c core.quotepath=off ls-files | go run ./tools/modcheck
package main

import (
	"bufio"
	"fmt"
	"os"

	"golang.org/x/mod/module"
)

func main() {
	bad := 0
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		if err := module.CheckFilePath(sc.Text()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			bad++
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "modcheck:", err)
		os.Exit(1)
	}
	if bad > 0 {
		fmt.Fprintf(os.Stderr, "modcheck: %d path(s) would break the module zip\n", bad)
		os.Exit(1)
	}
}
