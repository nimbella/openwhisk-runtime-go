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

	// validate that the function conforms to the supported interfaces
	if err := validate(Main); err != nil {
		fmt.Fprintf(os.Stderr, "Function does not conform to supported type: %s\n", err.Error())
		fmt.Fprintf(out, `{"ok": false}%s`, "\n")
		os.Exit(1)
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
		output, err := execute(Main, inbuf)
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
func execute(f interface{}, in []byte) ([]byte, error) {
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
	output, err := invoke(ctx, f, input["value"])
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

	// Only build the context if the customer provided enough parameters in their function to require one.
	ctx = buildContext(ctx)

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

// buildContext takes the context that is being used to invoke the user's function and adds the "context" data to it.
//
// Go's convention for contexts is to define a struct in the package you want to build contexts from and use a constant
// instance of that struct as the key and define an exported function that other packages can use to retrieve a struct
// with the data you want to convey from the context.
// See https://github.com/aws/aws-lambda-go/blob/main/lambdacontext/context.go for an example of this.
// If you need more than one key per package, you can define a struct type that you can put something like a string in
// so that you can differentiate constant instances of the structs.
// See https://github.com/golang/go/blob/master/src/net/http/server.go for an example of this.
//
// At DO, we don't yet offer a library our users can import as they write their functions, so we can't follow this
// pattern right now. Therefore, the naming convention we use for our context keys doesn't matter. In the future, we can
// change this by creating a library that our customers can use, at which point we will no longer have multiple context
// keys and the single remaining context key will no longer be a string. Therefore, while we do need to communicate
// these string keys to our customers for now, the naming convention we use for the strings doesn't matter. We've chosen
// this-case arbitrarily.
func buildContext(ctx context.Context) context.Context {
	// Unlike other programming languages we provide runtimes for, deadline is skipped because Go already has the
	// built-in context deadlines, so our customers already have a way (which is better than if we added a value here)
	// of retrieving the deadline.
	ctx = context.WithValue(ctx, "function-name", os.Getenv("__OW_ACTION_NAME"))
	ctx = context.WithValue(ctx, "function-version", os.Getenv("__OW_ACTION_VERSION"))
	ctx = context.WithValue(ctx, "activation-id", os.Getenv("__OW_ACTIVATION_ID"))
	ctx = context.WithValue(ctx, "request-id", os.Getenv("__OW_TRANSACTION_ID"))
	ctx = context.WithValue(ctx, "api-host", os.Getenv("__OW_API_HOST"))
	ctx = context.WithValue(ctx, "api-key", os.Getenv("__OW_API_KEY"))
	ctx = context.WithValue(ctx, "namespace", os.Getenv("__OW_NAMESPACE"))

	return ctx
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
