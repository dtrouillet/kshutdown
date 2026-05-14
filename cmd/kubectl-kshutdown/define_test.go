/*
Copyright 2026.

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
	"testing"
)

func TestParseLabelSelector(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]string
		wantErr bool
	}{
		{
			name:  "single pair",
			input: "app=foo",
			want:  map[string]string{"app": "foo"},
		},
		{
			name:  "multiple pairs",
			input: "app=foo,env=prod",
			want:  map[string]string{"app": "foo", "env": "prod"},
		},
		{
			name:  "value with dot",
			input: "app.kubernetes.io/part-of=payment",
			want:  map[string]string{"app.kubernetes.io/part-of": "payment"},
		},
		{
			name:  "value with equals sign in value",
			input: "key=a=b",
			want:  map[string]string{"key": "a=b"},
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "missing value",
			input:   "key",
			wantErr: true,
		},
		{
			name:    "empty key",
			input:   "=value",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLabelSelector(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseLabelSelector(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("key %q: got %q, want %q", k, got[k], v)
				}
			}
		})
	}
}
