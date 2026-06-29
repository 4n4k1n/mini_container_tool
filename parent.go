package main

import (
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/4n4k1n/ociregistry"
)

func parent() {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	memFlag := flags.String("memory", "", "memory limit (e.g. 256m, 1g)")
	cpusFlag := flags.Float64("cpus", 0, "cpu limit (e.g. 0.5, 2)")
	hostname := flags.String("hostname", "container", "container hostname")
	flags.Parse(os.Args[2:])

	args := flags.Args()
	usageCheck(len(args)+2, 3, "Usage:  ./container run IMAGE [COMMAND] [ARG...]")

	// pull image from registry
	image := args[0]
	result, err := ociregistry.Pull(image, "latest", "/tmp/oci/"+image)
	must(err)

	// build command: entrypoint + cmd, overridable from args
	cmd := append(result.Config.Entrypoint, result.Config.Cmd...)
	if len(args) > 1 {
		cmd = args[1:]
	}

	// extract layer dirs for overlayfs
	var layers []string
	for _, l := range result.Layers {
		layers = append(layers, l.Dir)
	}

	// write spec to temp file, child reads it after fork
	f, err := os.CreateTemp("", "container*.json")
	must(err)
	must(json.NewEncoder(f).Encode(spec{layers, cmd, result.Config.Env, result.Config.WorkingDir, *hostname}))
	f.Close()

	// set up cgroup before fork so we can write child PID after
	cgroupPath := ""
	if *memFlag != "" || *cpusFlag > 0 {
		cgroupPath, err = setupCgroup(*memFlag, *cpusFlag)
		must(err)
	}

	// re-exec self as child inside new namespaces
	c := exec.Command("/proc/self/exe", "child", f.Name())
	c.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
		UidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: os.Getuid(), Size: 1},
		},
		GidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: os.Getgid(), Size: 1},
		},
		GidMappingsEnableSetgroups: false,
	}
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	must(c.Start())

	// add child to cgroup after fork so we have its PID
	if cgroupPath != "" {
		must(os.WriteFile(cgroupPath+"/cgroup.procs", []byte(strconv.Itoa(c.Process.Pid)), 0644))
	}

	c.Wait()
	os.Remove(f.Name())
	if cgroupPath != "" {
		os.Remove(cgroupPath)
	}
}
