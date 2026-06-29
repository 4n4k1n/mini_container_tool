package main

import "os"

type spec struct {
	Layers   []string
	Cmd      []string
	Env      []string
	WorkDir  string
	Hostname string
}

func main() {
	usageCheck(len(os.Args), 2, "Usage:  ./container COMMAND")

	switch os.Args[1] {
	case "run":
		usageCheck(len(os.Args), 3, "Usage:  ./container run IMAGE [COMMAND] [ARG...]")
		parent()
	case "child":
		child()
	default:
		panic("unknown command")
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func usageCheck(argc, required int, message string) {
	if argc < required {
		println(message)
		os.Exit(1)
	}
}
