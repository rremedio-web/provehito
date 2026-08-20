// Command validatejson exits 0 when a file contains valid JSON.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: validatejson FILE\n")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "validatejson: %v\n", err)
		os.Exit(2)
	}
	if len(data) == 0 {
		fmt.Fprintf(os.Stderr, "validatejson: empty file\n")
		os.Exit(1)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		fmt.Fprintf(os.Stderr, "validatejson: %v\n", err)
		os.Exit(1)
	}
}
