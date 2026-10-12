package main

import (
	"os"

	"github.com/finalsatan/skill-manager/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
