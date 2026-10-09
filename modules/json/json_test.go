// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package json

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGiteaDBJSONUnmarshal(t *testing.T) {
	var m map[any]any
	err := UnmarshalHandleDoubleEncode(nil, &m)
	assert.NoError(t, err)
	err = UnmarshalHandleDoubleEncode([]byte(""), &m)
	assert.NoError(t, err)
}

func TestIndent(t *testing.T) {
	buf := &bytes.Buffer{}
	err := Indent(buf, []byte(`{"a":1}`), ">", "  ")
	assert.NoError(t, err)
	assert.Equal(t, `{
>  "a": 1
>}`, buf.String())
}

func TestNewDecoderDisallowUnknownFields(t *testing.T) {
	var value struct {
		Known string `json:"known"`
	}
	err := NewDecoderDisallowUnknownFields(strings.NewReader(`{"known":"value","unknown":true}`)).Decode(&value)
	assert.Error(t, err)
}

func TestNewDecoderDisallowUnknownFieldsStreamsValues(t *testing.T) {
	decoder := NewDecoderDisallowUnknownFields(strings.NewReader(`{"known":"value"}`))
	var value struct {
		Known string `json:"known"`
	}
	assert.NoError(t, decoder.Decode(&value))
	assert.Equal(t, "value", value.Known)
	assert.ErrorIs(t, decoder.Decode(&struct{}{}), io.EOF)
}
