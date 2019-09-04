package main

import (
	"fmt"
	"os"

	"github.com/urfave/cli"
)

func main() {
	debug := false
	app := cli.NewApp()
	app.Name = "ipp"
	app.Usage = "Instance-per-Pod"
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:        "debug",
			Usage:       "debug mode",
			Destination: &debug,
		},
	}
	app.Commands = []cli.Command{
		webhookCommand,
	}
	app.Before = func(clicontext *cli.Context) error {
		return nil
	}
	if err := app.Run(os.Args); err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "error: %+v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}
}
