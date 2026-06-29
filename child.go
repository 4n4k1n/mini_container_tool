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

func child() {
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
	// mount our own tmpfs for upper/work/merged — tmpfs owned by this user
	// namespace supports xattrs, which overlayfs needs for userxattr mode
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

	// lowerdir: top layer first (overlayfs priority order)
	lowerdirs := make([]string, len(layers))
	for i, l := range layers {
		lowerdirs[len(layers)-1-i] = l
	}
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s,userxattr", strings.Join(lowerdirs, ":"), upper, work)

	// stop mounts leaking to host
	must(syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""))
	must(syscall.Mount("overlay", merged, "overlay", 0, opts))

	// mount /proc before pivot_root — required in unprivileged user namespaces
	// (kernel needs a visible proc instance before allowing a new one to be mounted)
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
