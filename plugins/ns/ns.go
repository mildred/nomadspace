package main

import (
	"flag"
	"fmt"

	"github.com/mildred/nomadspace/ns"
)

func main() {
	flag.Parse()
	for i, arg := range flag.Args() {
		if i > 0 {
			fmt.Print("\n")
		}
		fmt.Print(ns.Ns(arg))
	}
}
