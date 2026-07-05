package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
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

	result := pullImage(args[0])

	cmd := append(result.Config.Entrypoint, result.Config.Cmd...)
	if len(args) > 1 {
		cmd = args[1:]
	}

	var layers []string
	for _, l := range result.Layers {
		layers = append(layers, l.Dir)
	}

	// the child reads its config from this temp file
	f, err := os.CreateTemp("", "container*.json")
	must(err)
	must(json.NewEncoder(f).Encode(spec{layers, cmd, result.Config.Env, result.Config.WorkingDir, *hostname}))
	f.Close()

	cgroupPath := ""
	if *memFlag != "" || *cpusFlag > 0 {
		cgroupPath, err = setupCgroup(*memFlag, *cpusFlag)
		must(err)
	}

	// lets us hold the child until its id maps are written
	syncR, syncW, err := os.Pipe()
	must(err)

	// No Uid/GidMappings here: unprivileged, Go could only map a single id.
	// writeIDMaps maps a full range below with newuidmap/newgidmap instead.
	c := exec.Command("/proc/self/exe", "child", f.Name())
	c.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
	}
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	c.ExtraFiles = []*os.File{syncR} // fd 3 in the child
	must(c.Start())
	syncR.Close()

	must(writeIDMaps(c.Process.Pid))

	if cgroupPath != "" {
		must(os.WriteFile(cgroupPath+"/cgroup.procs", []byte(strconv.Itoa(c.Process.Pid)), 0644))
	}

	syncW.Close() // maps are ready, let the child run

	c.Wait()
	os.Remove(f.Name())
	if cgroupPath != "" {
		os.Remove(cgroupPath)
	}
}

// writeIDMaps gives the child uid/gid 0 plus the user's subordinate range, so
// programs inside can switch ids (e.g. apt's _apt).
func writeIDMaps(pid int) error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	if err := idMap("newuidmap", pid, os.Getuid(), "/etc/subuid", u.Username); err != nil {
		return err
	}
	return idMap("newgidmap", pid, os.Getgid(), "/etc/subgid", u.Username)
}

func idMap(tool string, pid, hostID int, subFile, username string) error {
	start, count, err := parseSubID(subFile, username)
	if err != nil {
		return err
	}
	// container 0 -> our host id, then 1.. -> the subordinate range
	out, err := exec.Command(tool, strconv.Itoa(pid),
		"0", strconv.Itoa(hostID), "1",
		"1", strconv.Itoa(start), strconv.Itoa(count)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", tool, err, out)
	}
	return nil
}

// parseSubID reads a "name:start:count" line from /etc/subuid or /etc/subgid.
func parseSubID(path, username string) (start, count int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if p := strings.Split(line, ":"); len(p) == 3 && p[0] == username {
			start, _ = strconv.Atoi(p[1])
			count, _ = strconv.Atoi(p[2])
			return start, count, nil
		}
	}
	return 0, 0, fmt.Errorf("no entry for %q in %s", username, path)
}

func pullImage(image string) *ociregistry.PullResult {
	dest := "/tmp/oci/" + image
	metaPath := filepath.Join(dest, "pull.json")

	var result *ociregistry.PullResult
	if data, err := os.ReadFile(metaPath); err == nil {
		// reuse the cached pull
		fmt.Println("Found local image.")
		result = &ociregistry.PullResult{}
		must(json.Unmarshal(data, result))
	} else {
		fmt.Println("Pulling image from registry.")
		result, err = ociregistry.Pull(image, "latest", dest)
		must(err)
		data, err := json.Marshal(result)
		must(err)
		must(os.WriteFile(metaPath, data, 0644))
	}
	return result
}
