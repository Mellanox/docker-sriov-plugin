package main

import (
	log "github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	"github.com/docker/go-plugins-helpers/network"
	"os"

	"github.com/gopher-net/docker-passthrough-plugin/driver"
)

const (
	version = "0.0.1"
)

// Run initializes the driver
func Run(ctx *cli.Context) {
	if ctx.Bool("debug") {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.DebugLevel)
	}

	d, err := driver.StartDriver()
	if err != nil {
		panic(err)
	}
	h := network.NewHandler(d)

	log.Debugf("Mellanox passthrough/sriov plugin started.")
	log.Debugf("Ready to accept commands.")

	err = h.ServeUnix("passthrough", 0)
	if err != nil {
		log.Fatal("Run app error: %s", err.Error())
		os.Exit(1)
	}
}

func main() {

	var flagDebug = cli.BoolFlag{
		Name:  "debug, d",
		Usage: "enable debugging",
	}
	app := cli.NewApp()
	app.Name = "passthrough"
	app.Usage = "Docker Networking using Passthrough/SRIOV netdevices"
	app.Version = version
	app.Flags = []cli.Flag{
		flagDebug,
	}
	app.Action = Run
	app.Run(os.Args)
}
