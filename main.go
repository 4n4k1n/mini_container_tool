package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	switch os.Args[1] {
	case "run":
		parent()
	case "child":
		child()
	default:
		panic("Unknown command")
	}
}

func parent() {
	fmt.Println("parent pid:", os.Getpid())

	image := os.Args[2]
	result, err := ociregistry.Pull(image, "latest", "/tmp/oci/"+image)
	must(err)

	cmd := append(result.Config.Entrypoint, result.Config.Cmd...)
	if len(os.Args) > 3 {
		cmd = os.Args[3:]
	}

	var layers []string
	for _, l := range result.Layers {
		layers = append(layers, l.Dir)
	}

	f, err := os.CreateTemp("", "container*.json")
	must(err)
	must(json.NewEncoder(f).Encode(spec{layers, cmd, result.Config.Env, result.Config.WorkingDir}))
	f.Close()

	c := exec.Command("/proc/self/exe", "child", f.Name())
	c.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
	}
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	os.Remove(f.Name())
}

func child() {
	fmt.Println("child pid:", os.Getpid())

	f, err := os.Open(os.Args[2])
	must(err)
	var s spec
	must(json.NewDecoder(f).Decode(&s))
	f.Close()

	upper, work, merged := "/tmp/overlay/upper", "/tmp/overlay/work", "/tmp/overlay/merged"
	os.MkdirAll(upper, 0755)
	os.MkdirAll(work, 0755)
	os.MkdirAll(merged, 0755)

	// lowerdir: top layer first
	lowerdirs := make([]string, len(s.Layers))
	for i, l := range s.Layers {
		lowerdirs[len(s.Layers)-1-i] = l
	}
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", strings.Join(lowerdirs, ":"), upper, work)

	must(syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""))
	must(syscall.Mount("overlay", merged, "overlay", 0, opts))
	must(os.MkdirAll(filepath.Join(merged, "oldrootfs"), 0700))
	must(syscall.PivotRoot(merged, filepath.Join(merged, "oldrootfs")))
	must(os.Chdir("/"))
	must(syscall.Unmount("/oldrootfs", syscall.MNT_DETACH))
	must(os.Remove("/oldrootfs"))
	must(syscall.Mount("proc", "proc", "proc", 0, ""))

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
