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

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// OwExecutionEnv is the execution environment set at compile time
var OwExecutionEnv = ""

func main() {
	// check if the execution environment is correct
	if OwExecutionEnv != "" && OwExecutionEnv != os.Getenv("__OW_EXECUTION_ENV") {
		fmt.Println("Execution Environment Mismatch")
		fmt.Println("Expected: ", OwExecutionEnv)
		fmt.Println("Actual: ", os.Getenv("__OW_EXECUTION_ENV"))
		os.Exit(1)
	}

	// debugging
	var debug = os.Getenv("OW_DEBUG") != ""
	if debug {
		f, err := os.OpenFile("/tmp/action.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err == nil {
			log.SetOutput(f)
		}
		log.Printf("Environment: %v", os.Environ())
	}

	// input
	out := os.NewFile(3, "pipe")
	defer out.Close()
	reader := bufio.NewReader(os.Stdin)

	var function interface{} = Main
	var httpHandlerFunc http.HandlerFunc

	if httpFunc, ok := function.(http.HandlerFunc); ok {
		httpHandlerFunc = httpFunc
	} else if httpHandler, ok := function.(http.Handler); ok {
		httpHandlerFunc = httpHandler.ServeHTTP
	} else {
		// validate that the function conforms to the supported interfaces
		if err := validate(function); err != nil {
			fmt.Fprintf(os.Stderr, "Function does not conform to supported type: %s\n", err.Error())
			fmt.Fprintf(out, `{"ok": false}%s`, "\n")
			os.Exit(1)
		}
	}

	// acknowledgement of started action
	fmt.Fprintf(out, `{ "ok": true}%s`, "\n")
	if debug {
		log.Println("action started")
	}

	// read-eval-print loop
	for {
		// read one line
		inbuf, err := reader.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				log.Println(err)
			}
			break
		}
		if debug {
			log.Printf(">>>'%s'>>>", inbuf)
		}
		output, err := execute(function, httpHandlerFunc, inbuf)
		if err != nil {
			output = []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
		}
		if debug {
			log.Printf("<<<'%s'<<<", output)
		}
		fmt.Fprintf(out, "%s\n", output)

		fmt.Fprintln(os.Stdout, "XXX_THE_END_OF_A_WHISK_ACTIVATION_XXX")
		fmt.Fprintln(os.Stderr, "XXX_THE_END_OF_A_WHISK_ACTIVATION_XXX")
	}
}

// execute parses the input into a value to pass to f and environment to set
// for the duration of f and calls f with the respective value and environment.
func execute(f interface{}, httpHandler http.HandlerFunc, in []byte) ([]byte, error) {
	var input map[string]json.RawMessage
	if err := json.Unmarshal(in, &input); err != nil {
		return nil, fmt.Errorf("failed to parse input: %w", err)
	}

	// All values except "value" are expected to become environment variables.
	for k, v := range input {
		if k == "value" {
			continue
		}
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			os.Setenv("__OW_"+strings.ToUpper(k), s)
		}
	}

	ctx := context.Background()
	if deadline := os.Getenv("__OW_DEADLINE"); deadline != "" {
		// Setup a context that cancels at the given deadline.
		deadlineMillis, err := strconv.ParseInt(os.Getenv("__OW_DEADLINE"), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse deadline: %w", err)
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(context.Background(), time.UnixMilli(deadlineMillis))
		defer cancel()
	}

	// Process the value through the actual function
	var (
		output []byte
		err    error
	)
	if httpHandler == nil {
		output, err = invoke(ctx, f, input["value"])
	} else {
		output, err = invokeHttpHandler(ctx, httpHandler, input["value"])
	}
	if err != nil {
		return nil, err
	}

	// Sanitize output.
	output = bytes.ReplaceAll(output, []byte("\n"), []byte(""))
	return output, nil
}

var (
	errorInterface   = reflect.TypeOf((*error)(nil)).Elem()
	contextInterface = reflect.TypeOf((*context.Context)(nil)).Elem()
)

// validate validates a given generic function f for supported
func validate(f interface{}) error {
	fun := reflect.ValueOf(f)
	typ := fun.Type()

	if numIn := typ.NumIn(); numIn > 2 {
		return fmt.Errorf("at most 2 arguments are supported for the function, got %d", numIn)
	} else if numIn == 2 && !typ.In(0).Implements(contextInterface) {
		return fmt.Errorf("when passing 2 arguments, the first must be of type context.Context, got %s", typ.In(0).Name())
	}

	if numOut := typ.NumOut(); numOut > 2 {
		return fmt.Errorf("at most 2 return values are supported for the function, got %d", numOut)
	} else if numOut == 2 && !typ.Out(numOut-1).Implements(errorInterface) {
		return fmt.Errorf("when expecting 2 return values, the last must be of type error, got %s", typ.Out(numOut-1).Name())
	}

	return nil
}

// invoke calls a generic function f with the given JSON in bytes, which is assumed
// to be unmarshalable into a value argument of f, if present. If the function has a
// return value other than error, it's expected to be marshalable to JSON.
//
// All permutations of the signatures defined in buildArguments and handleReturnValues
// are supported.
func invoke(ctx context.Context, f interface{}, in []byte) (out []byte, err error) {
	defer func() {
		// Transform a panic into an error response.
		if p := recover(); p != nil {
			err := fmt.Errorf("function panicked: %v", p)
			out = []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
		}
	}()

	fun := reflect.ValueOf(f)

	arguments, err := buildArguments(ctx, fun, in)
	if err != nil {
		return nil, err
	}
	return handleReturnValues(fun.Call(arguments))
}

// buildArguments builds the arguments to call f.
//
// These argument signatures are supported:
// - ()
// - (context.Context)
// - (Tin)
// - (context.Context, Tin)
func buildArguments(ctx context.Context, f reflect.Value, in []byte) ([]reflect.Value, error) {
	typ := f.Type()
	numArgs := typ.NumIn()

	if numArgs == 0 {
		// No arguments, exit early.
		return nil, nil
	}

	if numArgs == 2 {
		// We know that the first argument must be the context and the second the value here.
		val := reflect.New(typ.In(1)).Interface()
		if err := json.Unmarshal(in, val); err != nil {
			return nil, fmt.Errorf("failed to unmarshal input value: %w", err)
		}
		return []reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(val).Elem()}, nil
	}

	// If there's only 1 argument, we need to figure out if it's only a context or only a value.
	if typ.In(0).Implements(contextInterface) {
		return []reflect.Value{reflect.ValueOf(ctx)}, nil
	}

	val := reflect.New(typ.In(0)).Interface()
	if err := json.Unmarshal(in, val); err != nil {
		return nil, fmt.Errorf("failed to unmarshal input value: %w", err)
	}
	return []reflect.Value{reflect.ValueOf(val).Elem()}, nil
}

// handleReturnValues handles the values returned from a reflected function's call.
//
// These return value signatures are supported:
// - <none>
// - error
// - Tout
// - (Tout, error)
func handleReturnValues(returns []reflect.Value) ([]byte, error) {
	if len(returns) == 0 {
		// If there's no return values, return a compatible empty JSON object.
		return []byte("{}"), nil
	}

	if len(returns) == 2 {
		if err := valueToError(returns[1]); err != nil {
			// Transform the function error into an actual error return from the function.
			// Return early as an error should always take precedence.
			return []byte(fmt.Sprintf(`{"error": %q}`, err.Error())), nil
		}

		ret, err := json.Marshal(returns[0].Interface())
		if err != nil {
			return nil, fmt.Errorf("failed to marshal output value: %w", err)
		}
		return ret, nil
	}

	// If there's only 1 return value, we need to figure out if it's only an error or only a value.
	if returns[0].Type().Implements(errorInterface) {
		if err := valueToError(returns[0]); err != nil {
			// Transform the function error into an actual error return from the function.
			// Return early as an error should always take precedence.
			return []byte(fmt.Sprintf(`{"error": %q}`, err.Error())), nil
		}
		return []byte("{}"), nil
	}

	ret, err := json.Marshal(returns[0].Interface())
	if err != nil {
		return nil, fmt.Errorf("failed to marshal output value: %w", err)
	}
	return ret, nil
}

// valueToError builds an error from the given reflect.Value, if there is one.
func valueToError(val reflect.Value) error {
	if err, ok := val.Interface().(error); ok && err != nil {
		return err
	}
	return nil
}

// owHttpRequest represents the metadata an HTTP request has when it comes via the
// web invocation path.
type owHttpRequest struct {
	Method  string            `json:"__ow_method,omitempty"`
	Headers map[string]string `json:"__ow_headers,omitempty"`
	Path    string            `json:"__ow_path,omitempty"`
	User    string            `json:"__ow_user,omitempty"`
	Body    string            `json:"__ow_body,omitempty"`
	Query   string            `json:"__ow_query,omitempty"` // Not available for now.
}

// owHttpResponse is a response as OW expects it from an action.
type owHttpResponse struct {
	Headers    map[string]string `json:"headers,omitempty"`
	StatusCode int               `json:"statusCode,omitempty"`
	Body       interface{}       `json:"body,omitempty"`
}

// responseRecorder is an implementation of http.ResponseWriter, which stores all writes
// in memory so it can be transformed into a response as OW understands it.
type responseRecorder struct {
	body    bytes.Buffer
	status  int
	headers http.Header
}

func (r *responseRecorder) Header() http.Header {
	if r.headers == nil {
		r.headers = make(map[string][]string, 1)
	}
	return r.headers
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	return r.body.Write(b)
}

func invokeHttpHandler(ctx context.Context, f func(http.ResponseWriter, *http.Request), in []byte) ([]byte, error) {
	// Parse the request.
	var r owHttpRequest
	if err := json.Unmarshal(in, &r); err != nil {
		return nil, fmt.Errorf("failed to parse request: %w", err)
	}
	// Transform it to *http.Request
	url, err := url.Parse("http://")
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}
	url.Path = r.Path
	url.RawQuery = r.Query
	req, err := http.NewRequestWithContext(ctx, r.Method, url.String(), strings.NewReader(r.Body))
	if err != nil {
		return nil, fmt.Errorf("failed to generate request: %w", err)
	}

	for k, v := range r.Headers {
		req.Header.Add(k, v)
	}

	recorder := &responseRecorder{}
	f(recorder, req)

	headers := make(map[string]string, len(recorder.headers))
	for k := range recorder.headers {
		headers[k] = recorder.headers.Get(k)
	}

	// Transform recorder (ResponseWriter) into a response OW can understand.
	response := owHttpResponse{
		Body:       recorder.body.String(),
		StatusCode: recorder.status,
		Headers:    headers,
	}
	return json.Marshal(response)
}
