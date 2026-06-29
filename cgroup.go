package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

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
