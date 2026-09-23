package main

import (
	"github.com/daviddwlee84/lazycrontab/internal/cli"
	"os"
	_ "time/tzdata"
)

var version = "dev"

func main() { os.Exit(cli.Execute(version)) }
