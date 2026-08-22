package main

import (
	"fmt"
	"os"
	qrcode "github.com/skip2/go-qrcode"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: gen_qr <url> <out.png>")
		return
	}
	err := qrcode.WriteFile(os.Args[1], qrcode.Medium, 320, os.Args[2])
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	fmt.Println("Saved QR to", os.Args[2])
}
