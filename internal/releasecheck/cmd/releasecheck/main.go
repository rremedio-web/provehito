// Command releasecheck validates release ZIP archives.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/provehito-project/provehito/internal/releasecheck"
)

func main() {
	expectedList := flag.String("expected-list", "", "newline-separated expected tracked file paths")
	denylist := flag.String("private-denylist", "", "path to private denylist file")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: releasecheck [flags] ARCHIVE.zip\n")
		os.Exit(2)
	}

	const maxArchiveSize = 32 * 1024 * 1024
	archivePath := flag.Arg(0)
	info, err := os.Stat(archivePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "releasecheck: stat archive: %v\n", err)
		os.Exit(2)
	}
	if info.Size() > maxArchiveSize {
		fmt.Fprintf(os.Stderr, "releasecheck: archive exceeds 32 MiB limit\n")
		os.Exit(2)
	}
	data, err := os.ReadFile(archivePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "releasecheck: read archive: %v\n", err)
		os.Exit(2)
	}

	var expected []string
	if *expectedList != "" {
		listData, err := os.ReadFile(*expectedList)
		if err != nil {
			fmt.Fprintf(os.Stderr, "releasecheck: read expected list: %v\n", err)
			os.Exit(2)
		}
		for _, line := range strings.Split(string(listData), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				expected = append(expected, line)
			}
		}
	}

	result, err := releasecheck.Check(data, releasecheck.Options{
		ExpectedFiles:       expected,
		PrivateDenylistPath: *denylist,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "releasecheck: %v\n", err)
		os.Exit(2)
	}

	out, err := releasecheck.CanonicalJSON(result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "releasecheck: marshal: %v\n", err)
		os.Exit(2)
	}
	fmt.Println(string(out))

	if result.StructuralStatus == releasecheck.StatusFail || result.PrivateStatus == releasecheck.StatusFail {
		os.Exit(1)
	}
}
