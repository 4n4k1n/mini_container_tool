package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// child (stage 1) lost its capabilities by exec'ing as an unmapped uid. It
// waits on fd 3 for the parent to write its id maps, then re-execs: the second
// exec runs as uid 0 and regains full caps for child2.
func child() {
	if sync := os.NewFile(3, "sync"); sync != nil {
		sync.Read(make([]byte, 1))
		sync.Close()
	}
	args := append([]string{"/proc/self/exe", "child2"}, os.Args[2:]...)
	must(syscall.Exec("/proc/self/exe", args, os.Environ()))
}

// child2 is stage 2: uid 0 with full caps, so it can mount and pivot_root.
func child2() {
	// read spec passed from parent
	f, err := os.Open(os.Args[2])
	must(err)
	var s spec
	must(json.NewDecoder(f).Decode(&s))
	f.Close()

	mountOverlay(s.Layers)
	pivotRoot("/tmp/container-work/merged")

	must(syscall.Sethostname([]byte(s.Hostname)))

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

func mountOverlay(layers []string) {
	// our own tmpfs, so overlayfs gets the xattrs userxattr mode needs
	workBase := "/tmp/container-work"
	os.RemoveAll(workBase)
	os.MkdirAll(workBase, 0755)
	must(syscall.Mount("tmpfs", workBase, "tmpfs", 0, ""))

	upper := workBase + "/upper"
	work := workBase + "/work"
	merged := workBase + "/merged"
	os.MkdirAll(upper, 0755)
	os.MkdirAll(work, 0755)
	os.MkdirAll(merged, 0755)

	// overlayfs wants the top layer first
	lowerdirs := make([]string, len(layers))
	for i, l := range layers {
		lowerdirs[len(layers)-1-i] = l
	}
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s,userxattr", strings.Join(lowerdirs, ":"), upper, work)

	// keep our mounts off the host
	must(syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""))
	must(syscall.Mount("overlay", merged, "overlay", 0, opts))

	// the image's resolv.conf is a dead symlink; we share the host net
	// namespace, so give it the host's working resolver
	if data, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		dst := filepath.Join(merged, "etc/resolv.conf")
		os.Remove(dst)
		must(os.WriteFile(dst, data, 0644))
	}

	// mount /proc before pivot_root, or the kernel rejects the new proc
	must(os.MkdirAll(filepath.Join(merged, "proc"), 0755))
	must(syscall.Mount("proc", filepath.Join(merged, "proc"), "proc", 0, ""))
}

func pivotRoot(newRoot string) {
	must(os.MkdirAll(filepath.Join(newRoot, "oldrootfs"), 0700))
	must(syscall.PivotRoot(newRoot, filepath.Join(newRoot, "oldrootfs")))
	must(os.Chdir("/"))
	must(syscall.Unmount("/oldrootfs", syscall.MNT_DETACH))
	must(os.Remove("/oldrootfs"))
}
