package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type TestTypeIn struct {
	Foo string `json:"foo,omitempy"`
}

type TestTypeOut struct {
	Bar string `json:"bar,omitempy"`
}

// cleanEnv removes all environment keys that are set by execute potentially.
func cleanEnv() {
	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if strings.HasPrefix(parts[0], "__OW_") {
			os.Unsetenv(parts[0])
		}
	}
}

func TestExecuteParsesEnvAndArgs(t *testing.T) {
	defer cleanEnv()

	f := func(arg map[string]interface{}) map[string]interface{} {
		env := make(map[string]string)
		for _, e := range os.Environ() {
			parts := strings.SplitN(e, "=", 2)
			if strings.HasPrefix(parts[0], "__OW_") {
				env[parts[0]] = parts[1]
			}
		}

		return map[string]interface{}{
			"env": env,
			"arg": arg,
		}
	}
	in := []byte(`{"foo":"baz","value":{"testkey":"testvalue"},"key1":"val1","invalid":1}`)
	want := []byte(`{"arg":{"testkey":"testvalue"},"env":{"__OW_FOO":"baz","__OW_KEY1":"val1"}}`)

	out, err := execute(f, nil, in)
	assert.NoError(t, err)
	assert.Equal(t, string(want), string(out))
}

func TestExecuteParsesDeadline(t *testing.T) {
	defer cleanEnv()

	f := func(ctx context.Context) map[string]string {
		deadline, _ := ctx.Deadline()
		return map[string]string{
			"deadline": deadline.String(),
		}
	}

	deadline := time.Now().Add(10 * time.Second).UnixMilli()
	in := []byte(fmt.Sprintf(`{"deadline":"%d","value":{"testkey":"testvalue"}}`, deadline))
	// Rebuilding the time from milliseconds is important for the comparison.
	want := []byte(fmt.Sprintf(`{"deadline":%q}`, time.UnixMilli(deadline).String()))

	out, err := execute(f, nil, in)
	assert.NoError(t, err)
	assert.Equal(t, string(want), string(out))
}

func TestExecuteDefaultsToNoDeadline(t *testing.T) {
	defer cleanEnv()

	type ret struct {
		Deadline *time.Time `json:"deadline,omitempty"`
	}
	f := func(ctx context.Context) ret {
		deadline, ok := ctx.Deadline()
		if !ok {
			return ret{}
		}
		return ret{Deadline: &deadline}
	}

	in := []byte(`{"value":{"testkey":"testvalue"}}`)
	// We expect an empty object since the deadline should be unset.
	want := []byte("{}")

	out, err := execute(f, nil, in)
	assert.NoError(t, err)
	assert.Equal(t, string(want), string(out))
}

func TestInvoke(t *testing.T) {
	inBytes := []byte(`{"foo":"baz"}`)
	wantValue := []byte(`{"bar":"baz"}`)
	wantNothing := []byte("{}")
	wantError := []byte(fmt.Sprintf(`{"error": %q}`, assert.AnError.Error()))

	tests := []struct {
		name string
		f    interface{}
		want []byte
	}{{
		name: "backwards compat",
		f: func(in map[string]interface{}) map[string]interface{} {
			return map[string]interface{}{"bar": "baz"}
		},
		want: wantValue,
	}, {
		name: "func (): return nothing",
		f:    func() {},
		want: wantNothing,
	}, {
		name: "func () error: return nil",
		f:    func() error { return nil },
		want: wantNothing,
	}, {
		name: "func () error: return error",
		f:    func() error { return assert.AnError },
		want: wantError,
	}, {
		name: "func (TIn) error: return nil",
		f:    func(in TestTypeIn) error { return nil },
		want: wantNothing,
	}, {
		name: "func () (TOut, error): returning value",
		f: func() (TestTypeOut, error) {
			return TestTypeOut{Bar: "baz"}, nil
		},
		want: wantValue,
	}, {
		name: "func () (TOut, error): returning error",
		f: func() (TestTypeOut, error) {
			return TestTypeOut{}, assert.AnError
		},
		want: wantError,
	}, {
		name: "func (Tin) (TOut, error): returning value", // not in Lambdas list
		f: func(in *TestTypeIn) (*TestTypeOut, error) {
			return &TestTypeOut{Bar: in.Foo}, nil
		},
		want: wantValue,
	}, {
		name: "func (Tin) (TOut, error): returning both, error takes precedence",
		f: func(in *TestTypeIn) (*TestTypeOut, error) {
			return &TestTypeOut{Bar: in.Foo}, assert.AnError
		},
		want: wantError,
	}, {
		name: "func (context.Context) error: return nil",
		f:    func(context.Context) error { return nil },
		want: wantNothing,
	}, {
		name: "func (context.Context) error: return error",
		f:    func(context.Context) error { return assert.AnError },
		want: wantError,
	}, {
		name: "func (context.Context, Tin) error: return nil",
		f:    func(context.Context, TestTypeIn) error { return nil },
		want: wantNothing,
	}, {
		name: "func (context.Context, Tin) error: return error",
		f:    func(context.Context, TestTypeIn) error { return assert.AnError },
		want: wantError,
	}, {
		name: "func (context.Context) (Tout, error): return value",
		f:    func(context.Context) (TestTypeOut, error) { return TestTypeOut{Bar: "baz"}, nil },
		want: wantValue,
	}, {
		name: "func (context.Context) (Tout, error): return error",
		f:    func(context.Context) (TestTypeOut, error) { return TestTypeOut{Bar: "baz"}, assert.AnError },
		want: wantError,
	}, {
		name: "func (context.Context, Tin) (Tout, error): return value",
		f:    func(ctx context.Context, in TestTypeIn) (TestTypeOut, error) { return TestTypeOut{Bar: in.Foo}, nil },
		want: wantValue,
	}, {
		name: "func (context.Context, Tin) (Tout, error): return error",
		f: func(ctx context.Context, in TestTypeIn) (TestTypeOut, error) {
			return TestTypeOut{Bar: in.Foo}, assert.AnError
		},
		want: wantError,
	}, {
		name: "panic: return error",
		f: func() {
			panic("test")
		},
		want: []byte(`{"error":"function panicked: test"}`),
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := invoke(context.Background(), tt.f, inBytes)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, out)
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name      string
		f         interface{}
		wantError bool
	}{{
		name: "backwards compat",
		f: func(in map[string]interface{}) map[string]interface{} {
			return map[string]interface{}{"bar": "baz"}
		},
	}, {
		name: "func ()",
		f:    func() {},
	}, {
		name: "func () error",
		f:    func() error { return nil },
	}, {
		name: "func (TIn) error",
		f:    func(in TestTypeIn) error { return nil },
	}, {
		name: "func () (TOut, error)",
		f: func() (TestTypeOut, error) {
			return TestTypeOut{Bar: "baz"}, nil
		},
	}, {
		name: "func (Tin) (TOut, error)", // not in Lambdas list
		f: func(in *TestTypeIn) (*TestTypeOut, error) {
			return &TestTypeOut{Bar: in.Foo}, nil
		},
	}, {
		name: "func (context.Context) error",
		f:    func(context.Context) error { return nil },
	}, {
		name: "func (context.Context, Tin) error",
		f:    func(context.Context, TestTypeIn) error { return nil },
	}, {
		name: "func (context.Context) (Tout, error)",
		f:    func(context.Context) (TestTypeOut, error) { return TestTypeOut{Bar: "baz"}, nil },
	}, {
		name: "func (context.Context, Tin) (Tout, error)",
		f:    func(ctx context.Context, in TestTypeIn) (TestTypeOut, error) { return TestTypeOut{Bar: in.Foo}, nil },
	}, {
		name:      "too many arguments",
		f:         func(context.Context, TestTypeIn, string) { return },
		wantError: true,
	}, {
		name:      "wrong arguments",
		f:         func(TestTypeIn, context.Context) { return },
		wantError: true,
	}, {
		name:      "too many return values",
		f:         func() (string, string, string) { return "", "", "" },
		wantError: true,
	}, {
		name:      "wrong return values",
		f:         func() (error, string) { return assert.AnError, "" },
		wantError: true,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(tt.f)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestInvokeHttpHandler(t *testing.T) {
	defaultRequest := owHttpRequest{
		Method: "get",
		Headers: map[string]string{
			"Testheader": "Testvalue",
		},
		Path:  "/this/is/a/testpath",
		Query: "query=test&query2=test2",
		Body:  "testbody",
	}

	tests := []struct {
		name    string
		request owHttpRequest
		f       func(http.ResponseWriter, *http.Request)
		want    []byte
	}{{
		name:    "empty handler",
		request: defaultRequest,
		f:       func(rw http.ResponseWriter, req *http.Request) {},
		want:    []byte(`{"body":""}`),
	}, {
		name:    "handler with all functionality",
		request: defaultRequest,
		f: func(rw http.ResponseWriter, req *http.Request) {
			rw.Header().Add("foo", "bar")
			rw.WriteHeader(http.StatusNotFound)
			fmt.Fprint(rw, "Hello world!")
		},
		want: []byte(`{"headers":{"Foo":"bar"},"statusCode":404,"body":"Hello world!"}`),
	}, {
		name:    "checking the request",
		request: defaultRequest,
		f: func(rw http.ResponseWriter, req *http.Request) {
			body, err := ioutil.ReadAll(req.Body)
			assert.NoError(t, err)

			for k := range req.Header {
				rw.Header().Add(k, req.Header.Get(k))
			}
			rw.Write(body)
			rw.Write([]byte(" "))
			rw.Write([]byte(req.URL.String()))
		},
		want: []byte(`{"headers":{"Testheader":"Testvalue"},"body":"testbody http:///this/is/a/testpath?query=test\u0026query2=test2"}`),
	}, {
		name: "handling base64",
		request: owHttpRequest{
			Method:          "get",
			Body:            "aGVsbG8",
			IsBase64Encoded: true,
		},
		f: func(rw http.ResponseWriter, req *http.Request) {
			body, err := ioutil.ReadAll(req.Body)
			assert.NoError(t, err)
			rw.Write(body)
		},
		want: []byte(`{"body":"hello"}`),
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestJson, err := json.Marshal(tt.request)
			assert.NoError(t, err)

			got, err := invokeHttpHandler(context.Background(), tt.f, requestJson)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
