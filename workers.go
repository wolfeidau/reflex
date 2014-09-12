package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kr/pty"
	"gopkg.in/fsnotify.v1"
)

var seqCommands = &sync.Mutex{}

func walker(watcher *fsnotify.Watcher) filepath.WalkFunc {
	return func(path string, f os.FileInfo, err error) error {
		if err != nil || !f.IsDir() {
			return nil
		}
		if err := watcher.Add(path); err != nil {
			infoPrintf(-1, "Error while watching new path %s: %s", path, err)
		}
		return nil
	}
}

func broadcast(in <-chan string, outs []chan<- string) {
	for e := range in {
		for _, out := range outs {
			out <- e
		}
	}
}

func watch(root string, watcher *fsnotify.Watcher, names chan<- string, done chan<- error) {
	if err := filepath.Walk(root, walker(watcher)); err != nil {
		infoPrintf(-1, "Error while walking path %s: %s", root, err)
	}

	for {
		select {
		case e := <-watcher.Events:
			path := strings.TrimPrefix(e.Name, "./")
			if verbose {
				infoPrintln(-1, "fsnotify event:", e)
			}
			if e.Op&fsnotify.Chmod > 0 {
				continue
			}
			names <- path
			if e.Op&fsnotify.Create > 0 {
				if err := filepath.Walk(path, walker(watcher)); err != nil {
					infoPrintf(-1, "Error while walking path %s: %s", path, err)
				}
			}
			// TODO: Cannot currently remove fsnotify watches recursively, or for deleted files. See:
			// https://github.com/cespare/reflex/issues/13
			// https://github.com/go-fsnotify/fsnotify/issues/40
			// https://github.com/go-fsnotify/fsnotify/issues/41
		case err := <-watcher.Errors:
			done <- err
			return
		}
	}
}

// filterMatching passes on messages matching the regex/glob.
func filterMatching(in <-chan string, out chan<- string, reflex *Reflex) {
	for name := range in {
		if reflex.useRegex {
			if !reflex.regex.MatchString(name) {
				continue
			}
		} else {
			matches, err := filepath.Match(reflex.glob, name)
			if err != nil {
				infoPrintln(reflex.id, "Error matching glob:", err)
				continue
			}
			if !matches {
				continue
			}
		}

		if reflex.onlyFiles || reflex.onlyDirs {
			stat, err := os.Stat(name)
			if err != nil {
				continue
			}
			if (reflex.onlyFiles && stat.IsDir()) || (reflex.onlyDirs && !stat.IsDir()) {
				continue
			}
		}
		out <- name
	}
}

// batch receives realtime file notification events and batches them up. It's a bit tricky, but here's what
// it accomplishes:
// * When we initially get a message, wait a bit and batch messages before trying to send anything. This is
//	 because the file events come in quick bursts.
// * Once it's time to send, don't do it until the out channel is unblocked. In the meantime, keep batching.
//   When we've sent off all the batched messages, go back to the beginning.
func batch(in <-chan string, out chan<- string, reflex *Reflex) {
	for name := range in {
		reflex.backlog.Add(name)
		timer := time.NewTimer(300 * time.Millisecond)
	outer:
		for {
			select {
			case name := <-in:
				reflex.backlog.Add(name)
			case <-timer.C:
				for {
					select {
					case name := <-in:
						reflex.backlog.Add(name)
					case out <- reflex.backlog.Next():
						if reflex.backlog.RemoveOne() {
							break outer
						}
					}
				}
			}
		}
	}
}

// runEach runs the command on each name that comes through the names channel. Each {} is replaced by the name
// of the file. The output of the command is passed line-by-line to the stdout chan.
func runEach(names <-chan string, reflex *Reflex) {
	for name := range names {
		if reflex.startService {
			if reflex.done != nil {
				infoPrintln(reflex.id, "Killing service")
				terminate(reflex)
			}
			infoPrintln(reflex.id, "Starting service")
			runCommand(reflex, name, stdout)
		} else {
			runCommand(reflex, name, stdout)
			<-reflex.done
			reflex.done = nil
		}
	}
}

func terminate(reflex *Reflex) {
	reflex.mu.Lock()
	reflex.killed = true
	reflex.mu.Unlock()
	// Write ascii 3 (what you get from ^C) to the controlling pty.
	// (This won't do anything if the process already died as the write will simply fail.)
	reflex.tty.Write([]byte{3})

	timer := time.NewTimer(500 * time.Millisecond)
	sig := syscall.SIGINT
	for {
		select {
		case <-reflex.done:
			return
		case <-timer.C:
			if sig == syscall.SIGINT {
				infoPrintln(reflex.id, "Sending SIGINT signal...")
			} else {
				infoPrintln(reflex.id, "Sending SIGKILL signal...")
			}

			// Instead of killing the process, we want to kill its whole pgroup in order to clean up any children
			// the process may have created.
			if err := syscall.Kill(-1*reflex.cmd.Process.Pid, sig); err != nil {
				infoPrintln(reflex.id, "Error killing:", err)
				if err.(syscall.Errno) == syscall.ESRCH { // "no such process"
					return
				}
			}
			// After SIGINT doesn't do anything, try SIGKILL next.
			timer.Reset(500 * time.Millisecond)
			sig = syscall.SIGKILL
		}
	}
}

func replaceSubSymbol(command []string, subSymbol string, name string) []string {
	replacer := strings.NewReplacer(subSymbol, name)
	newCommand := make([]string, len(command))
	for i, c := range command {
		newCommand[i] = replacer.Replace(c)
	}
	return newCommand
}

// runCommand runs the given Command. All output is passed line-by-line to the stdout channel.
func runCommand(reflex *Reflex, name string, stdout chan<- OutMsg) {
	command := replaceSubSymbol(reflex.command, reflex.subSymbol, name)
	cmd := exec.Command(command[0], command[1:]...)
	reflex.cmd = cmd

	if flagSequential {
		seqCommands.Lock()
	}

	tty, err := pty.Start(cmd)
	if err != nil {
		infoPrintln(reflex.id, err)
		return
	}
	reflex.tty = tty

	go func() {
		scanner := bufio.NewScanner(tty)
		for scanner.Scan() {
			stdout <- OutMsg{reflex.id, scanner.Text()}
		}
		// Intentionally ignoring scanner.Err() for now.
		// Unfortunately, the pty returns a read error when the child dies naturally, so I'm just going to ignore
		// errors here unless I can find a better way to handle it.
	}()

	done := make(chan struct{})
	reflex.done = done
	go func() {
		err := cmd.Wait()
		reflex.mu.Lock()
		killed := reflex.killed
		reflex.mu.Unlock()
		if !killed && err != nil {
			stdout <- OutMsg{reflex.id, fmt.Sprintf("(error exit: %s)", err)}
		}
		done <- struct{}{}
		if flagSequential {
			seqCommands.Unlock()
		}
	}()
}
