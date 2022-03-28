package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

type TestTypeIn struct {
	Foo string `json:"foo,omitempy"`
}

type TestTypeOut struct {
	Bar string `json:"bar,omitempy"`
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
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := invoke(tt.f, inBytes)
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
