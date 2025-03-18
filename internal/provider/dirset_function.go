// Copyright (c) HashiCorp, Inc.
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

var (
	_ function.Function = DirSetFunction{}
)

func NewDirSetFunction() function.Function {
	return DirSetFunction{}
}

type DirSetFunction struct{}

func (r DirSetFunction) Metadata(_ context.Context, req function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "dirset"
}

func (r DirSetFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Walks the file tree rooted at root and finds all directories",
		Parameters: []function.Parameter{
			function.StringParameter{
				AllowNullValue:     false,
				AllowUnknownValues: false,
				Description:        "The path to walk",
				Name:               "path",
			},
			function.StringParameter{
				AllowNullValue:     false,
				AllowUnknownValues: false,
				Description:        "The pattern to match",
				Name:               "pattern",
			},
		},
		Return: function.ListReturn{
			ElementType: types.StringType,
		},
	}
}

func (r DirSetFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var (
		path    string
		pattern string
	)

	resp.Error = function.ConcatFuncErrors(req.Arguments.Get(ctx, &path, &pattern))
	if resp.Error != nil {
		return
	}

	dd, err := dirset(path, pattern)
	if err != nil {
		resp.Error = function.NewFuncError(err.Error())
		return
	}

	resp.Error = function.ConcatFuncErrors(resp.Result.Set(ctx, dd))
}

func dirset(path, pattern string) ([]string, error) {
	dirs := make([]string, 0)

	fsys := os.DirFS(path)
	if err := doublestar.GlobWalk(fsys, pattern, func(path string, d fs.DirEntry) error {
		if !d.IsDir() {
			return nil
		}
		if path == "." {
			return nil
		}

		dirs = append(dirs, filepath.ToSlash(path))
		return nil
	}); err != nil {
		return nil, err
	}

	return dirs, nil
}
