// Command minosctl is the operator CLI for commissioning tasks directly
// against Minos, short-circuiting the Hermes/Discord intake path for
// Phase 1 Slice A testing per docs/phase-1-plan.md §4.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: minosctl <command> [flags]\n\ncommands:\n  commission  commission a new task\n\n")
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	switch flag.Arg(0) {
	case "commission":
		commissionCmd(flag.Args()[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", flag.Arg(0))
		flag.Usage()
		os.Exit(2)
	}
}

func commissionCmd(args []string) {
	fs := flag.NewFlagSet("commission", flag.ExitOnError)
	project := fs.String("project", "", "project slug (required)")
	brief := fs.String("brief", "", "one-line task brief (required)")
	repo := fs.String("repo", "", "target repo URL (required)")
	branch := fs.String("branch", "", "feature branch (required)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *project == "" || *brief == "" || *repo == "" || *branch == "" {
		fs.Usage()
		os.Exit(2)
	}

	// Dispatch via HTTP to Minos lands in Slice A task 6.
	fmt.Printf("commission: project=%s repo=%s branch=%s brief=%q\n", *project, *repo, *branch, *brief)
}
