// Password input is read from stdin, not command-line arguments/history.
package main

import (
	"bufio"
	"coursek/blog/backend/internal/auth"
	"fmt"
	"os"
	"strings"
)

func main() {
	s, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && len(s) == 0 {
		fmt.Fprintln(os.Stderr, "read password from stdin")
		os.Exit(1)
	}
	h, err := auth.HashPassword(strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(h)
}
