package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/4n4k1n/ociregistry"
)

type spec struct {
	Layers  []string
	Cmd     []string
	Env     []string
	WorkDir string
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
		panic("Unknown command")
	}
}

func parent() {
	// parse flags
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	memFlag := flags.String("memory", "", "memory limit (e.g. 256m, 1g)")
	cpusFlag := flags.Float64("cpus", 0, "cpu limit (e.g. 0.5, 2)")
	flags.Parse(os.Args[2:])
	args := flags.Args()
	usageCheck(len(args)+2, 3, "Usage:  ./container run IMAGE [COMMAND] [ARG...]")

	// pull image from registry
	image := args[0]
	result, err := ociregistry.Pull(image, "latest", "/tmp/oci/"+image)
	must(err)

	// append entrypoint and cmd into a command slice
	cmd := append(result.Config.Entrypoint, result.Config.Cmd...)
	if len(args) > 1 {
		cmd = args[1:]
	}

	// extracts directory paths for overlayfs
	var layers []string
	for _, l := range result.Layers {
		layers = append(layers, l.Dir)
	}

	// write spec to temp file, child reads it after fork
	f, err := os.CreateTemp("", "container*.json")
	must(err)
	must(json.NewEncoder(f).Encode(spec{layers, cmd, result.Config.Env, result.Config.WorkingDir}))
	f.Close()

	// set up cgroup before fork
	cgroupPath := ""
	if *memFlag != "" || *cpusFlag > 0 {
		cgroupPath, err = setupCgroup(*memFlag, *cpusFlag)
		must(err)
	}

	// re-exec self as child inside new namespaces
	c := exec.Command("/proc/self/exe", "child", f.Name())
	c.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
		// map our uid/gid to root inside the new user namespace so the
		// child can create namespaces and mount without real root
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

func setupCgroup(memory string, cpus float64) (string, error) {
	path := fmt.Sprintf("/sys/fs/cgroup/minicontainer/%d", os.Getpid())
	if err := os.MkdirAll(path, 0755); err != nil {
		return "", err
	}

	// enable controllers on parent cgroups
	os.WriteFile("/sys/fs/cgroup/cgroup.subtree_control", []byte("+memory +cpu"), 0644)
	os.WriteFile("/sys/fs/cgroup/minicontainer/cgroup.subtree_control", []byte("+memory +cpu"), 0644)

	if memory != "" {
		bytes, err := parseMemory(memory)
		if err != nil {
			return "", err
		}
		os.WriteFile(path+"/memory.max", []byte(strconv.FormatInt(bytes, 10)), 0644)
	}

	if cpus > 0 {
		quota := int(cpus * 100000)
		os.WriteFile(path+"/cpu.max", []byte(fmt.Sprintf("%d 100000", quota)), 0644)
	}

	return path, nil
}

func parseMemory(s string) (int64, error) {
	s = strings.ToLower(s)
	multipliers := map[byte]int64{'k': 1024, 'm': 1024 * 1024, 'g': 1024 * 1024 * 1024}
	last := s[len(s)-1]
	if mult, ok := multipliers[last]; ok {
		n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		return n * mult, err
	}
	return strconv.ParseInt(s, 10, 64)
}

func child() {
	// read spec passed from parent
	f, err := os.Open(os.Args[2])
	must(err)
	var s spec
	must(json.NewDecoder(f).Decode(&s))
	f.Close()

	// overlayfs dirs: upper=writable, work=scratch, merged=container view
	upper, work, merged := "/tmp/overlay/upper", "/tmp/overlay/work", "/tmp/overlay/merged"
	os.MkdirAll(upper, 0755)
	os.MkdirAll(work, 0755)
	os.MkdirAll(merged, 0755)

	// lowerdir: top layer first (overlayfs priority order)
	lowerdirs := make([]string, len(s.Layers))
	for i, l := range s.Layers {
		lowerdirs[len(s.Layers)-1-i] = l
	}
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", strings.Join(lowerdirs, ":"), upper, work)

	// stop mounts leaking to host
	must(syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""))
	// mount stacked layers into merged
	must(syscall.Mount("overlay", merged, "overlay", 0, opts))
	// fresh /proc for this pid namespace. must happen BEFORE pivot_root while the
	// host's /proc is still visible: in an unprivileged user namespace the kernel
	// (mount_too_revealing) only allows a new procfs mount when a fully-visible
	// proc instance already exists to compare against. it travels with the new root.
	must(os.MkdirAll(filepath.Join(merged, "proc"), 0755))
	must(syscall.Mount("proc", filepath.Join(merged, "proc"), "proc", 0, ""))
	// swap root to merged, park old root at oldrootfs
	must(os.MkdirAll(filepath.Join(merged, "oldrootfs"), 0700))
	must(syscall.PivotRoot(merged, filepath.Join(merged, "oldrootfs")))
	must(os.Chdir("/"))
	// hide host filesystem
	must(syscall.Unmount("/oldrootfs", syscall.MNT_DETACH))
	must(os.Remove("/oldrootfs"))

	cmd := exec.Command(s.Cmd[0], s.Cmd[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = s.Env
	if s.WorkDir != "" {
		cmd.Dir = s.WorkDir
	}
	if err := cmd.Run(); err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func usageCheck(argc int, required_argc int, message string) {
	if argc < required_argc {
		fmt.Println(message)
		os.Exit(1)
	}
}
