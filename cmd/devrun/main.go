package main

import (
	"os"

	"github.com/exterkamp/devrun/internal/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:])) }
