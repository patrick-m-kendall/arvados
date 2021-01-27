// Copyright (C) The Arvados Authors. All rights reserved.
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"git.arvados.org/arvados.git/lib/controller/rpc"
	"git.arvados.org/arvados.git/sdk/go/arvados"
)

// shellCommand connects the terminal to an interactive shell on a
// running container.
type shellCommand struct{}

func (shellCommand) RunCommand(prog string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	f := flag.NewFlagSet(prog, flag.ContinueOnError)
	f.SetOutput(stderr)
	f.Usage = func() {
		fmt.Fprint(stderr, prog+`: open an interactive shell on a running container.

Usage: `+prog+` [options] [username@]container-uuid [ssh-options] [remote-command [args...]]

Options:
`)
		f.PrintDefaults()
	}
	detachKeys := f.String("detach-keys", "ctrl-],ctrl-]", "set detach key sequence, as in docker-attach(1)")
	err := f.Parse(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	if f.NArg() < 1 {
		f.Usage()
		return 2
	}
	target := f.Args()[0]
	if !strings.Contains(target, "@") {
		target = "root@" + target
	}
	sshargs := f.Args()[1:]

	// Try setting up a tunnel, and exit right away if it
	// fails. This tunnel won't get used -- we'll set up a new
	// tunnel when running as SSH client's ProxyCommand child --
	// but in most cases where the real tunnel setup would fail,
	// we catch the problem earlier here. This makes it less
	// likely that an error message about tunnel setup will get
	// hidden behind noisy errors from SSH client like this:
	//
	// [useful tunnel setup error message here]
	// kex_exchange_identification: Connection closed by remote host
	// Connection closed by UNKNOWN port 65535
	// exit status 255
	exitcode := connectSSHCommand{}.RunCommand(
		"arvados-client connect-ssh",
		[]string{"-detach-keys=" + *detachKeys, "-probe-only=true", target},
		&bytes.Buffer{}, &bytes.Buffer{}, stderr)
	if exitcode != 0 {
		return exitcode
	}

	selfbin, err := os.Readlink("/proc/self/exe")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	sshargs = append([]string{
		"ssh",
		"-o", "ProxyCommand " + selfbin + " connect-ssh -detach-keys=" + shellescape(*detachKeys) + " " + shellescape(target),
		"-o", "StrictHostKeyChecking no",
		target},
		sshargs...)
	sshbin, err := exec.LookPath("ssh")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	err = syscall.Exec(sshbin, sshargs, os.Environ())
	fmt.Fprintf(stderr, "exec(%q) failed: %s\n", sshbin, err)
	return 1
}

// connectSSHCommand connects stdin/stdout to a container's gateway
// server (see lib/crunchrun/ssh.go).
//
// It is intended to be invoked with OpenSSH client's ProxyCommand
// config.
type connectSSHCommand struct{}

func (connectSSHCommand) RunCommand(prog string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	f := flag.NewFlagSet(prog, flag.ContinueOnError)
	f.SetOutput(stderr)
	f.Usage = func() {
		fmt.Fprint(stderr, prog+`: connect to the gateway service for a running container.

Usage: `+prog+` [options] [username@]container-uuid

Options:
`)
		f.PrintDefaults()
	}
	probeOnly := f.Bool("probe-only", false, "do not transfer IO, just exit 0 immediately if tunnel setup succeeds")
	detachKeys := f.String("detach-keys", "", "set detach key sequence, as in docker-attach(1)")
	if err := f.Parse(args); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	} else if f.NArg() != 1 {
		f.Usage()
		return 2
	}
	targetUUID := f.Args()[0]
	loginUsername := "root"
	if i := strings.Index(targetUUID, "@"); i >= 0 {
		loginUsername = targetUUID[:i]
		targetUUID = targetUUID[i+1:]
	}
	if os.Getenv("ARVADOS_API_HOST") == "" || os.Getenv("ARVADOS_API_TOKEN") == "" {
		fmt.Fprintln(stderr, "fatal: ARVADOS_API_HOST and ARVADOS_API_TOKEN environment variables are not set")
		return 1
	}
	insecure := os.Getenv("ARVADOS_API_HOST_INSECURE")
	rpcconn := rpc.NewConn("",
		&url.URL{
			Scheme: "https",
			Host:   os.Getenv("ARVADOS_API_HOST"),
		},
		insecure == "1" || insecure == "yes" || insecure == "true",
		func(context.Context) ([]string, error) {
			return []string{os.Getenv("ARVADOS_API_TOKEN")}, nil
		})
	if strings.Contains(targetUUID, "-xvhdp-") {
		crs, err := rpcconn.ContainerRequestList(context.TODO(), arvados.ListOptions{Limit: -1, Filters: []arvados.Filter{{"uuid", "=", targetUUID}}})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if len(crs.Items) < 1 {
			fmt.Fprintf(stderr, "container request %q not found\n", targetUUID)
			return 1
		}
		cr := crs.Items[0]
		if cr.ContainerUUID == "" {
			fmt.Fprintf(stderr, "no container assigned, container request state is %s\n", strings.ToLower(string(cr.State)))
			return 1
		}
		targetUUID = cr.ContainerUUID
		fmt.Fprintln(stderr, "connecting to container", targetUUID)
	} else if !strings.Contains(targetUUID, "-dz642-") {
		fmt.Fprintf(stderr, "target UUID is not a container or container request UUID: %s\n", targetUUID)
		return 1
	}
	sshconn, err := rpcconn.ContainerSSH(context.TODO(), arvados.ContainerSSHOptions{
		UUID:          targetUUID,
		DetachKeys:    *detachKeys,
		LoginUsername: loginUsername,
	})
	if err != nil {
		fmt.Fprintln(stderr, "error setting up tunnel:", err)
		return 1
	}
	defer sshconn.Conn.Close()

	if *probeOnly {
		return 0
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		defer cancel()
		_, err := io.Copy(stdout, sshconn.Conn)
		if err != nil && ctx.Err() == nil {
			fmt.Fprintf(stderr, "receive: %v\n", err)
		}
	}()
	go func() {
		defer cancel()
		_, err := io.Copy(sshconn.Conn, stdin)
		if err != nil && ctx.Err() == nil {
			fmt.Fprintf(stderr, "send: %v\n", err)
		}
	}()
	<-ctx.Done()
	return 0
}

func shellescape(s string) string {
	return "'" + strings.Replace(s, "'", "'\\''", -1) + "'"
}
