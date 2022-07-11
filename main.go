// Flatpak spawn simple reimplementation
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/godbus/dbus/v5"
)

func nullTerminatedByteString(s string) []byte {
	return append([]byte(s), 0)
}

// Version is the current value injected at build time.
var Version string

// Extract exit code from waitpid(2) status
func interpretWaitStatus(status uint32) (int, bool) {
	// From /usr/include/bits/waitstatus.h
	WTERMSIG := status & 0x7f
	WIFEXITED := WTERMSIG == 0

	if WIFEXITED {
		WEXITSTATUS := (status & 0xff00) >> 8
		return int(WEXITSTATUS), true
	}

	return 0, false
}

func runCommandSync(args []string) (error, int, bool) {

	// Connect to the dbus session to talk with flatpak-session-helper process.
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		log.Fatalln(err)
	}
	defer conn.Close()

	// Subscribe to HostCommandExited messages
	if err = conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.Flatpak.Development"),
		dbus.WithMatchMember("HostCommandExited"),
	); err != nil {
		log.Fatalln(err)
	}
	signals := make(chan *dbus.Signal, 1)
	conn.Signal(signals)

	// Spawn host command
	proxy := conn.Object("org.freedesktop.Flatpak", "/org/freedesktop/Flatpak/Development")

	var pid uint32
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalln(err)
	}

	cwdPath := nullTerminatedByteString(cwd)

	argv := make([][]byte, len(args))
	for i, arg := range args {
		argv[i] = nullTerminatedByteString(arg)
	}
	envs := map[string]string{"TERM": os.Getenv("TERM")}

	pty, err := createPty()
	if err != nil {
		log.Fatalln(err)
	}
	pty.Start()
	defer pty.Terminate()

	fds := map[uint32]dbus.UnixFD{
		0: dbus.UnixFD(pty.Stdin().Fd()),
		1: dbus.UnixFD(pty.Stdout().Fd()),
		2: dbus.UnixFD(pty.Stderr().Fd()),
	}

	flags := uint32(0)

	// Call command on the host
	err = proxy.Call("org.freedesktop.Flatpak.Development.HostCommand", 0,
		cwdPath, argv, fds, envs, flags,
	).Store(&pid)

	// an error occurred this early, most likely command not found.
	if err != nil {
		fmt.Println(err)
		return err, 127, true
	}

	// Wait for HostCommandExited to fire
	for message := range signals {
		waitStatus := message.Body[1].(uint32)
		status, exited := interpretWaitStatus(waitStatus)
		return nil, status, exited
	}

	return nil, 0, true
}

func main() {
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" {
		fmt.Fprintf(os.Stderr, "usage: %s command [arguments ...]", os.Args[0])
		return
	}

	// Version flag
	if os.Args[1] == "-v" || os.Args[1] == "--version" {
		fmt.Println(Version)
		return
	}

	_, exitCode, exited := runCommandSync(os.Args[1:])
	if exited {
		os.Exit(exitCode)
	}
}
