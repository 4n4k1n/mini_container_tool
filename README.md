# minicontainer

Minimal Linux container runtime in Go. Pulls OCI images from Docker Hub and runs them in isolated namespaces.

## Setup

```bash
go build -o container .
```

## Usage

```bash
sudo ./container run alpine
sudo ./container run alpine /bin/ls
```

## How it works

1. Pulls image layers from Docker Hub
2. Stacks layers with overlayfs
3. Forks into new PID, mount, and UTS namespaces
4. Pivots root to the merged overlay filesystem
5. Executes the image's command
