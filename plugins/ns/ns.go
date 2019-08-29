package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/mildred/nomadspace/ns"
)

func main() {
	flag.Parse()

	if len(flag.Args()) == 0 {
		ns := os.Getenv("env.meta.ns")
		if ns == "" {
			ns = os.Getenv("NOMADSPACE_ID")
		}
		if ns == "" {
			os.Exit(1)
		} else {
			fmt.Println(os.Getenv("env.meta.ns"))
		}
	} else {
		for i, arg := range flag.Args() {
			if i > 0 {
				fmt.Print("\n")
			}
			fmt.Print(ns.Ns(arg))
		}
	}
}
