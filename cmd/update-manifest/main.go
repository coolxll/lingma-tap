package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/coolxll/lingma-tap/internal/updater"
)

func main() {
	directory := flag.String("dir", "", "directory containing release assets")
	version := flag.String("version", "", "release version, including v prefix")
	flag.Parse()
	if *directory == "" || *version == "" {
		fmt.Fprintln(os.Stderr, "-dir and -version are required")
		os.Exit(2)
	}
	if err := updater.GenerateSignedManifest(*directory, *version, os.Getenv("UPDATE_SIGNING_PRIVATE_KEY")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
