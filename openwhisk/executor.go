/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package openwhisk

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/hashicorp/go-multierror"
	"golang.org/x/sync/errgroup"
)

// OutputGuard constant string
const (
	logSentinel = "XXX_THE_END_OF_A_WHISK_ACTIVATION_XXX"
	OutputGuard = logSentinel + "\n"

	remoteLogNotice = "Logs will be written to the specified remote location. Only errors in doing so will be surfaced here."

	logDestinationServiceEnv = "LOG_DESTINATION_SERVICE"
	logtailTokenEnv          = "LOGTAIL_SOURCE_TOKEN"
	papertrailTokenEnv       = "PAPERTRAIL_TOKEN"
	datadogSiteEnv           = "DD_SITE"
	datadogApiKeyEnv         = "DD_API_KEY"

	// This value is somewhat arbitrarily set now. It's low'ish to allow the logs to be written
	// in parallel to the actual action running to amortize the timing cost of writing the logs
	// to the backend.
	logBatchInterval = 100 * time.Millisecond
	// Datadog limits the size of a batch to 5 MB, so we leave some slack to that.
	logBatchSizeLimit = 4 * 1024 * 1024
)

// DefaultTimeoutStart to wait for a process to start
var DefaultTimeoutStart = 5 * time.Millisecond

type activationMetadata struct {
	ActivationId string `json:"activation_id"`
	ActionName   string `json:"action_name"`
}

// Executor is the container and the guardian  of a child process
// It starts a command, feeds input and output, read logs and control its termination
type Executor struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Reader
	exited chan bool

	lines        chan LogLine
	consumeGrp   errgroup.Group
	remoteLogger RemoteLogger

	logout *os.File
	logerr *os.File
}

// NewExecutor creates a child subprocess using the provided command line,
// writing the logs in the given file.
// You can then start it getting a communication channel
func NewExecutor(logout *os.File, logerr *os.File, command string, env map[string]string, args ...string) (proc *Executor) {
	cmd := exec.Command(command, args...)
	cmd.Stdout = logout
	cmd.Stderr = logerr
	cmd.Env = []string{}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	Debug("env: %v", cmd.Env)
	if Debugging {
		cmd.Env = append(cmd.Env, "OW_DEBUG=/tmp/action.log")
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil
	}
	pipeOut, pipeIn, err := os.Pipe()
	if err != nil {
		return nil
	}
	cmd.ExtraFiles = []*os.File{pipeIn}
	output := bufio.NewReader(pipeOut)
	e := &Executor{
		cmd:    cmd,
		input:  input,
		output: output,
		exited: make(chan bool),
		logout: logout,
		logerr: logerr,
	}

	if env[logDestinationServiceEnv] != "" {
		if err := e.setupRemoteLogging(env); err != nil {
			fmt.Fprintf(logerr, "Failed to setup remote logging: %v\n", err)
			return nil
		}
	}

	return e
}

func (proc *Executor) setupRemoteLogging(env map[string]string) error {
	// A chunky buffer in order to not block execution of the function from dealing with log lines.
	proc.lines = make(chan LogLine, 128)

	proc.cmd.Stdout = nil
	stdout, err := proc.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	proc.consumeGrp.Go(func() error { return consumeStream("stdout", stdout, proc.lines) })

	proc.cmd.Stderr = nil
	stderr, err := proc.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}
	proc.consumeGrp.Go(func() error { return consumeStream("stderr", stderr, proc.lines) })

	// TODO(SERVERLESS-958): Support multiple destinations at once.
	switch env[logDestinationServiceEnv] {
	case "logtail":
		if env[logtailTokenEnv] == "" {
			return fmt.Errorf("%q has to be an environment variable of the action", logtailTokenEnv)
		}

		logger := defaultRemoteLogger()
		logger.url = "https://in.logtail.com"
		logger.headers = map[string]string{"Authorization": fmt.Sprintf("Bearer %s", env[logtailTokenEnv])}
		logger.format = FormatLogtail
		logger.batchType = batchTypeArray
		proc.remoteLogger = logger
	case "papertrail":
		if env[papertrailTokenEnv] == "" {
			return fmt.Errorf("%q has to be an environment variable of the action", papertrailTokenEnv)
		}

		logger := defaultRemoteLogger()
		logger.url = "https://logs.collector.solarwinds.com/v1/logs"
		logger.headers = map[string]string{"Authorization": fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(":"+env[papertrailTokenEnv])))}
		logger.format = FormatLogtail // TODO: Is there a better format for papertrail?
		logger.batchType = batchTypeNewline
		proc.remoteLogger = logger
	case "datadog":
		if env[datadogSiteEnv] == "" || env[datadogApiKeyEnv] == "" {
			return fmt.Errorf("%q and %q have to be an environment variable of the action", datadogSiteEnv, datadogApiKeyEnv)
		}

		logger := defaultRemoteLogger()
		logger.url = fmt.Sprintf("https://http-intake.logs.%s/api/v2/logs", env[datadogSiteEnv])
		logger.headers = map[string]string{"DD-API-KEY": env[datadogApiKeyEnv]}
		logger.format = FormatDatadog
		logger.batchType = batchTypeArray
		proc.remoteLogger = logger
	}
	return nil
}

func defaultRemoteLogger() *batchingHttpLogger {
	return &batchingHttpLogger{
		http:           http.DefaultClient,
		batchInterval:  logBatchInterval,
		batchSizeLimit: logBatchSizeLimit,
		execAfter:      time.AfterFunc,
	}
}

// consumeStream consumes a log stream into the given channel.
func consumeStream(streamName string, stream io.Reader, ch chan LogLine) error {
	s := bufio.NewScanner(stream)
	for s.Scan() {
		ch <- LogLine{
			Time:    time.Now(),
			Message: s.Text(),
			Stream:  streamName,
		}
	}
	return s.Err()
}

// Interact interacts with the underlying process
func (proc *Executor) Interact(in []byte) ([]byte, error) {
	if proc.lines == nil {
		// Remote logging is not configured. Just roundtrip and return immediately.
		return proc.roundtrip(in)
	}

	defer func() {
		// Write our own sentinels instead of forwarding from the child. This makes sure that any
		// error logs we might've written are captured correctly.
		proc.logout.WriteString(OutputGuard)
		proc.logerr.WriteString(OutputGuard)
	}()

	// Fetch metadata from the incoming parameters
	var metadata activationMetadata
	if err := json.Unmarshal(in, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse activation metadata: %w", err)
	}

	var grp errgroup.Group
	grp.Go(func() error {
		var sawStdoutSentinel, sawStdErrSentinel bool
		var errors error
		for line := range proc.lines {
			if line.Message == logSentinel {
				if line.Stream == "stdout" {
					sawStdoutSentinel = true
				} else {
					sawStdErrSentinel = true
				}
				if sawStdoutSentinel && sawStdErrSentinel {
					// We've seen both sentinels. Stop consuming until the next request.
					return errors
				}
				continue
			}

			line.ActivationId = metadata.ActivationId
			line.ActionName = metadata.ActionName
			if err := proc.remoteLogger.Send(line); err != nil {
				// We want to continue consuming all of the logs so we don't exit immediately
				// but surface the error eventually.
				errors = multierror.Append(errors, err)
			}
		}
		return errors
	})

	proc.logout.WriteString(remoteLogNotice)
	proc.logout.WriteString("\n")
	out, err := proc.roundtrip(in)

	// Wait for the streams to process completely.
	if err := grp.Wait(); err != nil {
		fmt.Fprintf(proc.logerr, "Failed to process logs: %s\n", strings.TrimSpace(err.Error()))
	}

	// Flush any potential leftovers.
	if err := proc.remoteLogger.Flush(); err != nil {
		fmt.Fprintf(proc.logerr, "Failed to flush logs: %s\n", strings.TrimSpace(err.Error()))
	}

	return out, err
}

// roundtrip writes the input to the subprocess and waits for a response.
func (proc *Executor) roundtrip(in []byte) ([]byte, error) {
	proc.input.Write(in)
	proc.input.Write([]byte("\n"))

	chout := make(chan []byte)
	go func() {
		out, err := proc.output.ReadBytes('\n')
		if err == nil {
			chout <- out
		} else {
			chout <- []byte{}
		}
	}()
	var err error
	var out []byte
	select {
	case out = <-chout:
		if len(out) == 0 {
			err = errors.New("no answer from the action")
		}
	case <-proc.exited:
		err = errors.New("command exited")
	}
	return out, err
}

// Exited checks if the underlying command exited
func (proc *Executor) Exited() bool {
	select {
	case <-proc.exited:
		return true
	default:
		return false
	}
}

// ActionAck is the expected data structure for the action acknowledgement
type ActionAck struct {
	Ok bool `json:"ok"`
}

// Start execution of the command
// if the flag ack is true, wait forever for an acknowledgement
// if the flag ack is false wait a bit to check if the command exited
// returns an error if the program fails
func (proc *Executor) Start(waitForAck bool) error {
	// start the underlying executable
	Debug("Start:")
	err := proc.cmd.Start()
	if err != nil {
		Debug("run: early exit")
		proc.cmd = nil // no need to kill
		return fmt.Errorf("command exited")
	}
	Debug("pid: %d", proc.cmd.Process.Pid)

	go func() {
		proc.cmd.Wait()
		proc.exited <- true
	}()

	// not waiting for an ack, so use a timeout
	if !waitForAck {
		select {
		case <-proc.exited:
			return fmt.Errorf("command exited")
		case <-time.After(DefaultTimeoutStart):
			return nil
		}
	}

	// wait for acknowledgement
	Debug("waiting for an ack")
	ack := make(chan error)
	go func() {
		out, err := proc.output.ReadBytes('\n')
		Debug("received ack %s", out)
		if err != nil {
			ack <- err
			return
		}
		// parse ack
		var ackData ActionAck
		err = json.Unmarshal(out, &ackData)
		if err != nil {
			ack <- err
			return
		}
		// check ack
		if !ackData.Ok {
			ack <- fmt.Errorf("The action did not initialize properly.")
			return
		}
		ack <- nil
	}()
	// wait for ack or unexpected termination
	select {
	// ack received
	case err = <-ack:
		return err
	// process exited
	case <-proc.exited:
		return fmt.Errorf("Command exited abruptly during initialization.")
	}
}

// Stop will kill the process
// and close the channels
func (proc *Executor) Stop() {
	Debug("stopping")
	if proc.cmd != nil {
		proc.cmd.Process.Kill()
		proc.consumeGrp.Wait()
		proc.cmd = nil
	}
}
