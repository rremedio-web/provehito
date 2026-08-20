// Command jsonfield prints one string field from JSON on stdin.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: jsonfield FIELD\n")
		os.Exit(2)
	}
	var data map[string]any
	if err := json.NewDecoder(os.Stdin).Decode(&data); err != nil {
		fmt.Fprintf(os.Stderr, "jsonfield: %v\n", err)
		os.Exit(2)
	}
	value, ok := data[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "jsonfield: missing field %q\n", os.Args[1])
		os.Exit(2)
	}
	switch v := value.(type) {
	case string:
		fmt.Println(v)
	case float64:
		fmt.Printf("%d\n", int(v))
	default:
		fmt.Fprintf(os.Stderr, "jsonfield: unsupported field type\n")
		os.Exit(2)
	}
}
