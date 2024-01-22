// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsprpc

import (
	"context"
	"encoding/json"
	"fmt"

	"golang.org/x/tools/internal/gocommand"
)

func getGoEnv(ctx context.Context, env map[string]interface{}) (map[string]string, error) {
	var runEnv []string
	for k, v := range env {
		runEnv = append(runEnv, fmt.Sprintf("%s=%s", k, v))
	}
	runner := gocommand.Runner{}
	output, err := runner.Run(ctx, gocommand.Invocation{
		Verb: "env",
		Args: []string{"-json"},
		Env:  runEnv,
	})
	if err != nil {
		return nil, err
	}
	envmap := make(map[string]string)
	if err := json.Unmarshal(output.Bytes(), &envmap); err != nil {
		return nil, err
	}
	return envmap, nil
}
