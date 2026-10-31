package main

import (
	"log"
	"os"
	"os/exec"
	"syscall"
)

const runCmd = "tyde"

func main() {
	_ = os.Remove(logPath()) // remove old logs
	_ = os.Remove(runnerLogPath())
	log.SetOutput(openRunnerLogWriter())
	launchEnv := os.Environ()

	for {
		logFile := logPath()
		if _, err := os.Stat(logFile); err == nil {
			crashFile := crashLogPath()
			err = os.Rename(logFile, crashFile)
			if err != nil {
				log.Println("Could not save crash file", crashFile)
			}
		}

		exe := exec.Command(runCmd)
		exe.Env = append(launchEnv, "FYNE_DESK_RUNNER=1")
		// logger will be closed at the end of this for loop
		logger := openLogWriter()
		exe.Stdout, exe.Stderr = logger, logger
		err := exe.Run()
		if err == nil {
			return
		}

		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			log.Println("Could not execute", runCmd, "command")
			return
		}

		if exit, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			status := exit.ExitStatus()
			if status == 0 {
				log.Println("Exiting Error 0")
				return
			} else if status == 512 { // X server unavailable
				log.Println("X server went away")
				return
			} else if status == 2 { // the Go runtime's exit code for an unrecovered panic
				log.Println("Crashed with a panic, restarting")
			} else {
				log.Println("Restart from status", status)
			}
		}

		// close before starting next run
		_ = logger.Close()
	}
}
