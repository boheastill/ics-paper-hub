package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/boheastill/ics-paper-hub/pkg/ics"
	"github.com/boheastill/ics-paper-hub/pkg/server"
)

func main() {
	portFlag := flag.Int("port", 3000, "Web server port")
	hostIPFlag := flag.String("host", "", "LAN host IP override (auto-detected if empty)")
	dataDirFlag := flag.String("data", "./data", "Data directory for SQLite/JSON store and PDFs")
	printctlFlag := flag.String("printctl", "/home/bohea/print/printctl/bin/printctl", "Path to printctl binary")
	printerIPFlag := flag.String("printer-ip", "192.168.31.202", "Target printer IP")
	autoPrintFlag := flag.Bool("auto-print", false, "Automatically dispatch to physical printer upon task creation")
	icsChannelFlag := flag.String("ics-channel", "paper-interaction", "ICS channel name to notify")

	flag.Parse()

	cfg := server.Config{
		Port:         *portFlag,
		HostIP:       *hostIPFlag,
		DataDir:      *dataDirFlag,
		PrintctlPath: *printctlFlag,
		PrinterIP:    *printerIPFlag,
		AutoPrint:    *autoPrintFlag,
		ICSConfig: ics.Config{
			Enabled: true,
			Channel: *icsChannelFlag,
			Device:  "paper-hub",
		},
	}

	srv, err := server.NewServer(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize server: %v\n", err)
		os.Exit(1)
	}

	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Server exited: %v\n", err)
		os.Exit(1)
	}
}
