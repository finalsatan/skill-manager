package main

import (
	"os"

	"skillet/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
