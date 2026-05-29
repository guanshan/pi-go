package main

import (
	"context"
	"fmt"
	"os"

	codingagent "github.com/guanshan/pi-go/packages/coding-agent"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := codingagent.Run(context.Background(), codingagent.BuildInfo{
		Version: version,
		Commit:  commit,
		Date:    date,
	}, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
