/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/sirupsen/logrus"
)

var (
	processLog = flag.String("process-log", "", "path to the log where stdout and stderr are streamed for the process we execute")
	markerFile = flag.String("marker-file", "", "file we write the return code of the process we execute once it has finished running")
)

func main() {
	flag.Parse()

	if flag.NArg() == 0 {
		logrus.Fatal("No command or args specified")
	}

	if *processLog == "" {
		logrus.Fatal("No path for a process log file specified")
	}

	if *markerFile == "" {
		logrus.Fatal("No path for a marker file specified")
	}

	processLogFile, err := os.Create(*processLog)
	if err != nil {
		logrus.WithError(err).Fatal("Could not open output process logfile")
	}
	output := io.MultiWriter(os.Stdout, processLogFile)
	logrus.SetOutput(output)

	executable := flag.Arg(0)
	arguments := []string{}
	if flag.NArg() > 1 {
		arguments = flag.Args()[1:]
	}
	command := exec.Command(executable, arguments...)
	command.Stderr = output
	command.Stdout = output
	if err := command.Start(); err != nil {
		if err := ioutil.WriteFile(*markerFile, []byte("127"), os.ModePerm); err != nil {
			logrus.WithError(err).Fatal("Could not write to marker file")
		}
		logrus.WithError(err).Fatal("Could not start the process")
	}

	commandErr := command.Wait()

	returnCode := "1"
	if commandErr == nil {
		returnCode = "0"
	} else if exitErr, ok := err.(*exec.ExitError); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			returnCode = strconv.Itoa(status.ExitStatus())
		}
	}

	if err := ioutil.WriteFile(*markerFile, []byte(returnCode), os.ModePerm); err != nil {
		logrus.WithError(err).Fatal("Could not write return code to marker file")
	}
	if commandErr != nil {
		logrus.WithError(err).Fatal("Wrapped process failed")
	}
}
