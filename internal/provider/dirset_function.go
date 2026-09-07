// Copyright (c) HashiCorp, Inc.
// Copyright (c) 2025, 2026 Savely Krasovsky
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ function.Function = dirSetFunction{}

type dirSetFunction struct{}

func (r dirSetFunction) Metadata(_ context.Context, req function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "dirset"
}

func (r dirSetFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Walks the file tree rooted at root and finds all directories",
		Parameters: []function.Parameter{
			function.StringParameter{
				Description: "The path to walk",
				Name:        "path",
			},
			function.StringParameter{
				Description: "The pattern to match",
				Name:        "pattern",
			},
		},
		Return: function.ListReturn{
			ElementType: types.StringType,
		},
	}
}

func (r dirSetFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var (
		path    string
		pattern string
	)

	resp.Error = req.Arguments.Get(ctx, &path, &pattern)
	if resp.Error != nil {
		return
	}

	directories, err := dirset(path, pattern)
	if err != nil {
		resp.Error = function.NewFuncError(err.Error())
		return
	}

	resp.Error = resp.Result.Set(ctx, directories)
}

func dirset(path, pattern string) ([]string, error) {
	dirs := make([]string, 0)

	fsys := os.DirFS(path)
	if err := doublestar.GlobWalk(fsys, pattern, func(path string, d fs.DirEntry) error {
		if !d.IsDir() || path == "." {
			return nil
		}

		dirs = append(dirs, filepath.ToSlash(path))
		return nil
	}); err != nil {
		return nil, err
	}

	return dirs, nil
}
