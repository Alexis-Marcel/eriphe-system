package main

// serviceArgs is a service's `container run` (dockerEngine's has Docker's)
// and, for http, an empty target: the node reaches the container on its
// address, once it has one (serviceAddress).
//
// Its network: with egress, the tool's default one (NAT, like Docker's
// default bridge); without, none for stdio, and for http an internal network
// of its own, which the Mac reaches and the tasks never share.
func (e appleEngine) serviceArgs(spec serviceRunSpec) ([]string, error) {
	args := []string{"run", "--rm", "--progress", "none", "--name", spec.name, "--label", "eriphe.service=" + spec.service}
	if spec.stdio {
		args = append(args, "-i")
	}
	args = append(args, "--user", spec.user, "--cap-drop", "ALL", "--ulimit", "nproc=256",
		"--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=256m")
	if !spec.writable {
		args = append(args, "--read-only")
	}
	args = append(args, appleResources(e.o.serviceMemory, "")...)
	switch {
	case spec.egress:
		// The default network.
	case spec.stdio:
		args = append(args, "--network", "none")
	default:
		network := "eriphe-svc-" + spec.service
		if _, err := e.createNetwork(network, "eriphe.service="+spec.service, false); err != nil {
			return nil, err
		}
		args = append(args, "--network", network)
	}
	mounts, err := mountArgs(spec.mounts)
	if err != nil {
		return nil, err
	}
	args = append(args, mounts...)
	args = append(args, envArgs(spec.env)...)
	args = append(args, spec.image)
	return append(args, spec.args...), nil
}

func (e appleEngine) runService(spec serviceRunSpec) (*engineRun, string, error) {
	args, err := e.serviceArgs(spec)
	if err != nil {
		return nil, "", err
	}
	r, err := startCLI(e.o.docker, args, spec.env, spec.stdio, spec.stderr)
	return r, "", err
}
