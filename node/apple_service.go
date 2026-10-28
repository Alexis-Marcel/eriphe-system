package main

import (
	"errors"
	"os"
)

// appleServiceArgs is a service's `container run` (service.go has Docker's),
// the environment of that command and, for http, an empty target: the node
// reaches the container on its address, once it has one (runOnce).
//
// Its network: with egress, the tool's default one (NAT, like Docker's
// default bridge); without, none for stdio, and for http an internal network
// of its own, which the Mac reaches and the tasks never share.
func (n *node) appleServiceArgs(name string, spec serviceSpec, user string, uid, gid int) (args, env []string, target string, err error) {
	args = []string{"run", "--rm", "--progress", "none", "--name", serviceContainer(name), "--label", "eriphe.service=" + name}
	if spec.Transport == "stdio" {
		args = append(args, "-i")
	}
	args = append(args, "--user", user, "--cap-drop", "ALL", "--ulimit", "nproc=256",
		"--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=256m")
	if !spec.Writable {
		args = append(args, "--read-only")
	}
	args = append(args, n.appleResources(n.opt.serviceMemory, "")...)
	switch {
	case spec.Egress:
		// The default network.
	case spec.Transport == "stdio":
		args = append(args, "--network", "none")
	default:
		network := "eriphe-svc-" + name
		if _, err := n.createNetwork(network, "eriphe.service="+name, false); err != nil {
			return nil, nil, "", err
		}
		args = append(args, "--network", network)
	}
	if spec.Transport == "http" && (spec.Port < 1 || spec.Port > 65535) {
		return nil, nil, "", errors.New("an http service needs its port")
	}
	if spec.Volume {
		dir := n.serviceDir(name)
		mount, err := n.bindMount(dir, "/data")
		if err != nil {
			return nil, nil, "", err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, nil, "", err
		}
		os.Chown(dir, uid, gid)
		args = append(args, mount...)
	}
	env = os.Environ()
	for k, v := range spec.Env {
		args = append(args, "-e", k)
		env = append(env, k+"="+v)
	}
	args = append(args, spec.Image)
	args = append(args, spec.Args...)
	return args, env, "", nil
}
